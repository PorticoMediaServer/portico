package prepare

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Empty cache removes only copies no request names, never one being read or made. A copy no
// download waits for goes at the next tidy, except while it is being read (area 9 rule 24).
func TestCacheRemovalAndTidyUseOnlyCopiesNobodyWaitsFor(t *testing.T) {
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
			err = d.FinishPrepared(ctx, p.ID, p.PublicID, "mp4", 3, "hash", at)
		} else {
			err = d.SetPreparedState(ctx, p.ID, p.PublicID, state, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Folder, p.PublicID+".mp4"), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
		return p
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
	if err := s.Tidy(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PreparedByID(ctx, recent.ID); err != db.ErrNotFound {
		t.Fatal("tidy kept a copy no download waits for", err)
	}
	if _, err := d.PreparedByID(ctx, running.ID); err != nil {
		t.Fatal("tidy removed a copy being made", err)
	}
}

// A copy a download waits for lasts 48 hours from when it was ready, and an hour past its last
// fetch when a fetch carried on beyond them; then it goes, and the download reads as expired.
func TestACopyWaitedForLastsFortyEightHours(t *testing.T) {
	d, film, wanting := filmLibrary(t)
	clock := time.Now()
	s := service(t, d, t.TempDir(), func() time.Time { return clock })
	ctx := context.Background()
	p, err := d.WantPrepared(ctx, film.MediaID(), "240p", 3, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.FinishPrepared(ctx, p.ID, p.PublicID, "mp4", 3, "hash", clock); err != nil {
		t.Fatal(err)
	}
	wanting(p)
	for _, step := range []struct {
		after   time.Duration
		fetched bool
		kept    bool
	}{{47 * time.Hour, false, true}, {90 * time.Minute, true, true}, {30 * time.Minute, false, true}, {time.Hour, false, false}} {
		clock = clock.Add(step.after)
		if step.fetched {
			if err := d.UsePrepared(ctx, p.ID, clock); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Tidy(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := d.PreparedByID(ctx, p.ID); (err == nil) != step.kept {
			t.Fatalf("at %v the copy kept is %v; want %v", step.after, err == nil, step.kept)
		}
	}
}
