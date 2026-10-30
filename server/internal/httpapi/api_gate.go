package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// The credential gate is where an API key or personal access token meets the
// server. It runs before admission and before any handler, for every request,
// and it is the only place a credential-shaped bearer becomes usable:
//
//  1. The secret is looked up. Unknown, revoked or expired is 401.
//  2. The route's declared scope is compared with the scopes the credential
//     was given. A session-only route, or a scope it lacks, is 403. A route
//     nothing has classified is session-only.
//  3. The credential is held to its own rate and its own share of the server,
//     so one script cannot starve the people using it or the owner's console.
//  4. The request context is stamped. identity.AuthenticateContext accepts a
//     credential-shaped bearer only with that stamp, so no handler can treat a
//     key as an unscoped session by authenticating it some other way.
//
// What the member behind the credential may do is not decided here. The
// handlers decide that exactly as they do for a session, against live state.

const (
	// A credential may have this many ordinary requests in flight, and all
	// credentials together this many. Both are far below the lanes' capacities:
	// the difference is what stays free for people.
	credentialConcurrency       = 4
	credentialConcurrencyGlobal = 16
	// Long-lived requests (the event feed, waits) are counted apart, because
	// one of them held open is not load.
	credentialStreams       = 2
	credentialStreamsGlobal = 64

	credentialResolveConcurrency = 4
	credentialResolveBudget      = 2 * time.Second
	credentialResolveTTL         = 15 * time.Second
	credentialResolveEntries     = 1024
	credentialBuckets            = 4096
	credentialUseFlush           = time.Minute
)

func credentialErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "insufficient_scope", Status: 403, Message: "This API key or access token was not given permission to do this.", Retry: apierror.Never},
		{Code: "session_required", Status: 403, Message: "Sign in to do this. An API key or access token cannot.", Retry: apierror.Never},
		{Code: "credential_busy", Status: 429, Message: "This API key or access token has too many requests running. Try again shortly.", Retry: apierror.SameRequest},
		{Code: "access_tokens_off", Status: 403, Message: "The owner of this server has turned off personal access tokens.", Retry: apierror.Never},
		{Code: "credential_limit", Status: 409, Message: "There are too many of these already. Revoke one you no longer use.", Retry: apierror.Never},
		{Code: "credential_unavailable", Status: 403, Message: "API keys and access tokens are not available for this sign-in.", Retry: apierror.Never},
	}
}

type resolvedEntry struct {
	credential access.ResolvedCredential
	expires    time.Time
	authority  uint64
}

type credentialBucket struct {
	tokens float64
	at     time.Time
}

type credentialGate struct {
	mu              sync.Mutex
	resolved        map[string]resolvedEntry
	buckets         map[string]*credentialBucket
	inflight        map[string]int
	streams         map[string]int
	total           int
	streamed        int
	uses            map[string]access.CredentialUse
	flushMu         sync.Mutex
	useTimer        *time.Timer
	closed          bool
	resolving       chan struct{}
	resolveFailures limiter
}

func newCredentialGate() *credentialGate {
	return &credentialGate{resolved: map[string]resolvedEntry{}, buckets: map[string]*credentialBucket{}, inflight: map[string]int{}, streams: map[string]int{}, uses: map[string]access.CredentialUse{}, resolving: make(chan struct{}, credentialResolveConcurrency)}
}

type credentialNestedKey struct{}

// withNestedCredential marks a request the server issues to itself on behalf
// of a credential it has already admitted and charged (an MCP tool call). The
// scope check still runs; the rate and concurrency charge is not taken twice.
func withNestedCredential(ctx context.Context) context.Context {
	return context.WithValue(ctx, credentialNestedKey{}, true)
}

// presentedAPICredential finds a credential-shaped bearer. ambiguous is true
// when one is present but the request does not carry exactly one Authorization
// header, which no handler would read the same way twice.
func presentedAPICredential(r *http.Request) (secret string, present, ambiguous bool) {
	headers := r.Header.Values("Authorization")
	for _, header := range headers {
		token, ok := strings.CutPrefix(header, "Bearer ")
		if ok && identity.CredentialShaped(token) {
			secret, present = token, true
		}
	}
	return secret, present, present && len(headers) != 1
}

// QueryCredentialParameter is the query parameter an API key or personal
// access token may travel in on a GET or HEAD, so a tool can hand out an
// address (artwork, a stream, a download) that works on its own, as other
// media servers' tokens do.
const QueryCredentialParameter = "apiKey"

// liftQueryCredential moves a credential found in the query into the request's
// Authorization header and removes it from the address, on every method: what
// follows (scope checks, handlers that refuse unknown parameters, anything that
// records a path) sees an ordinary request with a bearer. Only a GET or HEAD
// carries it on; on any other method it is dropped, and the request is
// answered as having no credential. A value that is not shaped like a key or
// token (a session token) is dropped too: sessions never travel in addresses.
// A request that also has an Authorization header ends with two, which the
// gate refuses as ambiguous.
func liftQueryCredential(r *http.Request) *http.Request {
	if r.URL == nil || !strings.Contains(r.URL.RawQuery, QueryCredentialParameter+"=") {
		return r
	}
	// The other parameters keep their order and spelling (a signed address
	// stays signed); only the credential's own go.
	kept, secrets := []string{}, []string{}
	for _, part := range strings.Split(r.URL.RawQuery, "&") {
		name, value, _ := strings.Cut(part, "=")
		if decoded, err := url.QueryUnescape(name); err == nil && decoded == QueryCredentialParameter {
			if secret, err := url.QueryUnescape(value); err == nil {
				secrets = append(secrets, secret)
			} else {
				secrets = append(secrets, "")
			}
			continue
		}
		kept = append(kept, part)
	}
	if len(secrets) == 0 {
		return r
	}
	out := r.Clone(r.Context())
	out.URL.RawQuery = strings.Join(kept, "&")
	out.RequestURI = out.URL.RequestURI()
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && len(secrets) == 1 && identity.CredentialShaped(secrets[0]) {
		out.Header.Add("Authorization", "Bearer "+secrets[0])
	}
	return out
}

// CarriesAPICredential reports whether a request presents an API key or
// personal access token, by shape alone. It is for callers that sort requests
// before authentication (the console's connection reserve).
func CarriesAPICredential(r *http.Request) bool {
	_, present, _ := presentedAPICredential(liftQueryCredential(r))
	return present
}

func (g *credentialGate) lookup(digest string, now time.Time) (access.ResolvedCredential, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.resolved[digest]
	if !ok || !now.Before(entry.expires) || entry.authority != dbwork.AuthorityGeneration() {
		return access.ResolvedCredential{}, false
	}
	return entry.credential, true
}

// resolve bounds the work needed to identify an uncached credential before
// the request can enter its normal lane. Failed secrets share one budget per
// client and network across all routes; valid credentials do not spend it.
func (g *credentialGate) resolve(ctx context.Context, digest, address string, lookup func(context.Context) (access.ResolvedCredential, error)) (access.ResolvedCredential, error) {
	if err := ctx.Err(); err != nil {
		return access.ResolvedCredential{}, err
	}
	select {
	case g.resolving <- struct{}{}:
		defer func() { <-g.resolving }()
	default:
		return access.ResolvedCredential{}, &apierror.Error{Code: "credential_busy", RetryAfterSeconds: 1}
	}
	g.mu.Lock()
	_, known := g.resolved[digest]
	g.mu.Unlock()
	failures := &g.resolveFailures
	addressKey, networkKey := address, clientNetwork(address)
	failures.mu.Lock()
	if failures.entries == nil {
		failures.entries, failures.networks = map[string]rateEntry{}, map[string]rateEntry{}
	}
	now := time.Now()
	allowed := known || countable(failures.entries, addressKey, now, rateAttemptsPerAddress) && countable(failures.networks, networkKey, now, rateAttemptsPerNetwork)
	failures.mu.Unlock()
	if !allowed {
		return access.ResolvedCredential{}, &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 60}
	}
	bounded, cancel := context.WithTimeout(ctx, credentialResolveBudget)
	defer cancel()
	credential, err := lookup(bounded)
	if err != nil {
		failures.mu.Lock()
		charge(failures.entries, addressKey, time.Now(), rateAddressEntries)
		charge(failures.networks, networkKey, time.Now(), rateNetworkEntries)
		failures.mu.Unlock()
		g.mu.Lock()
		delete(g.resolved, digest)
		g.mu.Unlock()
	}
	return credential, err
}

// remember takes the authority generation read before the lookup that produced
// the credential, for the reason principalCache.store does.
func (g *credentialGate) remember(digest string, credential access.ResolvedCredential, now time.Time, generation uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.resolved) >= credentialResolveEntries {
		for key, entry := range g.resolved {
			if !now.Before(entry.expires) || len(g.resolved) >= credentialResolveEntries {
				delete(g.resolved, key)
			}
			if len(g.resolved) < credentialResolveEntries/2 {
				break
			}
		}
	}
	expires := now.Add(credentialResolveTTL)
	if !credential.ExpiresAt.IsZero() && credential.ExpiresAt.Before(expires) {
		expires = credential.ExpiresAt
	}
	g.resolved[digest] = resolvedEntry{credential: credential, expires: expires, authority: generation}
}

// take charges one request to the credential's bucket. It reports what is left
// and, when nothing is, how long until one request is available again.
func (g *credentialGate) take(id string, perMinute int, now time.Time) (remaining int, wait time.Duration, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	bucket := g.buckets[id]
	limit := float64(perMinute)
	if bucket == nil {
		if len(g.buckets) >= credentialBuckets {
			for key, candidate := range g.buckets {
				if now.Sub(candidate.at) > time.Minute {
					delete(g.buckets, key)
				}
			}
		}
		bucket = &credentialBucket{tokens: limit, at: now}
		g.buckets[id] = bucket
	}
	bucket.tokens += now.Sub(bucket.at).Seconds() * limit / 60
	if bucket.tokens > limit {
		bucket.tokens = limit
	}
	bucket.at = now
	if bucket.tokens < 1 {
		return 0, time.Duration((1-bucket.tokens)*60/limit*float64(time.Second)) + time.Millisecond, false
	}
	bucket.tokens--
	return int(bucket.tokens), 0, true
}

func (g *credentialGate) enter(id string, stream bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if stream {
		if g.streams[id] >= credentialStreams || g.streamed >= credentialStreamsGlobal {
			return false
		}
		g.streams[id]++
		g.streamed++
		return true
	}
	if g.inflight[id] >= credentialConcurrency || g.total >= credentialConcurrencyGlobal {
		return false
	}
	g.inflight[id]++
	g.total++
	return true
}

func (g *credentialGate) leave(id string, stream bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts, total := g.inflight, &g.total
	if stream {
		counts, total = g.streams, &g.streamed
	}
	if counts[id] <= 1 {
		delete(counts, id)
	} else {
		counts[id]--
	}
	if *total > 0 {
		*total--
	}
}

// used schedules a flush on the first use of an idle credential gate. No
// subsequent request is needed for the last-used time to reach the database.
func (g *credentialGate) used(store *access.Store, id string, now time.Time) {
	if store == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	use := g.uses[id]
	use.LastUsedMs, use.Requests = max(use.LastUsedMs, now.UnixMilli()), use.Requests+1
	g.uses[id] = use
	g.scheduleUseFlush(store)
}

// scheduleUseFlush is called with g.mu held. An idle gate has no timer.
func (g *credentialGate) scheduleUseFlush(store *access.Store) {
	if g.closed || g.useTimer != nil || len(g.uses) == 0 {
		return
	}
	g.useTimer = time.AfterFunc(credentialUseFlush, func() {
		defer supervise.Recover("httpapi.credential-use")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = g.flushUses(ctx, store)
	})
}

func (g *credentialGate) flushUses(ctx context.Context, store *access.Store) error {
	g.flushMu.Lock()
	defer g.flushMu.Unlock()
	g.mu.Lock()
	if g.useTimer != nil {
		g.useTimer.Stop()
		g.useTimer = nil
	}
	batch := g.uses
	g.uses = map[string]access.CredentialUse{}
	g.mu.Unlock()
	if len(batch) == 0 || store == nil {
		return nil
	}
	err := store.RecordCredentialUse(ctx, batch)
	g.mu.Lock()
	if err != nil {
		// Uses arriving during the write stay in the new map. Merge a failed
		// batch back into it, retaining the newest timestamp and both counts.
		for id, failed := range batch {
			current := g.uses[id]
			current.LastUsedMs = max(current.LastUsedMs, failed.LastUsedMs)
			current.Requests += failed.Requests
			g.uses[id] = current
		}
	}
	g.scheduleUseFlush(store)
	g.mu.Unlock()
	if err == nil {
		_ = store.PruneCredentials(ctx)
	}
	return err
}

// credentialUsageHandler exposes the router's pending metadata to its shutdown
// owner while keeping ordinary HTTP handling and nested MCP calls unchanged.
type credentialUsageHandler struct {
	http.Handler
	gate     *credentialGate
	store    *access.Store
	playback *playbackv1.Service
}

// FlushCredentialUse stops the router's timer and persists its pending usage.
// Call it after draining HTTP requests and before closing the database.
func FlushCredentialUse(ctx context.Context, handler http.Handler) error {
	h, ok := handler.(*credentialUsageHandler)
	if !ok || h.store == nil {
		return nil
	}
	h.gate.mu.Lock()
	h.gate.closed = true
	if h.gate.useTimer != nil {
		h.gate.useTimer.Stop()
		h.gate.useTimer = nil
	}
	h.gate.mu.Unlock()
	return h.gate.flushUses(ctx, h.store)
}

// apiRequestsPerMinute is the owner's rate for one credential.
func (d Dependencies) apiRequestsPerMinute(ctx context.Context) int {
	if setting, err := d.connectionSetting(ctx); err == nil && setting != nil && setting.apiPerMinute > 0 {
		return setting.apiPerMinute
	}
	return operations.DefaultAPIRequestsPerMinute
}

// routeScope is the scope the route serving r declares. A request no route
// serves has none; the router answers it 404 or 405 without a handler.
func (d Dependencies) routeScope(mux *http.ServeMux, r *http.Request) (apikit.Scope, bool) {
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return "", false
	}
	return d.patternScope(pattern, r.Method, r.URL.EscapedPath()), true
}

// patternScope resolves a matched mux pattern. A typed route answers from its
// own declaration; a pattern a hand-written handler shares with a typed route
// needs both to agree, and the stricter reading wins if they ever do not.
func (d Dependencies) patternScope(pattern, method, escapedPath string) apikit.Scope {
	legacy, listed := routeScopes[pattern]
	// A handler on the registry was built before the registry was finished, so
	// it reaches the finished one through the router's shared reference.
	registry := d.registry
	if registry == nil && d.self != nil {
		registry = d.self.registry
	}
	if registry != nil {
		if m, ok := registry.MetadataForPattern(pattern); ok {
			return m.Requires
		}
		if m, ok := registry.Lookup(method, escapedPath); ok && muxPattern(m.Method, m.Path) == pattern {
			if listed && legacy != m.Requires {
				return apikit.ScopeSession
			}
			return m.Requires
		}
	}
	if listed {
		return legacy
	}
	return apikit.ScopeSession
}

func credentialScopes(credential access.ResolvedCredential) []apikit.Scope {
	out := make([]apikit.Scope, 0, len(credential.Scopes))
	for _, scope := range credential.Scopes {
		out = append(out, apikit.Scope(scope))
	}
	return out
}

// scopeRefusal is the error for a credential that may not use a route.
func scopeRefusal(granted []apikit.Scope, need apikit.Scope) error {
	if need == apikit.ScopeSession || !apikit.ValidScope(need) {
		return &apierror.Error{Code: "session_required"}
	}
	if !apikit.ScopeAllows(granted, need) {
		return &apierror.Error{Code: "insufficient_scope", Reason: string(need)}
	}
	return nil
}

// registryScopeGate is installed on the typed registry. The edge has already
// made this check; it is repeated at the registry so that a typed route cannot
// be reached by a credential without it, whatever is in front.
func registryScopeGate(q *http.Request, need apikit.Scope) error {
	credential, ok := identity.APICredentialFrom(q.Context())
	if !ok {
		if _, present, _ := presentedAPICredential(q); present {
			return &apierror.Error{Code: "unauthorized"}
		}
		return nil
	}
	granted := make([]apikit.Scope, 0, len(credential.Scopes))
	for _, scope := range credential.Scopes {
		granted = append(granted, apikit.Scope(scope))
	}
	return scopeRefusal(granted, need)
}

// admitCredential applies the gate. For a request that carries no API
// credential it returns the request unchanged. Otherwise it returns the
// stamped request and a release function, or writes the refusal and reports
// false.
func (d Dependencies) admitCredential(w http.ResponseWriter, r *http.Request, mux *http.ServeMux) (*http.Request, func(), bool) {
	secret, present, ambiguous := presentedAPICredential(r)
	if !present {
		return r, nil, true
	}
	refuse := func(err error) (*http.Request, func(), bool) {
		closeUnreadUpload(w, r)
		w.Header().Set("WWW-Authenticate", `Bearer realm="Portico"`)
		policyErrors.Write(w, "", err)
		return nil, nil, false
	}
	gate, store := d.credentials, d.Access.Access
	if ambiguous || gate == nil || store == nil || len(secret) > 256 {
		return refuse(&apierror.Error{Code: "unauthorized"})
	}
	now := time.Now()
	digest := identity.Digest(secret)
	credential, cached := gate.lookup(digest, now)
	if !cached {
		generation := dbwork.AuthorityGeneration()
		resolved, err := gate.resolve(r.Context(), digest, clientAddress(r, d.trustedProxyPrefixes(r.Context())), func(ctx context.Context) (access.ResolvedCredential, error) {
			return store.ResolveCredential(ctx, digest)
		})
		if err != nil {
			if errors.Is(err, identity.ErrUnauthorized) {
				return refuse(&apierror.Error{Code: "unauthorized"})
			}
			var refusal *apierror.Error
			if errors.As(err, &refusal) {
				return refuse(refusal)
			}
			return refuse(&apierror.Error{Code: "internal", Cause: err})
		}
		credential = resolved
		gate.remember(digest, credential, now, generation)
	}
	// App passwords are accepted only by the dedicated native exchange.
	if credential.Kind == identity.CredentialKindAppPassword {
		return refuse(&apierror.Error{Code: "unauthorized"})
	}
	if credential.ParentID != "" {
		if err := d.requireIntegrations(r.Context()); err != nil {
			return refuse(&apierror.Error{Code: "unauthorized"})
		}
	}
	need, routed := d.routeScope(mux, r)
	if !routed {
		// Nothing serves this; the router's own 404 or 405 follows.
		return r, nil, true
	}
	if err := scopeRefusal(credentialScopes(credential), need); err != nil {
		w.Header().Del("WWW-Authenticate")
		closeUnreadUpload(w, r)
		policyErrors.Write(w, "", err)
		return nil, nil, false
	}
	stamped := r.WithContext(identity.WithAPICredential(r.Context(), identity.APICredential{ID: credential.ID, Kind: credential.Kind, AccountID: credential.AccountID, Scopes: credential.Scopes, Digest: digest}))
	if nested, _ := r.Context().Value(credentialNestedKey{}).(bool); nested {
		return stamped, nil, true
	}
	limit := d.apiRequestsPerMinute(r.Context())
	remaining, wait, ok := gate.take(credential.ID, limit, now)
	w.Header().Set("RateLimit-Policy", fmt.Sprintf(`"credential";q=%d;w=60`, limit))
	if !ok {
		seconds := int(wait/time.Second) + 1
		w.Header().Set("RateLimit", fmt.Sprintf(`"credential";r=0;t=%d`, seconds))
		closeUnreadUpload(w, r)
		policyErrors.Write(w, "", &apierror.Error{Code: "rate_limited", RetryAfterSeconds: seconds})
		return nil, nil, false
	}
	w.Header().Set("RateLimit", fmt.Sprintf(`"credential";r=%d`, remaining))
	lane := d.admission.laneFor(mux, r)
	if lane == laneMediaBody || lane == laneBulkTransfer {
		// Bytes are bounded by their own lanes and by the grant they carry.
		gate.used(store, credential.ID, now)
		return stamped, nil, true
	}
	stream := lane == laneRealtime
	if !gate.enter(credential.ID, stream) {
		closeUnreadUpload(w, r)
		policyErrors.Write(w, "", &apierror.Error{Code: "credential_busy", RetryAfterSeconds: 1})
		return nil, nil, false
	}
	gate.used(store, credential.ID, now)
	var once sync.Once
	return stamped, func() { once.Do(func() { gate.leave(credential.ID, stream) }) }, true
}
