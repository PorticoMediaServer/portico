package metadata

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// Run with PORTICO_ARTWORK_BENCH=1 go test ./internal/metadata
// -run TestArtworkFortyCardReadLatency -count=1 -v. This local measurement
// includes the selected-identity snapshot, immutable variant open and byte
// stream, but excludes HTTP authorization and a device's decode/render time.
func TestArtworkFortyCardReadLatency(t *testing.T) {
	if os.Getenv("PORTICO_ARTWORK_BENCH") != "1" {
		t.Skip("opt-in local artwork measurement")
	}
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	targets := make([]RepairTarget, 0, 40)
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("art-card-%02d", i)
		wtx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		lib, err := compactcatalog.LibraryTx(ctx, wtx, "lib")
		if err != nil {
			wtx.Rollback()
			t.Fatal(err)
		}
		entity, _, err := compactcatalog.UpsertEntityTx(ctx, wtx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: fmt.Sprintf("benchcard:%02d", i), Title: id, Year: 2020})
		if err != nil {
			wtx.Rollback()
			t.Fatal(err)
		}
		if err = wtx.Commit(); err != nil {
			t.Fatal(err)
		}
		public, err := entityid.Public(ctx, db, entity)
		if err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 800, 1200))
		for y := 0; y < 1200; y++ {
			for x := 0; x < 800; x++ {
				// Distinct deterministic textured images are closer to stored
				// poster byte sizes than forty compressible solid colours.
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(x*3 + y*2 + i*13 + x*y/77),
					G: uint8(x + y*5 + i*17 + x*y/53),
					B: uint8(x*2 + y + i*23 + x*y/97), A: 255,
				})
			}
		}
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 82}); err != nil {
			t.Fatal(err)
		}
		object := publishTestArtwork(t, s, encoded.Bytes(), "2026-09-23T00:00:00Z")
		candidate := "candidate-" + id
		if _, err = db.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at)
			VALUES(?,'item',?,'poster','','fixture',?,'local','fixture','bench','2026-09-23T00:00:00Z')`, candidate, entity, id); err != nil {
			t.Fatal(err)
		}
		// Arrival made both slots; the timing is the read path an actual card
		// load takes after the import has completed.
		if _, err = db.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at)
			VALUES('item',?,'poster','',?,?,0,1,'fixture','2026-09-23T00:00:00Z')`, entity, candidate, object.source); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, RepairTarget{"item", public})
	}
	read := func() (time.Duration, int64) {
		start := time.Now()
		var bytesRead int64
		for _, target := range targets {
			file, mime, err := s.EntityArtworkVariant(ctx, target, "poster", "", ArtworkSmall, "")
			if err != nil || mime != "image/jpeg" {
				t.Fatalf("%s: %s %v", target.ID, mime, err)
			}
			n, err := io.Copy(io.Discard, file)
			file.Close()
			if err != nil || n == 0 {
				t.Fatalf("%s: %d %v", target.ID, n, err)
			}
			bytesRead += n
		}
		return time.Since(start), bytesRead
	}
	first, firstBytes := read()
	repeat, repeatBytes := read()
	if firstBytes != repeatBytes {
		t.Fatal("immutable card bytes changed between reads")
	}
	t.Logf("40 cards (small slot) selected variant open+stream: first=%s repeat=%s bytes=%d", first, repeat, firstBytes)
}
