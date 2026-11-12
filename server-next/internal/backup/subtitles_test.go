package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Downloaded subtitle text must travel with the database that names it, even when the
// owner leaves Include artwork off. A restore on another machine has the original text.
func TestBackupRestoresDownloadedSubtitles(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if _, err := seed.Import(ctx, s.DB, seed.Config{Seed: 7, Items: 7}, nil); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, s.DB, 1)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := s.DB.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.DB.Items(ctx, viewers[0], db.ItemQuery{Library: libraries[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 1})
	if err != nil || len(items.Items) != 1 {
		t.Fatalf("film: %+v %v", items, err)
	}
	film, err := s.DB.SubtitleTarget(ctx, viewers[0], items.Items[0].PublicID, "")
	if err != nil {
		t.Fatal(err)
	}
	text := []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n")
	sum := sha256.Sum256(text)
	hash := hex.EncodeToString(sum[:])
	if _, err := s.DB.AddSubtitle(ctx, db.Subtitle{MediaID: film.MediaID, Provider: "test", ProviderRef: "one", FileHash: hash, Format: "srt", Language: "eng"}); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join("subtitles", hash[:2], hash)
	if err := os.MkdirAll(filepath.Join(s.DataDir, "subtitles", hash[:2]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.DataDir, name), text, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.make(ctx, "portico-20261112-030000", false); err != nil {
		t.Fatal(err)
	}
	archive, err := s.Path("portico-20261112-030000")
	if err != nil {
		t.Fatal(err)
	}
	restored := t.TempDir()
	if _, err := Inspect(ctx, archive, restored); err != nil {
		t.Fatal(err)
	}
	if err := Stage(ctx, archive, restored, "subtitles", time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Prepare(restored, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := read(restored, name); got != string(text) {
		t.Fatalf("restored subtitle %q", got)
	}
	for _, unsafe := range []string{"subtitles/../main.db", "subtitles/ab/../../../secrets.key", "subtitles/zz/" + hash, "subtitles/" + hash[:2] + "/" + hash + ".tmp"} {
		if entryKind(unsafe) != "" {
			t.Errorf("accepted unsafe subtitle name %q", unsafe)
		}
	}
	// One subtitle lost on disk must not stop every later backup of the server.
	if err := os.Remove(filepath.Join(s.DataDir, name)); err != nil {
		t.Fatal(err)
	}
	if err := s.make(ctx, "portico-20261112-040000", false); err != nil {
		t.Fatalf("a missing subtitle stopped the backup: %v", err)
	}
	missingArchive, err := s.Path("portico-20261112-040000")
	if err != nil {
		t.Fatal(err)
	}
	withoutSubtitle := t.TempDir()
	if _, err := Inspect(ctx, missingArchive, withoutSubtitle); err != nil {
		t.Fatalf("backup without the lost subtitle cannot be inspected: %v", err)
	}
	if err := Stage(ctx, missingArchive, withoutSubtitle, "missing subtitle", time.Now(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(withoutSubtitle, pendingDir, name)); !os.IsNotExist(err) {
		t.Fatalf("a missing subtitle was not skipped: %v", err)
	}
}
