// Package apikit makes authorization and wire contracts mandatory when registering routes.
package apikit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

type Access string

const (
	Public        Access = "public"
	Device        Access = "device"
	ViewerLibrary Access = "viewer_library"
	ViewerItem    Access = "viewer_item_restrictions"
	Owner         Access = "owner"
	Admin         Access = "admin_scope"
)

// Scope is what an API key or personal access token must have been given to
// use an operation. A signed-in session is not limited by it: a session is
// bounded by the member's own role and restrictions, which every credential is
// bounded by as well.
//
//	open     — anyone may call it; a key or token changes nothing.
//	read     — look at libraries, titles, the guide and the caller's own state.
//	playback — play, control playback, and change the caller's own watch
//	           state, lists, preferences and recordings.
//	library  — scan, match, edit and delete what is in the libraries.
//	admin    — everything else the member behind the credential may administer:
//	           server settings, profiles, members, devices and sessions.
//	session  — only a signed-in session may call it, each for its own reason:
//	           signing in and out, renewing and registering (a credential is
//	           not a sign-in); passwords, two-factor and PIN recovery (a
//	           credential must not change what proves the person); creating,
//	           listing and ending keys and tokens, ownership and pairing a
//	           screen or a TV code (a credential never mints or widens
//	           another); accepting an invitation (the invitee's own sign-up);
//	           remembered accounts and choosing a profile (sign-in screens on
//	           one device); the networking claim and identity proofs.
//
// A key or token also travels as the apiKey query parameter on a GET or HEAD,
// so an address can work on its own; the server takes it out of the address
// before anything reads the request.
type Scope string

const (
	ScopeOpen     Scope = "open"
	ScopeRead     Scope = "read"
	ScopePlayback Scope = "playback"
	ScopeLibrary  Scope = "library"
	ScopeAdmin    Scope = "admin"
	ScopeSession  Scope = "session"
)

// GrantableScopes are the scopes a key or token can be given, narrowest first.
var GrantableScopes = []Scope{ScopeRead, ScopePlayback, ScopeLibrary, ScopeAdmin}

// ValidScope reports whether s is a scope an operation may declare.
func ValidScope(s Scope) bool {
	switch s {
	case ScopeOpen, ScopeRead, ScopePlayback, ScopeLibrary, ScopeAdmin, ScopeSession:
		return true
	}
	return false
}

// Grantable reports whether s is a scope a key or token may hold.
func Grantable(s Scope) bool {
	for _, scope := range GrantableScopes {
		if scope == s {
			return true
		}
	}
	return false
}

// ScopeAllows reports whether a credential holding granted may use an operation
// that declares needed. Admin is every grantable scope; nothing grants session.
func ScopeAllows(granted []Scope, needed Scope) bool {
	switch needed {
	case ScopeOpen:
		return true
	case ScopeRead, ScopePlayback, ScopeLibrary, ScopeAdmin:
	default:
		return false
	}
	for _, scope := range granted {
		if scope == needed || scope == ScopeAdmin {
			return true
		}
	}
	return false
}

type Lane string

const (
	Default  Lane = "default"
	Browsing Lane = "browsing"
	Realtime Lane = "realtime"
	Security Lane = "security"
	Media    Lane = "media"
)

type Cost string

const (
	Constant       Cost = "constant"
	PageSized      Cost = "page_sized"
	SelectionAsync Cost = "selection_sized_async"
)

// Audience identifies the intended consumer in contracts and documentation.
// It does not grant access or replace Requires, Access, or credential scopes.
type Audience string

const (
	Integrations Audience = "Integrations"
	Clients      Audience = "Clients"
)

// ProtocolCompatibility marks exact foreign protocol templates, whose handlers
// translate into native integration operations. It confers no credential scope.
const ProtocolCompatibility = "integration-compatibility"

type Metadata struct {
	// Audience is mandatory; the registry never supplies a default.
	Audience Audience `json:"audience"`
	// Protocol is empty for native routes, or ProtocolCompatibility for foreign ones.
	Protocol string `json:"protocol,omitempty"`
	ID       string `json:"id"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Summary  string `json:"summary"`
	Access   Access `json:"access"`
	// AdminScope names the administrative area an Admin route belongs to.
	AdminScope string `json:"adminScope,omitempty"`
	// Requires is the scope an API key or personal access token needs. It is
	// mandatory: a route that has not decided cannot be registered.
	Requires  Scope    `json:"requires"`
	Lane      Lane     `json:"lane"`
	Cost      Cost     `json:"cost"`
	BodyLimit int64    `json:"bodyLimit"`
	Query     []string `json:"query,omitempty"`
	// QueryIntegers publishes bounds for integer query parameters named in
	// Query. The handler parses and applies them; this is their contract.
	QueryIntegers []QueryInteger `json:"queryIntegers,omitempty"`
	Headers       []string       `json:"headers,omitempty"`
	// ResponseHeaders describes emitted headers for an explicitly declared
	// status. Headers above remains the request-header parameter contract.
	ResponseHeaders     []ResponseHeader `json:"responseHeaders,omitempty"`
	NoResponseBody      bool             `json:"noResponseBody,omitempty"`
	Errors              []string         `json:"errors"`
	Status              int              `json:"status"`
	ResponseContentType string           `json:"responseContentType,omitempty"`
	RequestContentTypes []string         `json:"requestContentTypes,omitempty"`
	SuccessStatuses     []int            `json:"successStatuses,omitempty"`
	ResponseStatuses    []int            `json:"responseStatuses,omitempty"`
	// OptionalBody lets a route with a body contract also accept no body at all
	// (a DELETE whose body is optional); the handler then sees the zero Request.
	OptionalBody bool `json:"optionalBody,omitempty"`
	SSE          bool `json:"sse,omitempty"`
	// HTTP marks a handler that owns access enforcement and HTTP response semantics.
	HTTP bool `json:"http,omitempty"`
	// Admission names the server pool; Lane describes its broad API work class.
	Admission string `json:"admission,omitempty"`
}

type ResponseHeader struct {
	Status      int    `json:"status"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

// QueryInteger documents an integer query parameter without changing its
// handler. Unless Required or NoDefault, an absent value means Default. Bounds
// refuse the value unless the corresponding fallback or upper clamp is declared.
type QueryInteger struct {
	Name    string `json:"name"`
	Minimum int    `json:"minimum"`
	Default int    `json:"default"`
	Maximum int    `json:"maximum"`
	Clamped bool   `json:"clamped,omitempty"`
	// These flags describe existing permissive parsers. Fallback bounds are
	// extensions, not JSON-Schema bounds that would promise a refusal.
	DefaultBelowMinimum bool `json:"defaultBelowMinimum,omitempty"`
	DefaultAboveMaximum bool `json:"defaultAboveMaximum,omitempty"`
	// DefaultOnInvalid covers non-integer syntax. Parsers that saturate on
	// integer overflow describe that separate range behavior in Description.
	DefaultOnInvalid bool `json:"defaultOnInvalid,omitempty"`
	EmptyIsDefault   bool `json:"emptyIsDefault,omitempty"`
	Required         bool `json:"required,omitempty"`
	// NoDefault omits a scalar default when absence means unbounded traversal
	// or a query-dependent value, rather than a fixed integer.
	NoDefault bool `json:"noDefault,omitempty"`
	// ClampMaximum applies after validation when the accepted maximum is
	// larger than the effective page size (unlike Clamped's unbounded input).
	ClampMaximum int    `json:"clampMaximum,omitempty"`
	Description  string `json:"description,omitempty"`
}

type Route[Request, Response any] struct {
	Metadata
	Handler func(context.Context, *http.Request, Request) (Response, error)
	// RawHandler is for a GET with both a typed JSON long-poll response and
	// text/event-stream frames. Access and query validation still run first.
	RawHandler http.HandlerFunc
}
type Definition struct {
	Metadata
	Request  reflect.Type
	Response reflect.Type
}
type Authorize func(context.Context, *http.Request, Metadata) error

// AuthorizeContext is an Authorize that also returns the context the handler
// runs in, so a route can reuse the principal it was admitted with.
type AuthorizeContext func(context.Context, *http.Request, Metadata) (context.Context, error)

// ScopeGate decides whether the credential a request carries may use an
// operation that declares need. It returns nil for a request that carries no
// key or token, and for one whose key or token holds the scope.
type ScopeGate func(q *http.Request, need Scope) error

type Registry struct {
	handlers       []http.HandlerFunc
	errors         *apierror.Registry
	authorize      Authorize
	authorizeCtx   AuthorizeContext
	scopes         ScopeGate
	rewrite        PathRewrite
	refs           Refs
	routes         []Definition
	bound          map[string]bool
	frozen         bool
	writeErrors    []string
	routeErrors    []string
	metadataPolicy func(Metadata) Metadata
}

// MetadataPolicy adds a server-owned contract policy before each route is
// validated and registered. Install it before registering routes.
func (r *Registry) MetadataPolicy(policy func(Metadata) Metadata) { r.metadataPolicy = policy }

// GateScopes installs the check every route passes before access, body
// parsing or the handler. Public routes pass it too, so a route that only a
// signed-in session may call refuses a key however it is reached.
func (r *Registry) GateScopes(gate ScopeGate) { r.scopes = gate }

// PathRewrite may replace a path value before the handler reads it, such as
// an id that now names something else. q.Pattern is the matched route.
type PathRewrite func(q *http.Request, name, value string) string

// RewritePathValues installs the rewrite every matched path value passes.
func (r *Registry) RewritePathValues(rewrite PathRewrite) { r.rewrite = rewrite }

// Lookup returns the route that would serve method and escaped path.
func (r *Registry) Lookup(method, escapedPath string) (Metadata, bool) {
	best, score := -1, -1
	for i, route := range r.routes {
		if route.Method != method {
			continue
		}
		if _, rank, ok := matchRoute(route.Path, escapedPath); ok && rank > score {
			best, score = i, rank
		}
	}
	if best < 0 {
		return Metadata{}, false
	}
	return r.routes[best].Metadata, true
}

// RawRoute is for the few routes that write their own response (event
// streams): access, lane, cost and query rules still apply, and Response
// documents the JSON form the route also serves.
type RawRoute[Request, Response any] struct {
	Metadata
	Serve func(http.ResponseWriter, *http.Request)
}

type replyKey struct{}
type reply struct {
	releases []func()
	status   int
	header   http.Header
	// For a GET: what the client holds (If-None-Match), and whether the handler
	// found it current before composing anything (Conditional).
	ifNoneMatch string
	conditional bool
	notModified bool
}

// SetStatus overrides the route's declared success status for this request (a
// 202 while a resource is being prepared). Only 2xx values are accepted.
func SetStatus(ctx context.Context, status int) {
	if r, ok := ctx.Value(replyKey{}).(*reply); ok && status >= 200 && status < 300 {
		r.status = status
	}
}

// SetHeader adds a response header on success (Retry-After, a revision ETag,
// Report-Every-Ms). An ETag set here replaces the body-digest ETag of a GET.
func SetHeader(ctx context.Context, key, value string) {
	if r, ok := ctx.Value(replyKey{}).(*reply); ok && !strings.ContainsAny(key+value, "\r\n") {
		r.header.Set(key, value)
	}
}

// Conditional names this GET response's validator before the handler composes
// it, and reports whether the client already holds that version. On true the
// handler returns at once with a zero response and no error: the registry
// answers 304 without building, marshalling or hashing a body. The tag must
// change whenever the bytes could (revisions, the viewer's authority fence,
// the request's own parameters); it replaces the body-digest validator.
func Conditional(ctx context.Context, tag string) bool {
	r, ok := ctx.Value(replyKey{}).(*reply)
	if !ok || !r.conditional || tag == "" || strings.ContainsAny(tag, "\"\r\n") {
		return false
	}
	quoted := "\"" + tag + "\""
	r.header.Set("ETag", quoted)
	r.notModified = holds(r.ifNoneMatch, quoted)
	return r.notModified
}

// holds is the If-None-Match comparison: a list of entity tags, or `*`. The
// weak comparison applies, so W/ and the strong form match.
func holds(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate != "" && strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(tag, "W/") {
			return true
		}
	}
	return false
}

func New(authorize Authorize, catalogue *apierror.Registry) *Registry {
	if catalogue == nil {
		catalogue, _ = apierror.New()
	}
	return &Registry{authorize: authorize, errors: catalogue}
}

// NewContextual is New with an authorizer that passes a context to the handler.
func NewContextual(authorize AuthorizeContext, catalogue *apierror.Registry) *Registry {
	r := New(nil, catalogue)
	if authorize != nil {
		r.authorizeCtx = authorize
		r.authorize = func(ctx context.Context, q *http.Request, m Metadata) error {
			_, err := authorize(ctx, q, m)
			return err
		}
	}
	return r
}

// WriteErrors names errors every route that changes something can answer, such
// as a storage layer refusing a write it has no room to queue. They are added
// to each later non-GET route's declared errors. Call it before registering.
func (r *Registry) WriteErrors(codes ...string) {
	r.writeErrors = append(r.writeErrors, codes...)
}

// RouteErrors names errors every route can answer, reads included, such as a
// storage layer briefly refusing new work while it recovers. They are added to
// each later route's declared errors. Call it before registering.
func (r *Registry) RouteErrors(codes ...string) {
	r.routeErrors = append(r.routeErrors, codes...)
}

// Errors is the catalogue routes on this registry write errors from.
func (r *Registry) Errors() *apierror.Registry { return r.errors }

// admit applies access and the query allow-list. It returns the request the
// handler must use, or false after writing the error.
func (r *Registry) admit(w http.ResponseWriter, q *http.Request, m Metadata) (*http.Request, bool) {
	fail := func(err error) { r.errors.Write(w, "", err) }
	// A key or token is held to the route's declared scope before anything else.
	if r.scopes != nil {
		if err := r.scopes(q, m.Requires); err != nil {
			fail(err)
			return nil, false
		}
	}
	// Access precedes body parsing and conditional responses.
	if m.Access != Public {
		if r.authorizeCtx != nil {
			ctx, err := r.authorizeCtx(q.Context(), q, m)
			if err != nil {
				fail(err)
				return nil, false
			}
			if ctx != nil {
				q = q.WithContext(ctx)
			}
		} else if err := r.authorize(q.Context(), q, m); err != nil {
			fail(err)
			return nil, false
		}
	}
	allowed := map[string]bool{}
	for _, key := range m.Query {
		allowed[key] = true
	}
	var fields []apierror.Field
	for key := range q.URL.Query() {
		if !allowed[key] {
			fields = append(fields, apierror.Field{Path: key, Code: "unknown_field", Message: "This field is not recognized."})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
	if len(fields) > 0 {
		fail(&apierror.Error{Code: "invalid_request", Fields: fields})
		return nil, false
	}
	return q, true
}

func (r *Registry) define(m Metadata, request, response reflect.Type, access bool) error {
	if r.frozen {
		return errors.New("registry frozen")
	}
	if r.metadataPolicy != nil {
		m = r.metadataPolicy(m)
	}
	for _, code := range r.routeErrors {
		if !slices.Contains(m.Errors, code) {
			m.Errors = append(append([]string{}, m.Errors...), code)
		}
	}
	if m.Method != "GET" && m.Method != "HEAD" {
		for _, code := range r.writeErrors {
			if !slices.Contains(m.Errors, code) {
				m.Errors = append(append([]string{}, m.Errors...), code)
			}
		}
	}
	if err := validate(m); err != nil {
		return err
	}
	if !access || m.Access != Public && r.authorize == nil {
		return errors.New("handler and access enforcement required")
	}
	for _, code := range m.Errors {
		if _, ok := r.errors.Definition(code); !ok {
			return fmt.Errorf("route %s: unregistered route error %q", m.ID, code)
		}
	}
	for _, d := range r.routes {
		if d.ID == m.ID || d.Method == m.Method && d.Path == m.Path {
			return errors.New("duplicate route")
		}
	}
	r.routes = append(r.routes, Definition{m, request, response})
	return nil
}

// RegisterRaw registers a route that writes its own response after admission.
func RegisterRaw[Request, Response any](r *Registry, route RawRoute[Request, Response]) error {
	m := route.Metadata
	if err := r.define(m, reflect.TypeFor[Request](), reflect.TypeFor[Response](), route.Serve != nil); err != nil {
		return err
	}
	r.handlers = append(r.handlers, func(w http.ResponseWriter, q *http.Request) {
		if q, ok := r.admit(w, q, m); ok {
			route.Serve(w, q)
		}
	})
	return nil
}
func (r *Registry) Definitions() []Definition               { return append([]Definition{}, r.routes...) }
func (r *Registry) ErrorDefinitions() []apierror.Definition { return r.errors.Definitions() }
func (r *Registry) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	best, score := -1, -1
	var values map[string]string
	for i, route := range r.routes {
		if route.Method != q.Method {
			continue
		}
		matched, rank, ok := matchRoute(route.Path, q.URL.EscapedPath())
		if ok && rank > score {
			best, score, values = i, rank, matched
		}
	}
	if best < 0 {
		r.errors.Write(w, "", &apierror.Error{Code: "not_found"})
		return
	}
	q.Pattern = r.routes[best].Method + " " + r.routes[best].Path
	for key, value := range values {
		if r.rewrite != nil {
			value = r.rewrite(q, key, value)
		}
		q.SetPathValue(key, value)
	}
	if !r.routes[best].HTTP {
		// A mounted HTTP handler rewrites its own query on the way in.
		r.rewriteQuery(q)
	}
	r.handlers[best](w, q)
}
func matchRoute(pattern, path string) (map[string]string, int, bool) {
	want, got := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return nil, 0, false
	}
	values := map[string]string{}
	score := 0
	for i, part := range want {
		actual, err := url.PathUnescape(got[i])
		if err != nil || strings.Contains(actual, "/") {
			return nil, 0, false
		}
		if strings.HasPrefix(part, "{") {
			end := strings.IndexByte(part, '}')
			if end < 2 {
				return nil, 0, false
			}
			suffix := part[end+1:]
			if !strings.HasSuffix(actual, suffix) {
				return nil, 0, false
			}
			id := strings.TrimSuffix(actual, suffix)
			valid := opaquePathID(id)
			if pattern == "/v1/home/rows/{id}" && part == "{id}" {
				valid = homeRowPathID(id)
			}
			if pattern == "/v1/file-info-queries/{queryId}" && part == "{queryId}" {
				valid = fileInfoQueryPathID(id)
			}
			if !valid {
				return nil, 0, false
			}
			values[part[1:end]] = id
			score += len(suffix)
		} else {
			if part != actual {
				return nil, 0, false
			}
			score += len(part) + 100
		}
	}
	return values, score, true
}

func opaquePathID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// FileInfo preparation has one bounded composite authenticated handle. Keep
// this grammar at its private route; ordinary path IDs remain unchanged.
func fileInfoQueryPathID(id string) bool {
	if opaquePathID(id) {
		return true
	}
	if tail, ok := strings.CutPrefix(id, "open."); ok {
		return len(tail) == 43 && opaquePathID(tail)
	}
	return len(id) == 338 && id[183] == '.' && fileInfoTokenPart(id[:183]) && fileInfoTokenPart(id[184:])
}
func fileInfoTokenPart(id string) bool {
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return id != ""
}

// Home publishes saved-view and recommendation keys, not only opaque IDs.
// Keep this grammar at its one existing route; the handler still resolves the
// exact published key under the current viewer and library authority.
func homeRowPathID(id string) bool {
	if !utf8.ValidString(id) || utf8.RuneCountInString(id) > 128 {
		return false
	}
	if opaquePathID(id) {
		return true
	}
	if tail, ok := strings.CutPrefix(id, "view:"); ok {
		return opaquePathID(tail)
	}
	switch id {
	case "for_you:franchise", "for_you:series", "for_you:rediscover", "for_you:short", "for_you:gems", "for_you:top":
		return true
	}
	for _, prefix := range []string{"for_you:genre:", "for_you:person:", "for_you:tag:"} {
		if tail, ok := strings.CutPrefix(id, prefix); ok {
			if tail == "" || strings.TrimSpace(tail) != tail || tail == "." || tail == ".." {
				return false
			}
			for _, c := range tail {
				if !unicode.IsPrint(c) || strings.ContainsRune("/\\%?#", c) {
					return false
				}
			}
			return true
		}
	}
	return false
}
func (r *Registry) Freeze() { r.frozen = true }
func exactCompatibilityPath(path string) bool {
	if !strings.HasPrefix(path, "/") || path == "/" || strings.HasSuffix(path, "/") {
		return false
	}
	names := map[string]bool{}
	for index, part := range strings.Split(path[1:], "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := part[1 : len(part)-1]
			if index == 0 || name == "" || names[name] {
				return false
			}
			for i, c := range name {
				if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
					return false
				}
			}
			names[name] = true
			continue
		}
		if strings.ContainsAny(part, "{}*?#\\%:$") {
			return false
		}
		for _, c := range part {
			if unicode.IsSpace(c) || unicode.IsControl(c) {
				return false
			}
		}
	}
	return true
}

func validate(m Metadata) error {
	if m.Audience != Integrations && m.Audience != Clients {
		return errors.New("route audience must be Integrations or Clients")
	}
	nativePath := strings.HasPrefix(m.Path, "/v1/") || strings.HasPrefix(m.Path, "/health/")
	switch m.Protocol {
	case "":
		if !nativePath {
			return errors.New("foreign route requires the compatibility protocol marker")
		}
	case ProtocolCompatibility:
		if m.Audience != Integrations || nativePath || !exactCompatibilityPath(m.Path) {
			return errors.New("compatibility route requires Integrations and an exact foreign path template")
		}
	default:
		return errors.New("unknown route protocol")
	}
	if m.ID == "" || m.Summary == "" || m.Status < 200 || m.Status >= 300 || m.BodyLimit < 0 || m.BodyLimit > 1<<30 {
		return errors.New("route metadata incomplete")
	}
	// Reads take no body; a body limit on one would publish a request body
	// in the generated contracts.
	if (m.Method == "GET" || m.Method == "HEAD") && m.BodyLimit != 0 {
		return errors.New("read route declares a request body")
	}
	for _, integer := range m.QueryIntegers {
		if !slices.Contains(m.Query, integer.Name) || integer.Minimum > integer.Maximum || !integer.Required && !integer.NoDefault && (integer.Minimum > integer.Default || integer.Default > integer.Maximum) {
			return fmt.Errorf("route %q: query integer %q must be a declared query parameter with minimum <= default <= maximum", m.ID, integer.Name)
		}
		if integer.Clamped && integer.DefaultAboveMaximum || integer.Required && (integer.DefaultBelowMinimum || integer.DefaultAboveMaximum || integer.DefaultOnInvalid || integer.EmptyIsDefault) {
			return fmt.Errorf("query integer %q has conflicting fallback policies", integer.Name)
		}
		if integer.ClampMaximum != 0 && (integer.Clamped || integer.DefaultAboveMaximum || integer.ClampMaximum < integer.Minimum || integer.ClampMaximum > integer.Maximum) {
			return fmt.Errorf("query integer %q has an invalid post-validation clamp", integer.Name)
		}
		if integer.NoDefault && (integer.DefaultBelowMinimum || integer.DefaultAboveMaximum || integer.DefaultOnInvalid) {
			return fmt.Errorf("query integer %q needs a default for its fallback policies", integer.Name)
		}
	}
	for _, status := range m.SuccessStatuses {
		if status < 200 || status >= 300 {
			return errors.New("invalid success status")
		}
	}
	for _, status := range m.ResponseStatuses {
		if status < 100 || status > 599 {
			return errors.New("invalid response status")
		}
	}
	seenResponseHeaders := map[string]bool{}
	for _, header := range m.ResponseHeaders {
		if header.Status != m.Status && !slices.Contains(m.SuccessStatuses, header.Status) && !slices.Contains(m.ResponseStatuses, header.Status) {
			return errors.New("response header status must be declared")
		}
		if header.Name == "" || header.Description == "" {
			return errors.New("response header name and description required")
		}
		for _, c := range header.Name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
				return errors.New("invalid response header name")
			}
		}
		key := fmt.Sprint(header.Status) + ":" + strings.ToLower(header.Name)
		if seenResponseHeaders[key] {
			return errors.New("duplicate response header")
		}
		seenResponseHeaders[key] = true
	}
	for _, typ := range m.RequestContentTypes {
		if _, _, err := mime.ParseMediaType(typ); err != nil {
			return errors.New("invalid request content type")
		}
	}
	if m.ResponseContentType != "" {
		if _, _, e := mime.ParseMediaType(m.ResponseContentType); e != nil {
			return errors.New("invalid response content type")
		}
	}
	switch m.Access {
	case Public, Device, ViewerLibrary, ViewerItem, Owner:
	case Admin:
		if m.AdminScope == "" {
			return errors.New("admin scope required")
		}
	default:
		return errors.New("access rule required")
	}
	// An access label is a promise the authorizer keeps from the path. A route
	// labelled for one title or one library must name it where the authorizer
	// looks, or the label would say a check runs that does not.
	if m.Access == ViewerItem && !strings.Contains(m.Path, "/{itemId}") {
		return errors.New("a viewer_item route names its title as {itemId}")
	}
	if m.Access == ViewerLibrary && !strings.Contains(m.Path, "/{libraryId}") {
		return errors.New("a viewer_library route names its library as {libraryId}")
	}
	if !ValidScope(m.Requires) {
		return errors.New("credential scope required")
	}
	// An operation anyone may call is either open or reserved for signing in;
	// one that needs a caller needs a scope a credential could be given, or is
	// reserved for sessions.
	if m.Access == Public && m.Requires != ScopeOpen && m.Requires != ScopeSession {
		return errors.New("a public route is open or session-only")
	}
	if m.Access != Public && m.Requires == ScopeOpen {
		return errors.New("an authenticated route needs a scope")
	}
	switch m.Lane {
	case Default, Browsing, Realtime, Security, Media:
	default:
		return errors.New("lane required")
	}
	switch m.Cost {
	case Constant, PageSized, SelectionAsync:
	default:
		return errors.New("cost class required")
	}
	switch m.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return errors.New("invalid method")
	}
	return nil
}
func Register[Request, Response any](r *Registry, route Route[Request, Response]) error {
	m := route.Metadata
	// Integration merge: lane C's SSE raw handler (886f5011) on BE-playback's
	// define/admit registry; exactly one of Handler and RawHandler.
	if (route.Handler == nil) == (route.RawHandler == nil) {
		return errors.New("handler and access enforcement required")
	}
	if route.RawHandler != nil && (!m.SSE || m.Method != "GET" || m.BodyLimit != 0) || route.RawHandler == nil && m.SSE {
		return errors.New("streaming route must be a bodyless GET with a raw handler")
	}
	// A change to something that exists names the revision it was made
	// against. The route says so in its contract without having to remember to.
	fenced := route.RawHandler == nil && revisionFenced(m.Method, reflect.TypeFor[Request]())
	if fenced && !slices.Contains(m.Errors, "revision_required") {
		m.Errors = append(append([]string{}, m.Errors...), "revision_required")
	}
	if err := r.define(m, reflect.TypeFor[Request](), reflect.TypeFor[Response](), true); err != nil {
		return err
	}
	refs := r.bodyRefPaths(reflect.TypeFor[Request]())
	r.handlers = append(r.handlers, func(w http.ResponseWriter, q *http.Request) {
		fail := func(err error) { r.errors.Write(w, "", err) }
		q, ok := r.admit(w, q, m)
		if !ok {
			return
		}
		if route.RawHandler != nil {
			if q.ContentLength != 0 || len(q.TransferEncoding) != 0 {
				fail(&apierror.Error{Code: "invalid_request"})
				return
			}
			route.RawHandler(w, q)
			return
		}
		var request Request
		empty := q.ContentLength == 0 && len(q.TransferEncoding) == 0
		if m.BodyLimit > 0 && !(m.OptionalBody && empty && q.Header.Get("Content-Type") == "") {
			typ, _, err := mime.ParseMediaType(q.Header.Get("Content-Type"))
			if err != nil || (typ != "application/json" && !(m.Method == "PATCH" && typ == "application/merge-patch+json")) {
				fail(&apierror.Error{Code: "unsupported_media_type"})
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, q.Body, m.BodyLimit))
			if err != nil {
				fail(&apierror.Error{Code: "body_too_large"})
				return
			}
			if err = decodeStrict(raw, &request); err != nil {
				fail(&apierror.Error{Code: "invalid_request", Cause: err})
				return
			}
			if fenced && !revisionSent(raw) {
				fail(&apierror.Error{Code: "revision_required"})
				return
			}
			// An id that now names something else (a merged-away title) is
			// replaced once the document is known to be valid.
			if next := r.rewriteBody(q, raw, refs); !bytes.Equal(next, raw) {
				request = *new(Request)
				if err = json.Unmarshal(next, &request); err != nil {
					fail(&apierror.Error{Code: "invalid_request", Cause: err})
					return
				}
			}
		} else if m.BodyLimit == 0 && !empty {
			fail(&apierror.Error{Code: "invalid_request"})
			return
		}
		out := &reply{header: http.Header{}, conditional: m.Method == "GET", ifNoneMatch: q.Header.Get("If-None-Match")}
		q = q.WithContext(context.WithValue(q.Context(), replyKey{}, out))
		defer out.release()
		response, err := route.Handler(q.Context(), q, request)
		if err != nil {
			fail(err)
			return
		}
		status := m.Status
		if out.status != 0 {
			status = out.status
		}
		for key, values := range out.header {
			w.Header()[key] = values
		}
		if status == 204 {
			w.WriteHeader(204)
			return
		}
		// A validator the handler named (Conditional, or a revision through
		// SetHeader) is compared before the body is marshalled: an unchanged
		// answer costs no encoding, hashing or writing.
		if m.Method == "GET" && (out.notModified || w.Header().Get("ETag") != "" && holds(out.ifNoneMatch, w.Header().Get("ETag"))) {
			w.Header().Set("Cache-Control", "private, no-cache")
			w.WriteHeader(304)
			return
		}
		raw, err := marshalResponse(response)
		if err != nil {
			fail(err)
			return
		}
		if m.Method == "GET" {
			tag := w.Header().Get("ETag")
			if tag == "" {
				sum := sha256.Sum256(raw)
				tag = "\"" + hex.EncodeToString(sum[:]) + "\""
				w.Header().Set("ETag", tag)
			}
			w.Header().Set("Cache-Control", "private, no-cache")
			if holds(out.ifNoneMatch, tag) {
				w.WriteHeader(304)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	})
	return nil
}

// revisionFenced reports whether a body of this type, sent with this method,
// changes something that already exists at a numbered revision: a PUT, PATCH
// or DELETE whose body has an integer expectedRevision. A POST shares body
// types with creations and is not fenced by this rule.
func revisionFenced(method string, body reflect.Type) bool {
	if method != "PUT" && method != "PATCH" && method != "DELETE" {
		return false
	}
	for body != nil && body.Kind() == reflect.Pointer {
		body = body.Elem()
	}
	if body == nil || body.Kind() != reflect.Struct {
		return false
	}
	field, ok := body.FieldByName("ExpectedRevision")
	return ok && field.Type.Kind() == reflect.Int64 && strings.Split(field.Tag.Get("json"), ",")[0] == "expectedRevision"
}

// revisionSent reports whether the body names its expected revision. Leaving
// it out, or sending null, must never be read as revision 0: that would let a
// request that knows nothing about the current state replace it. An explicit
// 0 is a revision like any other.
func revisionSent(raw []byte) bool {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	revision, ok := body["expectedRevision"]
	return ok && !bytes.Equal(bytes.TrimSpace(revision), []byte("null"))
}
func marshalResponse(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Enforce the no-null response contract instead of silently inventing values.
	if err = nullFree(reflect.ValueOf(v), "response"); err != nil {
		return nil, err
	}
	if len(raw) > 512<<10 {
		return nil, errors.New("response exceeds page budget")
	}
	return raw, nil
}
func decodeStrict(raw []byte, out any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("a request document cannot be null")
	}
	// A token walk rejects duplicate keys, which encoding/json would otherwise overwrite.
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	if err := uniqueValue(tokens); err != nil {
		return err
	}
	if _, err := tokens.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	var document any
	values := json.NewDecoder(bytes.NewReader(raw))
	values.UseNumber()
	if err := values.Decode(&document); err != nil {
		return err
	}
	if err := exactFieldNames(document, reflect.TypeOf(out)); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

// encoding/json accepts case-folded struct fields even with DisallowUnknownFields.
// Strict request contracts require the exact published JSON names, recursively.
func exactFieldNames(value any, kind reflect.Type) error {
	if kind == nil {
		return nil
	}
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	if reflect.PointerTo(kind).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return nil
	}
	switch kind.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil // The typed decoder reports the value-kind error.
		}
		fields := make(map[string]reflect.Type, kind.NumField())
		var collect func(reflect.Type, int)
		collect = func(current reflect.Type, depth int) {
			for current.Kind() == reflect.Pointer {
				current = current.Elem()
			}
			if current.Kind() != reflect.Struct || depth > 16 {
				return
			}
			for i := range current.NumField() {
				field := current.Field(i)
				if !field.IsExported() {
					continue
				}
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if name == "-" {
					continue
				}
				if field.Anonymous && name == "" {
					collect(field.Type, depth+1)
					continue
				}
				if name == "" {
					name = field.Name
				}
				fields[name] = field.Type
			}
		}
		collect(kind, 0)
		for name, child := range object {
			field, ok := fields[name]
			if !ok {
				return errors.New("unknown or case-mismatched key")
			}
			if err := exactFieldNames(child, field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if array, ok := value.([]any); ok {
			for _, child := range array {
				if err := exactFieldNames(child, kind.Elem()); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if object, ok := value.(map[string]any); ok {
			for _, child := range object {
				if err := exactFieldNames(child, kind.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func uniqueValue(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || keys[s] {
				return errors.New("duplicate or invalid key")
			}
			keys[s] = true
			if err = uniqueValue(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err = uniqueValue(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	_, err = d.Token()
	return err
}

// CanonicalJSON rejects duplicate keys and trailing values before normalizing key order.
func CanonicalJSON(raw []byte) ([]byte, error) {
	var value any
	if err := decodeStrict(raw, &value); err != nil {
		return nil, err
	}
	// UseNumber preserves integers above float64's exact range for validation by
	// the typed handler; normalization must not collapse distinct large inputs.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// nullFree finds an accidental null in a response: a nil slice, map or
// interface, which encode as null where the contract promises a list, an object
// or a value. A nil pointer is the one intended null: the contract generator
// declares an always-sent pointer "required and nullable" (an unset ETA, an
// unknown count). Fields follow encoding/json: "-" is skipped, an omitempty
// field that is empty is not sent, embedded structs are flattened. A value with
// its own MarshalJSON is checked by scanning what it produces.
func nullFree(v reflect.Value, path string) error {
	if !v.IsValid() {
		return fmt.Errorf("response contains null at %s", path)
	}
	if v.Kind() != reflect.Pointer && v.Kind() != reflect.Interface && v.Type().Implements(marshalerType) {
		return marshaledNullFree(v.Interface(), path)
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		if v.Type().Implements(marshalerType) {
			return marshaledNullFree(v.Interface(), path)
		}
		return nullFree(v.Elem(), path)
	case reflect.Interface:
		if v.IsNil() {
			return fmt.Errorf("response contains null at %s", path)
		}
		return nullFree(v.Elem(), path)
	case reflect.Slice:
		if v.IsNil() {
			return fmt.Errorf("response contains null at %s", path)
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		fallthrough
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := nullFree(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return fmt.Errorf("response contains null at %s", path)
		}
		entries := v.MapRange()
		for entries.Next() {
			if err := nullFree(entries.Value(), fmt.Sprintf("%s.%v", path, entries.Key())); err != nil {
				return err
			}
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() && !field.Anonymous {
				continue
			}
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, options, _ := strings.Cut(tag, ",")
			value := v.Field(i)
			if field.Anonymous && name == "" {
				if value.Kind() == reflect.Pointer {
					if value.IsNil() {
						continue
					}
					value = value.Elem()
				}
				if value.Kind() == reflect.Struct {
					if err := nullFree(value, path); err != nil {
						return err
					}
					continue
				}
			}
			if !field.IsExported() {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if strings.Contains(","+options+",", ",omitempty,") && emptyJSON(value) {
				continue
			}
			if err := nullFree(value, path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

var marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

func marshaledNullFree(v any, path string) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if token == nil {
			return fmt.Errorf("response contains null at %s", path)
		}
	}
}

// emptyJSON is encoding/json's omitempty test.
func emptyJSON(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
