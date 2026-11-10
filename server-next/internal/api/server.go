// Package api is Portico's HTTP API. Each area keeps its routes, its request and response
// shapes and its refusals in one file; this file is what they all hang on.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// APIMajor is the number in the path. It changes only for a break old apps could not survive.
const APIMajor = 1

// maxBody bounds a JSON request body. Uploads get their own routes and their own limits.
const maxBody = 64 << 10

// Server answers the API from one database.
type Server struct {
	db     *db.DB
	tokens *auth.Tokens
	// passwords is replaced by tests with a cheap hasher.
	passwords *auth.Passwords
	log       *slog.Logger
	mux       *http.ServeMux
	version   string
	id        string
	identity  identity
	routes    []*Route
	now       func() time.Time
	// setupCode is what creating the owner needs. It is made at start while there is no
	// account, and written to the log and to setupFile, never sent to an app.
	setupCode, setupFile string
	// playback runs the viewing sessions. See playback.go.
	playback *playback.Service
	// queues builds and runs play queues. See queues.go.
	queues  *playqueue.Service
	network networkState
}

// Route is one operation, kept so the contract can be generated and the operation budget counted.
type Route struct {
	Pattern string
	Access  Access
	// Scope is what an API key or personal token needs to call the route. Handle sets it
	// from the access and the method; a route that needs something else says so.
	Scope    Scope
	Request  reflect.Type
	Response reflect.Type
}

// Scope is what a key made for a tool must have been given to use a route. A signed-in
// person is not limited by scopes, and no scope lets a key do more than its account may.
type Scope string

const (
	// ScopeOpen routes need no sign-in at all.
	ScopeOpen Scope = "open"
	// ScopeRead: look at libraries, titles and the account's own state.
	ScopeRead Scope = "read"
	// ScopePlayback: play, and change the account's own watch state and lists.
	ScopePlayback Scope = "playback"
	// ScopeLibrary: scan, match, edit and delete what is in the libraries.
	ScopeLibrary Scope = "library"
	// ScopeAdmin: everything else the account behind the key may administer.
	ScopeAdmin Scope = "admin"
	// ScopeSession routes take only a signed-in person: signing in and out, passwords and
	// two-step sign-in, profiles, devices, and making keys. A key never mints or widens another.
	ScopeSession Scope = "session"
)

// GrantableScopes are the scopes a key can be given.
var GrantableScopes = []Scope{ScopeRead, ScopePlayback, ScopeLibrary, ScopeAdmin}

func defaultScope(pattern string, access Access) Scope {
	switch {
	case access == Public:
		return ScopeOpen
	case access == Admin || access == Owner:
		return ScopeAdmin
	case access == WithProfile && strings.HasPrefix(pattern, "GET "):
		return ScopeRead
	case access == WithProfile:
		return ScopePlayback
	}
	return ScopeSession
}

// Access is who may call a route. It is checked before the body is read.
type Access int

const (
	// Public routes need no sign-in.
	Public Access = iota
	// SignedIn routes need a current access token; a profile need not be selected yet.
	SignedIn
	// WithProfile routes act for a viewing profile, so one must be selected.
	WithProfile
	// Admin routes need an admin or the owner, on that account's main profile.
	Admin
	// Owner routes need the owner, on the owner's main profile.
	Owner
)

// Page is the answer of every list route. NextCursor is absent on the last page; a list
// short enough to come whole never has one.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// None is the request type of a route with no body, and the response type of one that answers 204.
type None struct{}

// New builds the server and registers every area's routes. dataDir is the folder the
// server keeps its files in.
func New(ctx context.Context, d *db.DB, dataDir, version string, log *slog.Logger) (*Server, error) {
	key, err := d.SettingOrCreate(ctx, "auth.signing_key", auth.NewKey)
	if err != nil {
		return nil, err
	}
	tokens, err := auth.NewTokens(key)
	if err != nil {
		return nil, err
	}
	seed, err := d.SettingOrCreate(ctx, "server.identity_key", auth.NewKey)
	if err != nil {
		return nil, err
	}
	identity, err := newIdentity(seed)
	if err != nil {
		return nil, err
	}
	s := &Server{db: d, tokens: tokens, log: log, mux: http.NewServeMux(), version: version, id: identity.id, identity: identity, now: time.Now,
		passwords: auth.NewPasswords(auth.PasswordCost, auth.PINCost)}
	if set, err := d.HasUsers(ctx); err != nil {
		return nil, err
	} else if !set {
		s.setupCode, s.setupFile = auth.NewCode(2), filepath.Join(dataDir, "setup-code.txt")
		if err := os.WriteFile(s.setupFile, []byte(s.setupCode+"\n"), 0o600); err != nil {
			return nil, err
		}
		log.Info("this server has no owner yet: create one with this setup code", "setupCode", s.setupCode, "file", s.setupFile)
	}
	if err := s.loadNetworks(ctx); err != nil {
		return nil, err
	}
	s.systemRoutes()
	s.networkRoutes()
	s.identityRoutes()
	s.authRoutes()
	s.profileRoutes()
	s.preferenceRoutes()
	s.deviceRoutes()
	s.memberRoutes()
	s.invitationRoutes()
	s.keyRoutes()
	s.catalogueRoutes()
	s.playback = playback.New(d, playback.NoEngine{}, playback.NoEngine{}, log, func() time.Time { return s.now() })
	go s.playback.Run(ctx)
	s.playbackRoutes()
	s.queues = playqueue.New(d, s.playback, func() time.Time { return s.now() })
	s.queueRoutes()
	return s, nil
}

// Routes lists every registered operation.
func (s *Server) Routes() []*Route { return s.routes }

type contextKey int

const (
	requestIDKey contextKey = iota
	callerKey
)

// RequestID returns the id this request is logged under and that its error body carries.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// ServeHTTP gives every request an id, turns a panic into the standard error, and routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := db.NewID("req")
	w.Header().Set("X-Request-Id", id)
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				panic(p)
			}
			s.log.Error("request panicked", "requestId", id, "path", r.URL.Path, "panic", p)
			s.writeError(w, r, errors.New("panic"))
		}
	}()
	if _, pattern := s.mux.Handler(r); pattern == "" {
		// A token that is no longer good is told so whatever the address, so an app that
		// asks for something this server does not have still learns to renew its sign-in.
		if strings.HasPrefix(r.URL.Path, "/v1/") && r.Header.Get("Authorization") != "" {
			if _, err := s.admit(r, &Route{Access: SignedIn, Scope: ScopeRead}); err != nil {
				if e, ok := asError(err); !ok || e.Status == http.StatusUnauthorized {
					s.writeError(w, r, err)
					return
				}
			}
		}
		// The mux would answer in plain text. Say the same thing in the API's error format.
		probe := &statusOnly{}
		s.mux.ServeHTTP(probe, r)
		if probe.status == http.StatusMethodNotAllowed {
			w.Header().Set("Allow", probe.Header().Get("Allow"))
			s.writeError(w, r, refusal(http.StatusMethodNotAllowed, "method_not_allowed", "This address does not take that method.", false))
			return
		}
		s.writeError(w, r, ErrNotFound())
		return
	}
	s.mux.ServeHTTP(w, r)
}

type statusOnly struct {
	header http.Header
	status int
}

func (p *statusOnly) Header() http.Header {
	if p.header == nil {
		p.header = http.Header{}
	}
	return p.header
}
func (p *statusOnly) Write(b []byte) (int, error) { return len(b), nil }
func (p *statusOnly) WriteHeader(status int)      { p.status = status }

// Handle registers one JSON operation. pattern is a ServeMux pattern such as
// "POST /v1/profiles/{id}/select". The caller is checked against access, the body is read
// into Req (or must be absent when Req is None), and the reply is Resp as JSON, or 204
// when Resp is None. Returning an *Error refuses the request; any other error is a 500
// that is logged with the request id and tells the client nothing. The returned route can
// be given a Scope other than the default while routes are being registered.
func Handle[Req, Resp any](s *Server, pattern string, access Access, fn func(r *http.Request, req Req) (Resp, error)) *Route {
	noBody := reflect.TypeFor[Req]() == reflect.TypeFor[None]()
	noReply := reflect.TypeFor[Resp]() == reflect.TypeFor[None]()
	route := &Route{pattern, access, defaultScope(pattern, access), reflect.TypeFor[Req](), reflect.TypeFor[Resp]()}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, route)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		var req Req
		if err := readBody(w, r, &req, noBody); err != nil {
			s.writeError(w, r, err)
			return
		}
		resp, err := fn(r, req)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if noReply {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if special, ok := any(resp).(replier); ok {
			if status := special.reply(w.Header()); status != 0 {
				w.WriteHeader(status)
			}
		}
		json.NewEncoder(w).Encode(resp)
	})
	return route
}

// replier is a response that is not a plain 200: it can set headers (an ETag) and return a
// status other than 200 (201 for something created, 202 for a start still preparing).
// Returning 0 leaves the status alone.
type replier interface{ reply(h http.Header) int }

// JSON is a value this server passes through as it is: a device's capability document, or
// facts the media engine writes for the player. The contract calls it unknown.
type JSON = json.RawMessage

// readBody reads a bounded JSON body into out. Fields this server does not know are ignored,
// so a newer app can talk to an older server; /v1/system says what the server understands.
func readBody(w http.ResponseWriter, r *http.Request, out any, noBody bool) error {
	body := http.MaxBytesReader(w, r.Body, maxBody)
	if noBody {
		if n, _ := io.Copy(io.Discard, body); n > 0 {
			return ErrInvalid("This request takes no body.")
		}
		return nil
	}
	// A route whose body is all optional may be sent with none.
	if r.ContentLength == 0 {
		return nil
	}
	if kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); kind != "application/json" {
		return refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send the request as application/json.", false)
	}
	dec := json.NewDecoder(body)
	if err := dec.Decode(out); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return refusal(http.StatusRequestEntityTooLarge, "body_too_large", "The request is too large.", false)
		}
		return ErrInvalid("The request body is not valid JSON for this request.")
	}
	if dec.More() {
		return ErrInvalid("The request body holds more than one JSON value.")
	}
	return nil
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := asError(err)
	if !ok {
		// A client that went away is not a server fault worth a log line.
		if !errors.Is(err, context.Canceled) {
			s.log.Error("request failed", "requestId", RequestID(r.Context()), "method", r.Method, "path", r.URL.Path, "error", err)
		}
		e = refusal(http.StatusInternalServerError, "internal", "The request could not be completed.", false)
	}
	out := *e
	out.RequestID = RequestID(r.Context())
	if out.RetryAfterMs > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt((out.RetryAfterMs+999)/1000, 10))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(out.Status)
	json.NewEncoder(w).Encode(errorBody{out})
}

// Caller is who a request acts for, taken from its access token or its key.
type Caller struct {
	// SessionID is 0 for a request made with a key.
	SessionID int64
	UserID    int64
	// ProfileID is 0 until a profile has been selected.
	ProfileID int64
	// Role is owner, admin or member, or account before a profile is selected. A profile
	// other than the account's main one is always a member, whoever owns the account.
	Role string
	// Manage says the caller is on the account's main profile, and so may change the account
	// itself: its profiles, devices, sessions and password.
	Manage bool
}

// CallerFrom returns the signed-in caller of a route whose access is not Public.
func CallerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey).(Caller)
	return c
}

// keyPrefix starts every API key and personal token, so one is told from a session's token at a glance.
const keyPrefix = "pk_"

// admit checks the bearer token or key against what the route needs.
func (s *Server) admit(r *http.Request, route *Route) (*http.Request, error) {
	if route.Access == Public {
		return r, nil
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return r, ErrUnauthorized()
	}
	var c Caller
	if strings.HasPrefix(token, keyPrefix) {
		key, err := s.db.KeyCaller(r.Context(), auth.Hash(token), s.now())
		switch {
		case errors.Is(err, db.ErrNotFound):
			return r, ErrUnauthorized()
		case err != nil:
			return r, err
		case route.Scope == ScopeSession:
			return r, refusal(http.StatusForbidden, "session_required", "This needs a signed-in person, not a key.", false)
		case !slices.Contains(key.Scopes, string(route.Scope)) && !slices.Contains(key.Scopes, string(ScopeAdmin)):
			return r, refusal(http.StatusForbidden, "insufficient_scope", "This key was not given the scope this needs.", false)
		}
		c = Caller{UserID: key.UserID, ProfileID: key.ProfileID, Role: key.Role}
	} else {
		claims, err := s.tokens.CheckAccess(token, s.now())
		// A token of a session that has since been ended, or whose profile or role has changed,
		// is refused although it has not expired. The app renews and carries on, or signs in.
		if err != nil || s.db.TokenRefused(claims.SessionID, db.TokenStamp{Boot: claims.Boot, Seq: claims.Seq}) {
			return r, ErrUnauthorized()
		}
		c = Caller{SessionID: claims.SessionID, UserID: claims.UserID, ProfileID: claims.ProfileID, Role: claims.Role, Manage: claims.Manage}
	}
	switch {
	case route.Access == WithProfile && c.ProfileID == 0:
		return r, ErrConflict("profile_selection_required", "Choose a profile to continue.")
	case route.Access == Admin && c.Role != "owner" && c.Role != "admin", route.Access == Owner && c.Role != "owner":
		return r, ErrNotPermitted()
	}
	return r.WithContext(context.WithValue(r.Context(), callerKey, c)), nil
}
