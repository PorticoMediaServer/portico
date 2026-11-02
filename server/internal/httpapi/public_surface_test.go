package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// publicSurfaceAllow is the complete unauthenticated answer of one Public route
// (O-169, decision point 3): the status a caller without credentials receives
// and every JSON key path in that answer. A key path names a nested object with
// dots and an array's entries with `[]` ("error.code", "providers[].name").
// Adding, removing or renaming a key on a Public route fails this test until
// the entry here is changed in the same commit, which makes the pre-sign-in
// surface a reviewed decision instead of an accident.
type publicSurfaceAllow struct {
	status int
	keys   []string
	// binary marks a response with no JSON body (a font or a HEAD answer).
	binary bool
}

// The shared error envelopes. An unauthenticated caller may learn what it can
// do about a refusal, never what failed inside the server.
var (
	publicFailureRequestID = []string{"error", "error.code", "error.message", "error.requestId", "error.retry"}
	publicFailureRetryable = []string{"error", "error.code", "error.message", "error.retryable"}
	publicUnavailable      = []string{"error", "error.code", "error.message", "error.requestId", "error.retry", "error.retryAfterMs"}
)

// publicSurfaceRoutes is every Public route's allowed answer, keyed by
// "METHOD /path" from the registry. A registry route with no entry fails.
var publicSurfaceRoutes = map[string]publicSurfaceAllow{
	// Health and readiness: liveness and database readiness only.
	"GET /health/live":  {status: 200, keys: []string{"alive"}},
	"GET /health/ready": {status: 200, keys: []string{"ready"}},
	"GET /v1/readiness": {status: 200, keys: []string{"ready", "status"}},

	// What a client needs to choose this server, decide how to sign in and
	// check that it is new enough: identity, sign-in kind, setup state, API
	// version and majors, capabilities. No build, version, OS or paths.
	"GET /v1/server":                     {status: 200, keys: []string{"api", "api.level", "api.major", "id", "name", "setupRequired"}},
	"GET /v1/system":                     {status: 200, keys: []string{"apiLevel", "apiMajors", "apiVersion", "capabilities", "hostedAttached", "hostedConfigured", "id", "move", "moveEndsAt", "name", "setupRequired", "signIn"}},
	"GET /v1/auth/capabilities":          {status: 200, keys: []string{"hostedAttach", "methods", "quickConnect", "selfRegistration", "serverId", "serverName", "setupRequired", "twoFactor"}},
	"GET /v1/setup/state":                {status: 200, keys: []string{"status"}},
	"GET /v1/auth/services-reachability": {status: 200, keys: []string{"configured", "reachable"}},

	// This connection only: transport facts a sign-in screen shows. Home
	// networks, advertised addresses and the caller's address are owner facts.
	"GET /v1/networking/connection": {status: 200, keys: []string{"plainHttpOutsideHome", "remote", "secure", "secureConnections"}},

	// Static attribution notices and the bundled subtitle font.
	"GET /v1/attribution":        {status: 200, keys: []string{"providers", "providers[].name", "providers[].notice", "providers[].url"}},
	"GET /v1/subtitle-fonts/cjk": {status: 200, binary: true},

	// The MCP transport: a GET opens no stream.
	"GET /v1/mcp": {status: 405, keys: publicFailureRequestID},

	// The Plex-compatible public identity, with a fixed placeholder version.
	"GET /System/Info/Public":                     {status: 404, keys: publicFailureRequestID},
	"GET /system/info/public":                     {status: 404, keys: publicFailureRequestID},
	"GET /mediabrowser/System/Info/Public":        {status: 404, keys: publicFailureRequestID},
	"GET /mediabrowser/system/info/public":        {status: 404, keys: publicFailureRequestID},
	"POST /Users/AuthenticateByName":              {status: 404, keys: publicFailureRequestID},
	"POST /users/authenticatebyname":              {status: 404, keys: publicFailureRequestID},
	"POST /mediabrowser/Users/AuthenticateByName": {status: 404, keys: publicFailureRequestID},
	"POST /mediabrowser/users/authenticatebyname": {status: 404, keys: publicFailureRequestID},
	"POST /v1/integrations/authenticate":          {status: 404, keys: publicFailureRequestID},

	// Sign-in, setup, recovery, invitations and device approval. An empty or
	// invalid body gets a closed failure, never a validation of internals.
	"POST /v1/setup":                        {status: 400, keys: publicFailureRetryable},
	"POST /v1/setup/browser":                {status: 401, keys: publicFailureRequestID},
	"POST /v1/setup/start-over":             {status: 409, keys: publicFailureRequestID},
	"POST /v1/setup/remote-check":           {status: 409, keys: publicFailureRequestID},
	"POST /v1/setup/app":                    {status: 409, keys: publicFailureRequestID},
	"POST /v1/setup/resume":                 {status: 401, keys: publicFailureRetryable},
	"POST /v1/setup/abandon":                {status: 401, keys: publicFailureRetryable},
	"POST /v1/setup/approval":               {status: 401, keys: publicFailureRetryable},
	"POST /v1/setup/retry":                  {status: 401, keys: publicFailureRetryable},
	"POST /v1/setup/account/complete":       {status: 401, keys: publicFailureRetryable},
	"POST /v1/direct/sign-in":               {status: 401, keys: publicFailureRetryable},
	"POST /v1/direct/portico-challenge":     {status: 403, keys: publicFailureRetryable},
	"POST /v1/direct/password/reset":        {status: 401, keys: publicFailureRetryable},
	"POST /v1/auth/refresh":                 {status: 401, keys: publicFailureRetryable},
	"POST /v1/auth/register":                {status: 403, keys: publicFailureRetryable},
	"POST /v1/auth/two-factor/challenge":    {status: 401, keys: publicFailureRetryable},
	"POST /v1/auth/remembered-accounts":     {status: 400, keys: publicFailureRequestID},
	"POST /v1/auth/portico-return/prepare":  {status: 403, keys: publicFailureRetryable},
	"POST /v1/auth/portico-return/exchange": {status: 400, keys: publicFailureRetryable},
	"POST /v1/auth/move/code":               {status: 410, keys: publicFailureRequestID},
	"POST /v1/auth/move/local":              {status: 400, keys: publicFailureRetryable},
	"POST /v1/auth/move/password":           {status: 403, keys: publicFailureRetryable},
	"POST /v1/auth/move/portico":            {status: 403, keys: publicFailureRetryable},
	"POST /v1/access/invitations/accept":    {status: 404, keys: publicFailureRetryable},
	"POST /v1/access/invitations/preview":   {status: 400, keys: publicFailureRequestID},
	"POST /v1/quick-connect":                {status: 400, keys: publicFailureRetryable},
	"POST /v1/quick-connect/token":          {status: 400, keys: publicFailureRetryable},
	"POST /v1/quick-connect/cancel":         {status: 400, keys: publicFailureRetryable},
	"POST /v1/hosted/wake":                  {status: 401, keys: publicFailureRetryable},
	"POST /v1/networking/identity-proof":    {status: 401, keys: publicFailureRetryable},
	"POST /v1/networking/endpoint-proof":    {status: 404, keys: publicFailureRetryable},
	"POST /v1/mcp":                          {status: 401, keys: publicFailureRetryable},
	"POST /v1/cast/preview":                 {status: 400, keys: []string{"error", "error.code", "error.message", "error.retryable", "protocolVersion"}},
	"POST /v1/cast/reconnect":               {status: 400, keys: []string{"error", "error.code", "error.message", "error.retryable", "protocolVersion"}},
	"POST /v1/cast/redeem":                  {status: 400, keys: []string{"error", "error.code", "error.message", "error.retryable", "protocolVersion"}},

	// A reset file's fixed name and its reissue wait; never the operating
	// system or the data and key directories it was written to.
	"POST /v1/auth/owner-reset/request": {status: 200, keys: []string{"fileName", "retryAfterSeconds"}},
	"POST /v1/auth/owner-reset/verify":  {status: 401, keys: publicFailureRetryable},

	// Download and subtitle routes use the grant in the path as their
	// credential; an unknown, spent or unconfigured grant is a closed failure.
	"GET /v1/downloads/artifacts/{grant}":                                        {status: 503, keys: publicUnavailable},
	"GET /v1/downloads/artifacts/{grant}/artwork/{role}":                         {status: 503, keys: publicUnavailable},
	"GET /v1/downloads/artifacts/{grant}/chapters/{source}/{index}/image":        {status: 503, keys: publicUnavailable},
	"GET /v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}.vtt":    {status: 503, keys: publicUnavailable},
	"GET /v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}/{file}": {status: 503, keys: publicUnavailable},
	"GET /v1/downloads/artifacts/{grant}/trickplay/{set}/thumbnails.vtt":         {status: 503, keys: publicUnavailable},
	"GET /v1/downloads/artifacts/{grant}/trickplay/{set}/tiles/{tile}":           {status: 404, keys: publicFailureRequestID},
	"HEAD /v1/downloads/artifacts/{grant}":                                       {status: 503, binary: true},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}":                      {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}.vtt":                  {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts":                {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts/{font}":         {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/lines":                {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.ass":         {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.idx":         {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.mks":         {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.ssa":         {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.sub":         {status: 503, keys: publicUnavailable},
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.sup":         {status: 503, keys: publicUnavailable},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/fonts/{font}":        {status: 503, binary: true},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.ass":        {status: 503, binary: true},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.idx":        {status: 503, binary: true},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.mks":        {status: 503, binary: true},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.ssa":        {status: 503, binary: true},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.sub":        {status: 503, binary: true},
	"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.sup":        {status: 503, binary: true},

	// Development-only routes (devtrust builds): with no seeded e2e owner they
	// are the closed not-found answer. They never exist in a release build.
	"GET /v1/dev/e2e":          {status: 404, keys: publicFailureRetryable},
	"POST /v1/dev/e2e/sign-in": {status: 404, keys: publicFailureRetryable},
}

var publicSurfacePathParameter = regexp.MustCompile(`\{[A-Za-z]+\}`)

// TestPublicSurfaceBeforeSignIn calls every Public route through the real
// router without a credential and compares its exact answer with the reviewed
// allowlist above. A new Public route, a new key on an old one, or any build,
// platform or host fact in a body fails here.
func TestPublicSurfaceBeforeSignIn(t *testing.T) {
	d, root := publicSurfaceFixture(t)
	server := httptest.NewServer(New(d))
	defer server.Close()
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, definition := range registry.Definitions() {
		if definition.Access == "public" {
			patterns = append(patterns, definition.Method+" "+definition.Path)
		}
	}
	if len(patterns) < 40 {
		t.Fatalf("only %d Public routes were enumerated: the registry, not a list here, is the source", len(patterns))
	}
	sort.Strings(patterns)
	banned := publicSurfaceBannedValues(t, root)
	client := server.Client()
	for _, pattern := range patterns {
		t.Run(strings.NewReplacer(" ", "_", "/", "_").Replace(pattern), func(t *testing.T) {
			allow, ok := publicSurfaceRoutes[pattern]
			if !ok {
				t.Fatalf("%s is Public but has no entry in publicSurfaceRoutes: document what it answers before sign-in, or do not expose it", pattern)
			}
			method, path, _ := strings.Cut(pattern, " ")
			// Every path parameter is one opaque unknown value: a grant, a code
			// or an id a caller does not hold. No route may tell them apart.
			path = publicSurfacePathParameter.ReplaceAllString(path, "unknown")
			var body io.Reader
			if method != "GET" && method != "HEAD" {
				body = strings.NewReader(`{}`)
			}
			request, err := http.NewRequest(method, server.URL+path, body)
			if err != nil {
				t.Fatal(err)
			}
			if body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, value := range banned {
				if strings.Contains(string(raw), value) {
					t.Errorf("%s disclosed %q: %s", pattern, value, raw)
				}
			}
			if response.StatusCode != allow.status {
				t.Fatalf("%s answered %d, the allowlist says %d: %s", pattern, response.StatusCode, allow.status, raw)
			}
			if allow.binary {
				return
			}
			var document any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatalf("%s did not answer JSON: %v: %s", pattern, err, raw)
			}
			paths := map[string]bool{}
			publicSurfaceKeyPaths("", document, paths)
			got := make([]string, 0, len(paths))
			for path := range paths {
				got = append(got, path)
			}
			want := append([]string{}, allow.keys...)
			sort.Strings(got)
			sort.Strings(want)
			if !slices.Equal(got, want) {
				t.Fatalf("%s answered keys %v, the allowlist says %v: %s", pattern, got, want, raw)
			}
		})
	}
}

// publicSurfaceKeyPaths records every object key in a decoded document, with
// array entries marked `[]`, so a nested object cannot gain a field unseen.
func publicSurfaceKeyPaths(prefix string, value any, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			out[path] = true
			publicSurfaceKeyPaths(path, child, out)
		}
	case []any:
		for _, child := range typed {
			publicSurfaceKeyPaths(prefix+"[]", child, out)
		}
	}
}

// publicSurfaceBannedValues is every exact build, platform and location fact
// that must never appear in a pre-sign-in answer. Placeholders a development
// build writes are left out: they name no release and identify nothing.
func publicSurfaceBannedValues(t *testing.T, root string) []string {
	t.Helper()
	values := []string{runtime.Version(), runtime.GOOS, runtime.GOARCH}
	for _, value := range []string{buildinfo.Version, buildinfo.BuildID, buildinfo.SourceDigest, buildinfo.BuiltAt} {
		if value != "" && value != "development" && value != "dev" && value != "unsealed" {
			values = append(values, value)
		}
	}
	if host, err := os.Hostname(); err == nil && len(host) >= 4 {
		values = append(values, host)
	}
	if root != "" {
		values = append(values, root)
	}
	if cwd, err := os.Getwd(); err == nil && len(cwd) > 1 {
		values = append(values, cwd)
	}
	return values
}

// publicSurfaceFixture is the smallest server a Public answer needs: a real
// database, the identity that owns it and the Portico Account control. The
// data directory is returned so the test can grep for it.
func publicSurfaceFixture(t *testing.T) (Dependencies, string) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	control, err := hosted.New(db, ident, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return Dependencies{DB: db, Identity: ident, Hosted: control, Catalog: catalog.New(db), Origins: []string{"http://127.0.0.1:11111"}}, root
}
