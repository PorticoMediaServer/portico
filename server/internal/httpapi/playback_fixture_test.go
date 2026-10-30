package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playbackruntime"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type currentPlaybackHTTPFixture struct {
	d     Dependencies
	h     http.Handler
	token string
	t     *testing.T
}

func currentPlaybackHTTP(t *testing.T) *currentPlaybackHTTPFixture {
	t.Helper()
	return currentPlaybackHTTPWith(t, nil)
}

// currentPlaybackHTTPWith runs beforeStart (composition-root configuration such
// as ConfigureChannels) before the playback runtime starts.
func currentPlaybackHTTPWith(t *testing.T, beforeStart func(rt *playbackruntime.Runtime, db *sql.DB, state string, hostedControl *hosted.Service)) *currentPlaybackHTTPFixture {
	t.Helper()
	helper := os.Getenv("PORTICO_PLAYBACK_TEST_HELPER")
	if helper == "" {
		t.Fatal("explicit reviewed helper binary required")
	}
	state := t.TempDir()
	var canonicalErr error
	state, canonicalErr = filepath.EvalSymlinks(state)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if e := os.Chmod(state, 0700); e != nil {
		t.Fatal(e)
	}
	media := filepath.Join(state, "media")
	if e := os.Mkdir(media, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(media, "tiny.bin")
	if e := os.WriteFile(path, []byte("0123456789"), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.Open(filepath.Join(state, "current.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	id, e := identity.New(db, state)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); e != nil {
		t.Fatal(e)
	}
	session, e := id.Issue("account", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	fixtures := catalogtest.New(t, db)
	library := fixtures.Library("library", "Test", "movie", media)
	item := fixtures.Movie(library, path, "Test", 0)
	fixtures.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "bin"})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
	})
	fixtures.Drain()
	cached, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	store := storage.New(helper)
	managed, e := mounts.New(db, state, "", helper, store)
	if e != nil {
		t.Fatal(e)
	}
	store.Guard = managed.Guard
	store.MountedRoot = managed.RootFor
	rt, e := playbackruntime.New(db, id, store)
	if e != nil {
		t.Fatal(e)
	}
	if beforeStart != nil {
		beforeStart(rt, db, state, cached)
	}
	if e = rt.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		if e := rt.Shutdown(ctx); e != nil {
			t.Errorf("shutdown %v", e)
		}
		if n := store.Supervisor.Active(); n != 0 {
			t.Errorf("remaining source helpers %d", n)
		}
	})
	d := Dependencies{Origins: []string{"http://127.0.0.1:19412"}, DB: db, Identity: id, Catalog: catalog.New(db), Hosted: cached, Storage: store, Mounts: managed, PlaybackRuntime: rt}
	mux := New(d)
	return &currentPlaybackHTTPFixture{d: d, h: mux, token: session.AccessToken, t: t}
}
func (f *currentPlaybackHTTPFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var raw []byte
	if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			f.t.Fatal(e)
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
