package metadata

import (
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"math/rand"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func artworkPolicyService(t *testing.T) *Service {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "metadata.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Service{db: db}
}
func setArtworkPolicy(t *testing.T, s *Service, edge int) {
	t.Helper()
	if _, err := dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassMaintenance, `UPDATE metadata_runtime_settings SET revision=revision+1,artwork_max_edge=? WHERE singleton=1`, edge); err != nil {
		t.Fatal(err)
	}
}
func TestArtworkContextCapturesActualPolicyAndGuardsPublicationRevision(t *testing.T) {
	s := artworkPolicyService(t)
	ctx := context.Background()
	for _, edge := range []int{1280, 1920, 2560} {
		setArtworkPolicy(t, s, edge)
		encoded, check, err := s.CaptureArtworkContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return check(ctx, tx) }); err != nil {
			t.Fatal(err)
		}
		picture := image.NewNRGBA(image.Rect(0, 0, 2700, 1080))
		for i := range picture.Pix {
			picture.Pix[i] = 255
		}
		out, err := encodeArtworkSlot(encoded, picture, artworkSlotLarge)
		raw, w, h := out.data, out.width, out.height
		if err != nil || w != edge || h != edge*1080/2700 || len(raw) > artworkLargeCap[edge] {
			t.Fatal("captured policy did not reach real encoder", edge, w, h, len(raw), err)
		}
		out, err = encodeArtworkSlot(encoded, picture, artworkSlotSmall)
		w, h = out.width, out.height
		if err != nil || max(w, h) != artworkSmallEdge {
			t.Fatal("small slot followed the large policy", w, h, err)
		}
		if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `UPDATE metadata_runtime_settings SET revision=revision+1 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return check(ctx, tx) }); !errors.Is(err, ErrEpisodeThumbnailEncodingStale) {
			t.Fatal("changed policy revision published stale pixels", err)
		}
	}
}
func TestArtworkContextHonoursLargerByteCeilingWithoutEnlargingPictures(t *testing.T) {
	s := artworkPolicyService(t)
	ctx := context.Background()
	picture := image.NewNRGBA(image.Rect(0, 0, 1600, 1000))
	random := rand.New(rand.NewSource(88))
	for i := 0; i < len(picture.Pix); i += 4 {
		picture.Pix[i] = byte(random.Intn(256))
		picture.Pix[i+1] = byte(random.Intn(256))
		picture.Pix[i+2] = byte(random.Intn(256))
		picture.Pix[i+3] = 255
	}
	// Noise needs more than 1920's whole allowance at the quality floor. The
	// 2560 policy keeps it at full size inside its own allowance, never smaller.
	setArtworkPolicy(t, s, 1920)
	standard, _, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = encodeArtworkSlot(standard, picture, artworkSlotLarge); !errors.Is(err, errArtworkCapUnreachable) {
		t.Fatal("the 1920 cap was stretched or the picture was shrunk", err)
	}
	setArtworkPolicy(t, s, 2560)
	larger, _, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := encodeArtworkSlot(larger, picture, artworkSlotLarge)
	raw, w, h := out.data, out.width, out.height
	if err != nil || len(raw) <= 500000 || len(raw) > artworkLargeCap[2560]*3/2 || w != 1600 || h != 1000 {
		t.Fatal("larger policy did not use its actual bounded byte allowance", len(raw), w, h, err)
	}
	small := image.NewUniform(color.NRGBA{R: 20, G: 40, B: 60, A: 255})
	bounded := image.NewNRGBA(image.Rect(0, 0, 640, 320))
	for y := 0; y < 320; y++ {
		for x := 0; x < 640; x++ {
			bounded.Set(x, y, small.C)
		}
	}
	for _, edge := range []int{1280, 2560} {
		setArtworkPolicy(t, s, edge)
		encoded, _, err := s.CaptureArtworkContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out, err = encodeArtworkSlot(encoded, bounded, artworkSlotLarge)
		w, h = out.width, out.height
		if err != nil || w != 640 || h != 320 {
			t.Fatal("small picture was enlarged", edge, w, h, err)
		}
	}
}
func TestArtworkContextFailsClosedForMissingPolicyAndNilPublicationTx(t *testing.T) {
	s := artworkPolicyService(t)
	ctx := context.Background()
	_, check, err := s.CaptureArtworkContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = check(ctx, nil); err == nil {
		t.Fatal("nil publication transaction accepted")
	}
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `DELETE FROM metadata_runtime_settings WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if encoded, guard, err := s.CaptureArtworkContext(ctx); !errors.Is(err, sql.ErrNoRows) || encoded != nil || guard != nil {
		t.Fatal("missing canonical policy became a default", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = s.CaptureArtworkContext(cancelled); err == nil {
		t.Fatal("cancelled capture succeeded")
	}
}
