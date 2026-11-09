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
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// APIMajor is the number in the path. It changes only for a break old apps could not survive.
const APIMajor = 1

// maxBody bounds a JSON request body. Uploads get their own routes and their own limits.
const maxBody = 64 << 10

// Server answers the API from one database.
type Server struct {
	db      *db.DB
	tokens  *auth.Tokens
	log     *slog.Logger
	mux     *http.ServeMux
	version string
	id      string
	routes  []Route
	now     func() time.Time
}

// Route is one operation, kept so the contract can be generated and the operation budget counted.
type Route struct {
	Pattern  string
	Access   Access
	Request  reflect.Type
	Response reflect.Type
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

// None is the request type of a route with no body, and the response type of one that answers 204.
type None struct{}

// New builds the server and registers every area's routes.
func New(ctx context.Context, d *db.DB, version string, log *slog.Logger) (*Server, error) {
	key, err := d.SettingOrCreate(ctx, "auth.signing_key", auth.NewKey)
	if err != nil {
		return nil, err
	}
	tokens, err := auth.NewTokens(key)
	if err != nil {
		return nil, err
	}
	id, err := d.SettingOrCreate(ctx, "server.id", func() string { return db.NewID("srv") })
	if err != nil {
		return nil, err
	}
	s := &Server{db: d, tokens: tokens, log: log, mux: http.NewServeMux(), version: version, id: id, now: time.Now}
	s.systemRoutes()
	return s, nil
}

// Routes lists every registered operation.
func (s *Server) Routes() []Route { return s.routes }

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
		// The mux would answer in plain text. Say the same thing in the API's error format.
		probe := &statusOnly{}
		s.mux.ServeHTTP(probe, r)
		if probe.status == http.StatusMethodNotAllowed {
			w.Header().Set("Allow", probe.Header().Get("Allow"))
			s.writeError(w, r, refusal(http.StatusMethodNotAllowed, "method_not_allowed", "This address does not take that method.", RetryNever))
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
// that is logged with the request id and tells the client nothing.
func Handle[Req, Resp any](s *Server, pattern string, access Access, fn func(r *http.Request, req Req) (Resp, error)) {
	noBody := reflect.TypeFor[Req]() == reflect.TypeFor[None]()
	noReply := reflect.TypeFor[Resp]() == reflect.TypeFor[None]()
	s.routes = append(s.routes, Route{pattern, access, reflect.TypeFor[Req](), reflect.TypeFor[Resp]()})
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, access)
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
		json.NewEncoder(w).Encode(resp)
	})
}

// readBody reads a bounded JSON body into out. Unknown fields are refused, so a typing
// mistake is not silently ignored; /v1/system tells an app what this server understands.
func readBody(w http.ResponseWriter, r *http.Request, out any, noBody bool) error {
	body := http.MaxBytesReader(w, r.Body, maxBody)
	if noBody {
		if n, _ := io.Copy(io.Discard, body); n > 0 {
			return ErrInvalid("This request takes no body.")
		}
		return nil
	}
	if kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); kind != "application/json" {
		return refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send the request as application/json.", RetryNever)
	}
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return refusal(http.StatusRequestEntityTooLarge, "body_too_large", "The request is too large.", RetryNever)
		}
		if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return ErrInvalid("Check the request and try again.",
				Field{Path: strings.Trim(field, `"`), Code: "unknown_field", Message: "This field is not recognized."})
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
		e = refusal(http.StatusInternalServerError, "internal", "The request could not be completed.", RetryNever)
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

// Caller is who a request acts for, taken from its access token.
type Caller struct {
	SessionID int64
	UserID    int64
	// ProfileID is 0 until a profile has been selected.
	ProfileID int64
	// Role is owner, admin or member, or account before a profile is selected. A profile
	// other than the account's main one is always a member, whoever owns the account.
	Role string
	// Manage says the caller may change the account itself: its profiles, devices and sessions.
	Manage bool
}

// CallerFrom returns the signed-in caller of a route whose access is not Public.
func CallerFrom(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey).(Caller)
	return c
}

// admit checks the bearer token against what the route needs.
func (s *Server) admit(r *http.Request, access Access) (*http.Request, error) {
	if access == Public {
		return r, nil
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return r, ErrUnauthorized()
	}
	claims, err := s.tokens.CheckAccess(token, s.now())
	if err != nil {
		return r, ErrUnauthorized()
	}
	c := Caller{SessionID: claims.SessionID, UserID: claims.UserID, ProfileID: claims.ProfileID, Role: claims.Role, Manage: claims.Manage}
	switch {
	case access == WithProfile && c.ProfileID == 0:
		return r, ErrConflict("profile_selection_required", "Choose a profile to continue.")
	case access == Admin && c.Role != "owner" && c.Role != "admin", access == Owner && c.Role != "owner":
		return r, ErrNotPermitted()
	}
	return r.WithContext(context.WithValue(r.Context(), callerKey, c)), nil
}
