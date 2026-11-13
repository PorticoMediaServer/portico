package prepare

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

func TestCacheRemovalAndGeneratedKeepingUseOnlyReusableCopies(t *testing.T) {
	d, film, _ := filmLibrary(t)
	clock := time.Now()
	s := service(t, d, t.TempDir(), func() time.Time { return clock })
	ctx := context.Background()
	makeCopy := func(recipe, state string, age time.Duration) db.Prepared {
		t.Helper()
		at := clock.Add(-age)
		p, err := d.WantPrepared(ctx, film.MediaID(), recipe, 3, at)
		if err != nil {
			t.Fatal(err)
		}
		if state == db.PreparedReady {
			err = d.FinishPrepared(ctx, p.ID, "mp4", 3, "hash", at)
		} else {
			err = d.SetPreparedState(ctx, p.ID, state, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Folder, p.PublicID+".mp4"), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	policy := func(word string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"keepGeneratedFiles": word})
		if _, err := s.Settings.Write(ctx, 0, settings.Storage.Name(), raw, -1, clock); err != nil {
			t.Fatal(err)
		}
		if err := s.Tidy(ctx); err != nil {
			t.Fatal(err)
		}
	}
	old := makeCopy("240p", db.PreparedReady, 60*24*time.Hour)
	recent := makeCopy("480p", db.PreparedReady, 0)
	running := makeCopy("720p-low", db.PreparedPreparing, 60*24*time.Hour)
	release, err := s.ReadCopy(ctx, recent.ID)
	if err != nil {
		t.Fatal(err)
	}
	subtitle := filepath.Join(s.Folder, old.PublicID+"-1.0.vtt")
	if err := os.WriteFile(subtitle, []byte("words"), 0600); err != nil {
		t.Fatal(err)
	}
	policy("long")
	if _, err := d.PreparedByID(ctx, old.ID); err != nil {
		t.Fatal("Longer did not retain a 60-day copy", err)
	}
	if bytes, err := s.CachedCopies(ctx, false); err != nil || bytes != 8 {
		t.Fatalf("preview: %d %v, want copy plus subtitles only", bytes, err)
	}
	if bytes, err := s.CachedCopies(ctx, true); err != nil || bytes != 8 {
		t.Fatalf("empty: %d %v", bytes, err)
	}
	if _, err := d.PreparedByID(ctx, old.ID); err != db.ErrNotFound {
		t.Fatal("the removed copy stayed listed", err)
	}
	if _, err := os.Stat(subtitle); !os.IsNotExist(err) {
		t.Fatal("the removed copy's subtitle stayed", err)
	}
	for _, p := range []db.Prepared{recent, running} {
		if _, err := os.Stat(filepath.Join(s.Folder, p.PublicID+".mp4")); err != nil {
			t.Fatal("cache removal touched recent/running output", err)
		}
	}
	release()
	if bytes, err := s.CachedCopies(ctx, false); err != nil || bytes != 3 {
		t.Fatalf("a recently made unused copy should be clearable: %d %v", bytes, err)
	}
	old = makeCopy("240p", db.PreparedReady, 60*24*time.Hour)
	policy("balanced")
	if _, err := d.PreparedByID(ctx, old.ID); err != db.ErrNotFound {
		t.Fatal("Balanced did not expire a 60-day copy", err)
	}
	old = makeCopy("240p", db.PreparedReady, 20*24*time.Hour)
	policy("balanced")
	if _, err := d.PreparedByID(ctx, old.ID); err != nil {
		t.Fatal("Balanced expired a 20-day copy", err)
	}
	policy("short")
	if _, err := d.PreparedByID(ctx, old.ID); err != db.ErrNotFound {
		t.Fatal("Shorter did not expire a 20-day copy", err)
	}
}
