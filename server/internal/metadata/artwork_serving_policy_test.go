package metadata

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
)

// Serving opens one of the files made at arrival and nothing else: the small
// slot at 800, the large slot at the policy edge. No request resizes, encodes
// or writes, whatever it asks for.
func TestArtworkServingOpensArrivalSlotsOnly(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	setArtworkPolicy(t, s, 2560)
	before, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UploadArtwork(ctx, target, "poster", "", before.Revision, editorJPEG(t, 2700, 1080), editorActor(), allowAll); err != nil {
		t.Fatal(err)
	}
	var source string
	if err = db.QueryRow(`SELECT digest FROM artwork_selections WHERE role='poster'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	small, large, _ := testSourceBlobs(t, db, source)
	files := func() int {
		entries, err := os.ReadDir(s.cacheRoot)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	var blobs int
	if err = db.QueryRow(`SELECT count(*) FROM artwork_blobs`).Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	stored := files()
	for _, tc := range []struct {
		size  ArtworkSize
		blob  string
		width int
	}{{ArtworkSmall, small, artworkSmallEdge}, {ArtworkLarge, large, 2560}, {ArtworkSizeForWidth(400), small, artworkSmallEdge}, {ArtworkSizeForWidth(1920), large, 2560}} {
		f, mime, err := s.EntityArtworkVariant(ctx, target, "poster", "", tc.size, source)
		if err != nil || mime != "image/jpeg" {
			t.Fatal(tc.size, mime, err)
		}
		name := filepath.Base(f.Name())
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil || name != tc.blob+".img" || cfg.Width != tc.width {
			t.Fatal("served file is not the slot made at arrival", tc.size, name, cfg, err)
		}
	}
	var after int
	if err = db.QueryRow(`SELECT count(*) FROM artwork_blobs`).Scan(&after); err != nil || after != blobs || files() != stored {
		t.Fatal("a request wrote artwork", blobs, after, err)
	}
	// A missing slot is pending; it is never resized from or replaced by the
	// other slot.
	if err = os.Remove(filepath.Join(s.cacheRoot, small+".img")); err != nil {
		t.Fatal(err)
	}
	if f, _, err := s.EntityArtworkVariant(ctx, target, "poster", "", ArtworkSmall, source); !errors.Is(err, ErrArtworkPending) {
		if f != nil {
			f.Close()
		}
		t.Fatal("a missing small slot was substituted", err)
	}
	if files() != stored-1 {
		t.Fatal("a request rebuilt a slot")
	}
	if _, _, err := s.EntityArtworkVariant(ctx, target, "poster", "", ArtworkSize(9), source); !errors.Is(err, ErrRepairInput) {
		t.Fatal("unknown size accepted", err)
	}
}

func TestArtworkLegacyWidthsMapToSlots(t *testing.T) {
	for width, want := range map[int]ArtworkSize{1: ArtworkSmall, 400: ArtworkSmall, 800: ArtworkSmall, 801: ArtworkLarge, 1920: ArtworkLarge, 2560: ArtworkLarge, 9999: ArtworkLarge} {
		if got := ArtworkSizeForWidth(width); got != want {
			t.Fatal(width, got, want)
		}
	}
}
