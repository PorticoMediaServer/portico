package api

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
	"golang.org/x/crypto/bcrypt"
)

// lockedBuffer is a log the server writes to from many goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) contains(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.buf.Bytes(), []byte(s))
}

// openLoggedServer is openTestServer with its log kept, for a test of what the log says.
func openLoggedServer(t *testing.T, path string, log *lockedBuffer) *Server {
	t.Helper()
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(d) })
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(ctx, d, filepath.Dir(path), "test", slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stopWhenDone(t, s, cancel)
	s.passwords = auth.NewPasswords(bcrypt.MinCost, bcrypt.MinCost)
	return s
}

// asksTMDBWith asks the server's TMDB about one film and says which token the request was
// signed with.
func asksTMDBWith(t *testing.T, s *Server, stand *tmdbtest.Server) string {
	t.Helper()
	s.UseTMDBHost(stand.URL)
	stand.Token.Store(nil)
	s.metadata.Providers["tmdb"].Search(context.Background(), metadata.Query{Kind: "movie", Title: "Heat", Year: 1995})
	if token := stand.Token.Load(); token != nil {
		return *token
	}
	return ""
}

// TMDB works with Portico's built-in key, and the owner's own key in Settings › Metadata takes
// precedence over it. The owner's key is stored sealed: no route answers it, the database files
// do not hold it as typed, it is in use again after a restart, and one the server can no longer
// open is not used (Portico's key is) and is said in the log. Neither key is ever in the log or
// in any settings answer. Administrators cannot open the page, and a page saved since it was
// read is refused.
func TestProviderKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	var log lockedBuffer
	s := openLoggedServer(t, path, &log)
	stand := tmdbtest.New(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	addMember(t, s, owner, "ada", "admin")
	const ownKey = "eyJhbGciOiJIUzI1NiJ9.the-owner's-own-read-token"
	secrets := []string{ownKey, tmdb.ProjectToken}
	holdsAKey := func(where string, raw []byte) {
		t.Helper()
		for _, secret := range secrets {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("%s holds a TMDB key", where)
			}
		}
	}
	page := func(token string) MetadataSettings {
		t.Helper()
		var p MetadataSettings
		w := call(t, s, "GET", "/v1/admin/metadata/settings", token, nil, &p)
		if w.Code != 200 {
			t.Fatalf("the settings page answered %d: %s", w.Code, w.Body)
		}
		holdsAKey("the settings page", w.Body.Bytes())
		return p
	}

	// A new server: Portico's key, nothing set.
	if p := page(owner); p.Revision != 1 || p.TMDBCredentialConfigured || p.IncludeAdult {
		t.Fatalf("a new server's page: %+v", p)
	}
	if got := asksTMDBWith(t, s, stand); got != tmdb.ProjectToken {
		t.Fatalf("with no key of the owner's, TMDB was asked with %q; want Portico's key", got)
	}

	// The owner's own key takes precedence.
	key, adult, rev := ownKey, true, int64(1)
	w := call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{ExpectedRevision: &rev, TMDBReadToken: &key, IncludeAdult: &adult}, nil)
	if w.Code != 200 {
		t.Fatalf("saving the owner's key: %d %s", w.Code, w.Body)
	}
	holdsAKey("the answer to saving the key", w.Body.Bytes())
	if p := page(owner); p.Revision != 2 || !p.TMDBCredentialConfigured || !p.IncludeAdult {
		t.Fatalf("after saving the key: %+v", p)
	}
	if got := asksTMDBWith(t, s, stand); got != ownKey {
		t.Fatalf("with the owner's key saved, TMDB was asked with %q; want the owner's", got)
	}
	// A page read before that save is out of date.
	if w := call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{ExpectedRevision: &rev, IncludeAdult: &adult}, nil); w.Code != 412 {
		t.Fatalf("a save from a page out of date answered %d, want 412", w.Code)
	}
	long := string(bytes.Repeat([]byte("k"), 5000))
	if w := call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{TMDBReadToken: &long}, nil); w.Code != 400 {
		t.Fatalf("a key longer than any real one answered %d, want 400", w.Code)
	}
	if w := call(t, s, "GET", "/v1/admin/metadata/settings", signInAs(t, s, "ada", testPassword, 5).AccessToken, nil, nil); w.Code != 403 {
		t.Fatalf("an administrator opened the provider keys page: %d", w.Code)
	}
	var all Page[SettingsDocument]
	holdsAKey("the settings documents", call(t, s, "GET", "/v1/admin/settings", owner, nil, &all).Body.Bytes())
	for _, file := range []string{path, path + "-wal"} {
		raw, _ := os.ReadFile(file)
		holdsAKey(filepath.Base(file), raw)
	}

	// After a restart the owner's key is opened and in use again.
	stopServer(s)
	closeDB(s.db)
	s = openLoggedServer(t, path, &log)
	owner = onMain(t, s, signIn(t, s, 1)).AccessToken
	if p := page(owner); !p.TMDBCredentialConfigured || !p.IncludeAdult {
		t.Fatalf("after a restart: %+v", p)
	}
	if got := asksTMDBWith(t, s, stand); got != ownKey {
		t.Fatalf("after a restart TMDB was asked with %q; want the owner's key", got)
	}

	// Removed, Portico's key is used again.
	empty := ""
	if w := call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{TMDBReadToken: &empty}, nil); w.Code != 200 {
		t.Fatalf("removing the key: %d %s", w.Code, w.Body)
	}
	if p := page(owner); p.TMDBCredentialConfigured {
		t.Fatalf("after removing the key: %+v", p)
	}
	if got := asksTMDBWith(t, s, stand); got != tmdb.ProjectToken {
		t.Fatalf("after removing the owner's key TMDB was asked with %q; want Portico's", got)
	}

	// A key the server can no longer open (its key file lost) is not used, and the log says so.
	call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{TMDBReadToken: &key}, nil)
	stopServer(s)
	closeDB(s.db)
	if err := os.Remove(filepath.Join(filepath.Dir(path), "secrets.key")); err != nil {
		t.Fatal(err)
	}
	s = openLoggedServer(t, path, &log)
	owner = onMain(t, s, signIn(t, s, 2)).AccessToken
	if p := page(owner); p.TMDBCredentialConfigured || !p.TMDBCredentialUnreadable {
		t.Fatalf("with the key file gone, the page does not say the owner's key must be entered again: %+v", p)
	}
	if got := asksTMDBWith(t, s, stand); got != tmdb.ProjectToken {
		t.Fatalf("with the key file gone TMDB was asked with %q; want Portico's", got)
	}
	if !log.contains("cannot be opened") {
		t.Fatal("the log does not say the owner's key must be entered again")
	}
	// Entered again, it is in use and the page says so.
	if w := call(t, s, "PUT", "/v1/admin/metadata/settings", owner, MetadataSettingsChange{TMDBReadToken: &key}, nil); w.Code != 200 {
		t.Fatalf("entering the key again: %d %s", w.Code, w.Body)
	}
	if p := page(owner); !p.TMDBCredentialConfigured || p.TMDBCredentialUnreadable {
		t.Fatalf("after entering the key again: %+v", p)
	}
	for _, secret := range secrets {
		if log.contains(secret) {
			t.Fatal("the log holds a TMDB key")
		}
	}
}
