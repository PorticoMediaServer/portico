package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
)

// The same root used by New reaches actual scanner warnings, network callbacks and
// playback progress errors. No line in this test is given a log_kind attribute by hand.
func TestEveryMessagesKindHasARealSource(t *testing.T) {
	log, err := serverlog.Open(filepath.Join(t.TempDir(), "logs"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	data := t.TempDir()
	if err := os.Chmod(data, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(t.Context(), filepath.Join(data, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(d) })
	ctx, cancel := context.WithCancel(t.Context())
	s, err := New(ctx, d, data, "test", slog.New(log.Handler()))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stopWhenDone(t, s, cancel)
	s.passwords = auth.NewPasswords(bcrypt.MinCost, bcrypt.MinCost)
	s.UseTMDBHost("http://127.0.0.1:1")
	s.UseLog(log)
	token := onMainProfile(t, s).AccessToken
	claims, err := s.tokens.CheckAccess(token, s.now())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := seed.Import(ctx, d, seed.Config{Seed: 3, Items: 20}, nil); err != nil {
		t.Fatal(err)
	}
	s.log.Info("server logger ready")
	s.public.OnChange("203.0.113.7")
	s.porticoProblem(errors.New("stand-in service unavailable"))
	folder := t.TempDir()
	library, err := d.CreateLibrary(ctx, "Unavailable folder", db.LibraryMovies, "", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scans.Scan(ctx, library.PublicID, claims.UserID, true); err != nil {
		t.Fatal(err)
	}
	s.jobs.Wait(ctx)

	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var movies string
	for _, lib := range libraries {
		if lib.Kind == db.LibraryMovies && lib.ID != library.ID {
			movies = lib.PublicID
			break
		}
	}
	items, err := d.Items(ctx, db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}, db.ItemQuery{Library: movies, Kinds: []db.ItemKind{db.KindMovie}, Limit: 1})
	if err != nil || len(items.Items) != 1 {
		t.Fatalf("movie fixture: %+v %v", items, err)
	}
	engine := &fakeEngine{}
	s.playback.SetEngine(engine, engine)
	var session PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", token, PlaybackStart{ItemID: items.Items[0].PublicID}, &session); w.Code != 201 {
		t.Fatalf("playback: %d %s", w.Code, w.Body)
	}
	running := s.playback.All()[0]
	s.jobs.Wait(ctx)
	// A failed save exercises the service's actual logger, without a real media process.
	closeDB(d)
	if _, err := s.playback.Report(ctx, running.ID, running.Viewer(), running.SignInID, playback.Report{State: playback.StatePaused, PositionMs: 1000, PlayedMs: 1000}); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"server logger ready":                  "server",
		"library folder not scanned":           "scan",
		"this server's public address changed": "network",
		"a call to Portico Services failed":    "network",
		"saving playback progress failed":      "playback",
	}
	entries, _ := log.Since(0)
	for _, entry := range entries {
		if kind, exists := expected[entry.Message]; exists {
			if entry.Kind != kind {
				t.Errorf("%q has kind %q, want %q", entry.Message, entry.Kind, kind)
			}
			delete(expected, entry.Message)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("natural log sources did not run: %v", expected)
	}
}
