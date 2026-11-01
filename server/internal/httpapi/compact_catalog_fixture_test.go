package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackruntime"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// settleCompactCatalogue drains queued derived data for tests that wrote
// catalogue facts through the compact-catalogue API.
func settleCompactCatalogue(t *testing.T, db *sql.DB) {
	t.Helper()
	catalogtest.New(t, db).Drain()
}

func tl6FixtureMovie(t testing.TB, d Dependencies, library, path, title string, year int) catalogtest.Item {
	t.Helper()
	c := catalogtest.New(t, d.DB)
	var root string
	if err := d.DB.QueryRow(`SELECT root FROM catalog_libraries WHERE library_id=?`, library).Scan(&root); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(root, path)
	handle := c.Handle(library)
	item := c.Entity(compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: title, Year: year, Added: "2026-01-01T00:00:00.000Z"}, nil)
	item.Asset, item.Token = c.File(item.ID, path, 5400)
	c.Drain()
	return item
}

func tl6AssetID(t testing.TB, db *sql.DB, item string) int64 {
	t.Helper()
	c := catalogtest.New(t, db)
	var asset int64
	if err := db.QueryRow(`SELECT asset_id FROM catalog_asset_links WHERE entity_id=? ORDER BY part_index LIMIT 1`, c.ID(item)).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	return asset
}

func tl6BulkMovieTx(ctx context.Context, tx *sql.Tx, libraryID, key, title string, year int, overview string) error {
	var library int64
	var root string
	if err := tx.QueryRowContext(ctx, `SELECT id,root FROM catalog_libraries WHERE library_id=?`, libraryID).Scan(&library, &root); err != nil {
		return err
	}
	path := filepath.Join(root, key+".mkv")
	id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: title, Year: year, Added: "2026-01-01T00:00:00.000Z"})
	if err != nil || overview == "" {
		return err
	}
	return compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"overview": overview})
}

// tl6V1Fixture is the small playback server fixture copied for TL6 tests that
// run with unrelated test files stubbed out. Its catalogue rows use catalogtest.
type tl6V1Fixture struct {
	t       *testing.T
	db      *sql.DB
	id      *identity.Service
	cat     *catalog.Service
	handler http.Handler
	owner   identity.Envelope
	library string
	items   []string
	root    string
	v1      *playbackv1.Service
	deps    Dependencies
}

func newTL6V1Fixture(t *testing.T, films int) *tl6V1Fixture {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	cat := catalog.New(db)
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f := &tl6V1Fixture{t: t, db: db, id: id, cat: cat, root: root, v1: &playbackv1.Service{}}
	f.deps = Dependencies{DB: db, Identity: id, Catalog: cat, Playback: playback.New(db), Hosted: host, PlaybackV1: f.v1}
	f.handler = New(f.deps)
	f.deps.v1 = f.v1
	secret, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	w := f.raw("POST", "/v1/setup", "", nil, map[string]string{"authMode": "local", "setupToken": string(secret), "username": "owner", "password": "long-test-password", "name": "Test"})
	if w.Code != 201 {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &f.owner); err != nil {
		t.Fatal(err)
	}
	lib, err := cat.Create("Movies", "movie", root)
	if err != nil {
		t.Fatal(err)
	}
	f.library = lib.ID
	c := catalogtest.New(t, db)
	handle := c.Handle(lib.ID)
	for i := range films {
		path := filepath.Join(root, fmt.Sprintf("Film %02d.mp4", i))
		if err = os.WriteFile(path, bytes.Repeat([]byte("0"), 1000), 0600); err != nil {
			t.Fatal(err)
		}
		item := c.Movie(handle, path, fmt.Sprintf("Film %02d", i), 2000+i)
		f.items = append(f.items, item.Public)
	}
	c.Drain()
	return f
}

func tl6SubtitleFixture(t *testing.T) (*tl6V1Fixture, *subtitles.Service, identity.Principal, string) {
	t.Helper()
	f := newTL6V1Fixture(t, 1)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := subtitles.New(subtitles.Options{
		DB: f.db, Directory: filepath.Join(root, "subtitles"), Storage: storage.New(binary), HelperBinary: binary,
		Authorize: func(_ context.Context, _ *sql.Tx, principal identity.Principal, _ string) (identity.Principal, error) {
			return principal, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	player := playback.New(f.db)
	player.ConfigureSubtitleDelivery(subs)
	f.v1.Subtitles = subs
	f.deps = Dependencies{DB: f.db, Identity: f.id, Catalog: f.cat, Playback: player, Subtitles: subs, PlaybackV1: f.v1}
	f.handler = New(f.deps)
	principal, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	if err := f.db.QueryRow(`SELECT a.token FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?)`, f.items[0]).Scan(&source); err != nil {
		t.Fatal(err)
	}
	return f, subs, principal, source
}

func (f *tl6V1Fixture) device(installation string) identity.Envelope {
	f.t.Helper()
	return f.deviceFor(f.owner.Viewer.AccountID, f.owner.Viewer.ProfileID, "owner", installation)
}

func (f *tl6V1Fixture) deviceFor(account, profile, role, installation string) identity.Envelope {
	f.t.Helper()
	id := (installation + strings.Repeat("0", 40))[:40]
	ctx, err := identity.WithIssuingDevice(context.Background(), identity.DeviceRegistration{InstallationID: id, Name: installation, Platform: "test", App: "test", AppVersion: "1"}, "127.0.0.1")
	if err != nil {
		f.t.Fatal(err)
	}
	gated, err := dbwork.Begin(ctx, f.db, dbwork.ClassSecurityFence)
	if err != nil {
		f.t.Fatal(err)
	}
	defer gated.Rollback()
	env, err := f.id.IssueTx(ctx, gated.Tx(), account, profile, "local", role, 1, time.Time{})
	if err != nil {
		f.t.Fatal(err)
	}
	if err = gated.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return env
}

func (f *tl6V1Fixture) member() (string, string) {
	f.t.Helper()
	folder := filepath.Join(f.root, "kids")
	if err := os.MkdirAll(folder, 0700); err != nil {
		f.t.Fatal(err)
	}
	kids, err := f.cat.Create("Kids", "movie", folder)
	if err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(folder, "Kids Film.mp4")
	if err = os.WriteFile(path, bytes.Repeat([]byte("0"), 1000), 0600); err != nil {
		f.t.Fatal(err)
	}
	c := catalogtest.New(f.t, f.db)
	item := c.Movie(c.Handle(kids.ID), path, "Kids Film", 2000)
	c.Drain()
	if _, err = f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',X'00','member-profile',1)`); err != nil {
		f.t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='member'`, `["`+kids.ID+`"]`); err != nil {
		f.t.Fatal(err)
	}
	env, err := f.id.Issue("member", "member-profile", "local", "member", 1)
	if err != nil {
		f.t.Fatal(err)
	}
	return env.AccessToken, item.Public
}

func (f *tl6V1Fixture) raw(method, path, token string, headers map[string]string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var data []byte
	if body != nil {
		if s, ok := body.(string); ok {
			data = []byte(s)
		} else {
			data, _ = json.Marshal(body)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		contentType := "application/json"
		if method == "PATCH" {
			contentType = "application/merge-patch+json"
		}
		r.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func (f *tl6V1Fixture) call(method, path string, headers map[string]string, body any, want int, out any) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.callAs(f.owner.AccessToken, method, path, headers, body, want, out)
}

func (f *tl6V1Fixture) callAs(token, method, path string, headers map[string]string, body any, want int, out any) *httptest.ResponseRecorder {
	f.t.Helper()
	w := f.raw(method, path, token, headers, body)
	if want != 0 && w.Code != want {
		f.t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	if out != nil && w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			f.t.Fatalf("%s %s: %v: %s", method, path, err, w.Body.String())
		}
	}
	return w
}

func tl6StartBody(item string, extra map[string]any) map[string]any {
	body := map[string]any{"itemId": item}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func tl6V1Code(w *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func tl6WebCapabilities() map[string]any { return webCapabilities() }

func tl6QueueItems(ids ...string) map[string]any {
	return map[string]any{"items": map[string]any{"ids": ids}}
}

func tl6Itoa(value int64) string { return strconv.FormatInt(value, 10) }

const tl6RaceDetector = false

type tl6CurrentPlaybackHTTPFixture struct {
	d     Dependencies
	h     http.Handler
	token string
	t     *testing.T
}

func tl6CurrentPlaybackHTTP(t *testing.T) *tl6CurrentPlaybackHTTPFixture {
	t.Helper()
	helper := os.Getenv("PORTICO_PLAYBACK_TEST_HELPER")
	if helper == "" {
		t.Fatal("explicit reviewed helper binary required")
	}
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(state, "media")
	if err = os.Mkdir(media, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(media, "tiny.bin")
	if err = os.WriteFile(path, bytes.Repeat([]byte("0"), 1000), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(state, "current.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	session, err := id.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	library := c.Library("library", "Test", "movie", media)
	c.Movie(library, path, "Test", 2000)
	c.Drain()
	cached, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(helper)
	managed, err := mounts.New(db, state, "", helper, store)
	if err != nil {
		t.Fatal(err)
	}
	store.Guard = managed.Guard
	store.MountedRoot = managed.RootFor
	rt, err := playbackruntime.New(db, id, store)
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rt.Shutdown(ctx); err != nil {
			t.Errorf("shutdown %v", err)
		}
		if n := store.Supervisor.Active(); n != 0 {
			t.Errorf("remaining source helpers %d", n)
		}
	})
	d := Dependencies{Origins: []string{"http://127.0.0.1:19412"}, DB: db, Identity: id, Catalog: catalog.New(db), Hosted: cached, Storage: store, Mounts: managed, PlaybackRuntime: rt}
	return &tl6CurrentPlaybackHTTPFixture{d: d, h: New(d), token: session.AccessToken, t: t}
}

func (f *tl6CurrentPlaybackHTTPFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Header().Get("Cache-Control") != "no-store" {
		f.t.Fatal("private response cacheable")
	}
	return w
}

func tl6SupportFixture(t *testing.T) (Dependencies, identity.Envelope, identity.Envelope) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','PRIVATE-OWNER',x'00','profile',1),('member','PRIVATE-MEMBER',x'00','member-profile',1)`); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	c.Library("private-library", "PRIVATE-TITLE", "movie", "/private/SOURCE-PATH")
	c.Drain()
	if _, err = db.Exec(`INSERT INTO jobs(id,library_id,status,error,created_at) VALUES('private-job','private-library','failed','TOKEN-secret','2026-09-05T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	owner, err := id.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	member, err := id.Issue("member", "member-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	control, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: control}, owner, member
}

func tl6NotificationFixture(t *testing.T) (Dependencies, *http.ServeMux, identity.Envelope, identity.Envelope) {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1),('member','member',x'00','member-profile',1)`); err != nil {
		t.Fatal(err)
	}
	owner, err := id.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	member, err := id.Issue("member", "member-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Console: operations.New(db)}
	mux := http.NewServeMux()
	d.notificationRoutes(mux)
	return d, mux, owner, member
}

func tl6NotificationRequest(h http.Handler, method, token, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func tl6ExtraItem(t *testing.T, db *sql.DB) catalogtest.Item {
	t.Helper()
	c := catalogtest.New(t, db)
	path := "/movies/extra.mp4"
	library := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Extra, Key: compactcatalog.ExtraKey("/movies", path), Title: "Extra"}, nil)
	c.File(item.ID, path, 60)
	c.Drain()
	return item
}

func tl6RefusedStatus(token string) int {
	if token == "" {
		return http.StatusUnauthorized
	}
	return http.StatusForbidden
}

func tl6AssertSpecResponse(t *testing.T, method, path string, w *httptest.ResponseRecorder) {
	t.Helper()
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); method != "HEAD" && err != nil {
		t.Fatalf("%s %s %d: %v", method, path, w.Code, err)
	}
	doc, schema, err := apispec.Response(method, path, w.Code)
	if err != nil {
		t.Fatal(err)
	}
	if problems := doc.ValidateJSON(schema, w.Body.Bytes()); len(problems) > 0 {
		t.Fatalf("%s %s %d does not match %s: %v\n%s", method, path, w.Code, doc.File, problems, w.Body.String())
	}
}

var tl6HostedRootPublic, tl6HostedRootPrivate, tl6HostedRootError = ed25519.GenerateKey(rand.Reader)

func tl6HostedRootPin() string {
	if tl6HostedRootError != nil {
		panic(tl6HostedRootError)
	}
	return base64.RawURLEncoding.EncodeToString(tl6HostedRootPublic)
}

func tl6HostedRootID() string { return hostedtrust.KeyID(tl6HostedRootPublic) }

var tl6ZeroHostedRootID = hostedtrust.KeyID(make([]byte, 32))

func tl6CertifiedHostedPolicy(t *testing.T, leaf ed25519.PrivateKey, id string, payload []byte) hosted.Signed {
	t.Helper()
	if tl6HostedRootError != nil {
		t.Fatal(tl6HostedRootError)
	}
	now := time.Now().UTC()
	cert, err := hostedtrust.Certify(tl6HostedRootPrivate, leaf.Public().(ed25519.PublicKey), id, "documents", now.Add(-time.Minute), now.Add(24*time.Hour), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := hostedtrust.Sign(leaf, cert, payload)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func tl6IssueHostedFixture(t *testing.T, db *sql.DB, ident *identity.Service, account, profile, role string) (identity.Envelope, error) {
	t.Helper()
	return issuePorticoFixture(t, db, ident, account, profile, role)
}
