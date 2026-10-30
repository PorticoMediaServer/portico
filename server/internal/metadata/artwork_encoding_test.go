package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
	"testing"
)

// artworkFixture is a deterministic picture: smooth gradients compress well,
// noise does not. Alpha, when set, varies across the picture.
func artworkFixture(w, h int, noise, alpha bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var random uint32 = 42
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			random = random*1664525 + 1013904223
			c := color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) * 127 / (w + h)), 255}
			if noise {
				c = color.NRGBA{uint8(random), uint8(random >> 8), uint8(random >> 16), 255}
			}
			if alpha {
				c.A = uint8(x * 255 / w)
				if noise {
					c.A = uint8(random >> 24)
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// artworkLadder encodes the scaled picture at every quality rung, exactly as
// the slot encoder scales it.
func artworkLadder(t *testing.T, source image.Image, w, h int) map[int][]byte {
	t.Helper()
	scaled := image.NewNRGBA(image.Rect(0, 0, w, h))
	if err := imagework.CatmullRom(context.Background(), scaled, source); err != nil {
		t.Fatal(err)
	}
	out := map[int][]byte{}
	for _, quality := range artworkJPEGQualities {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, scaled, &jpeg.Options{Quality: quality}); err != nil {
			t.Fatal(err)
		}
		out[quality] = b.Bytes()
	}
	return out
}

// Opaque art is JPEG at full slot size. The first ladder rung within the cap
// wins; past every rung, only the floor may publish, inside 1.5x the cap, and
// it is counted. Nothing is ever encoded below q75 or at a smaller size.
func TestArtworkSlotJPEGLadderFloorAndAllowance(t *testing.T) {
	ctx := context.Background()
	source := artworkFixture(1200, 1800, true, false)
	w, h := 533, artworkSmallEdge
	ladder := artworkLadder(t, source, w, h)
	q90, q82, q75 := len(ladder[90]), len(ladder[82]), len(ladder[75])
	if !(q90 > q82 && q82 > q75) {
		t.Fatal("fixture ladder is not monotonic", q90, q82, q75)
	}
	cases := []struct {
		name  string
		limit int
		want  []byte
		state string
	}{
		{"first rung fits", q90, ladder[90], "within"},
		{"middle rung fits", q82, ladder[82], "within"},
		{"floor fits", q75, ladder[75], "within"},
		{"floor inside allowance", q75 - 1, ladder[75], "outlier"},
		{"floor at the allowance edge", (q75*2 + 2) / 3, ladder[75], "outlier"},
		{"floor past the allowance", q75*2/3 - 1, nil, "unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := encodeArtworkWithin(ctx, source, artworkSlotSmall, artworkSmallEdge, tc.limit)
			if tc.state == "unreachable" {
				var refusal *artworkCapError
				if !errors.Is(err, errArtworkCapUnreachable) || !errors.As(err, &refusal) || refusal.slot != artworkSlotSmall || refusal.png || out.data != nil {
					t.Fatal("a picture past its allowance was published or its refusal is untyped", len(out.data), err)
				}
				return
			}
			if err != nil || out.width != w || out.height != h || out.mime != "image/jpeg" {
				t.Fatal("the slot was not encoded at its full size", out.width, out.height, err)
			}
			if !bytes.Equal(out.data, tc.want) {
				t.Fatal("the encoder left the quality ladder", len(out.data), len(tc.want))
			}
			if out.outlier != (tc.state == "outlier") || len(out.data) > tc.limit*3/2 {
				t.Fatal("outlier state or allowance is wrong", out.outlier, len(out.data), tc.limit)
			}
		})
	}
}

// The real slots: a smooth picture fits both caps at full size; noise at the
// small slot is past its allowance even at q75 and is refused, not shrunk.
func TestArtworkSlotsUseTheirApprovedEdgesAndCaps(t *testing.T) {
	ctx := context.Background()
	smooth := artworkFixture(2400, 1600, false, false)
	for _, slot := range []artworkSlot{artworkSlotSmall, artworkSlotLarge} {
		edge, limit := artworkSlotLimits(ctx, slot)
		out, err := encodeArtworkSlot(ctx, smooth, slot)
		if err != nil || out.width != edge || out.height != edge*1600/2400 || len(out.data) > limit || out.outlier {
			t.Fatal("smooth art did not fit its slot at full size", slot, out.width, out.height, len(out.data), err)
		}
		if _, format, err := image.DecodeConfig(bytes.NewReader(out.data)); err != nil || format != "jpeg" {
			t.Fatal("opaque art was not JPEG", format, err)
		}
	}
	if edge, limit := artworkSlotLimits(ctx, artworkSlotSmall); edge != 800 || limit != 150_000 {
		t.Fatal("small slot limits", edge, limit)
	}
	for edge, limit := range map[int]int{1280: 300_000, 1920: 500_000, 2560: 800_000} {
		if artworkLargeCap[edge] != limit {
			t.Fatal("large cap", edge, artworkLargeCap[edge])
		}
	}
	// Unscaled noise at exactly the small edge needs about 260 KB at q75.
	if _, err := encodeArtworkSlot(ctx, artworkFixture(533, 800, true, false), artworkSlotSmall); !errors.Is(err, errArtworkCapUnreachable) {
		t.Fatal("noise past the small allowance was published", err)
	}
}

// Opaque PNG input is still opaque art: it becomes JPEG.
func TestArtworkSlotEncodesOpaquePNGInputAsJPEG(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, artworkFixture(900, 600, false, false)); err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(source.Bytes()))
	if err != nil || format != "png" {
		t.Fatal(format, err)
	}
	out, err := encodeArtworkSlot(context.Background(), decoded, artworkSlotSmall)
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err = image.DecodeConfig(bytes.NewReader(out.data)); err != nil || format != "jpeg" {
		t.Fatal("opaque PNG input stayed PNG", format, err)
	}
}

// Transparency is lossless PNG: the decoded pixels are exactly the scaled
// pixels, whether written as an exact palette or full colour. Noisy alpha past
// the allowance is refused, never shrunk or flattened.
func TestArtworkSlotKeepsAlphaLossless(t *testing.T) {
	ctx := context.Background()
	flat := image.NewNRGBA(image.Rect(0, 0, 600, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 600; x++ {
			flat.SetNRGBA(x, y, color.NRGBA{uint8(x / 100 * 40), 80, 200, uint8(y / 60 * 50)})
		}
	}
	for name, source := range map[string]*image.NRGBA{"palette": flat, "full colour": artworkFixture(700, 400, false, true)} {
		out, err := encodeArtworkSlot(ctx, source, artworkSlotSmall)
		raw, w, h := out.data, out.width, out.height
		if err != nil || w != source.Bounds().Dx() || h != source.Bounds().Dy() || out.mime != "image/png" {
			t.Fatal(name, "alpha slot changed size", w, h, err)
		}
		decoded, format, err := image.Decode(bytes.NewReader(raw))
		if err != nil || format != "png" {
			t.Fatal(name, "alpha was not PNG", format, err)
		}
		scaled := image.NewNRGBA(image.Rect(0, 0, w, h))
		if err = imagework.CatmullRom(ctx, scaled, source); err != nil {
			t.Fatal(err)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if got := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA); got != scaled.NRGBAAt(x, y) {
					t.Fatal(name, "PNG was not lossless at", x, y, got, scaled.NRGBAAt(x, y))
				}
			}
		}
		if _, paletted := decoded.(*image.Paletted); paletted != (name == "palette") {
			t.Fatal(name, "exact palette choice", paletted)
		}
	}
	_, err := encodeArtworkSlot(ctx, artworkFixture(700, 400, true, true), artworkSlotSmall)
	var refusal *artworkCapError
	if !errors.Is(err, errArtworkCapUnreachable) || !errors.As(err, &refusal) || !refusal.png {
		t.Fatal("noisy alpha past the allowance was published", err)
	}
}

// The output buffer is charged once at the allowance and never grows past it.
func TestArtworkBoundedBufferRefusesToGrow(t *testing.T) {
	out := &artworkBoundedBuffer{limit: 8, b: make([]byte, 0, 8)}
	if n, err := out.Write([]byte("12345678")); n != 8 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := out.Write([]byte("9")); n != 0 || !errors.Is(err, errArtworkEncodedTooLarge) || cap(out.b) != 8 {
		t.Fatal("bounded buffer grew", n, err, cap(out.b))
	}
}

// A shrunk large-edge policy re-makes only the source's large file, from the
// large file. The source identity, the lock, the upload and the undo history
// are untouched byte for byte (HIST2), and an open reader of the old file keeps
// working.
func TestArtworkCompactionPreservesSelectionAndLocks(t *testing.T) {
	s, db, target, _ := integrationScreen(t)
	ctx := context.Background()
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(p08cPNG(t, 20, 20))), Header: http.Header{}}, nil
	})}
	if err := s.ScreenStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	entity := resolveArtworkTestEntity(t, db, target)
	integrationExec(t, db, `UPDATE metadata_runtime_settings SET artwork_max_edge=2560`)
	old := publishTestArtwork(t, s, p08cPNG(t, 2400, 1600), "2020-01-01T00:00:00Z")
	_, oldLarge, _ := testSourceBlobs(t, db, old.source)
	integrationExec(t, db, `UPDATE artwork_selections SET digest=?,locked=1 WHERE entity_id=? AND role='poster'`, old.source, entity)
	integrationExec(t, db, `INSERT INTO artwork_uploads SELECT candidate_id,kind,entity_id,role,subject,digest,'image/png',1,2400,1600,'owner','2020-01-01T00:00:00Z' FROM artwork_selections WHERE entity_id=? AND role='poster'`, entity)
	history := `{"digest":"` + old.source + `"}`
	integrationExec(t, db, `INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('item',?,'before','after','upload','owner','2020-01-01T00:00:00Z',?,?)`, entity, history, history)
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM artwork_selections WHERE entity_id=? AND role='poster'`, entity).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(filepath.Join(s.cacheRoot, oldLarge+".img"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	integrationExec(t, db, `UPDATE metadata_runtime_settings SET artwork_max_edge=1920`)
	if err = s.CompactArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	var digest string
	var locked int
	var after int64
	if err = db.QueryRow(`SELECT digest,locked,revision FROM artwork_selections WHERE entity_id=? AND role='poster'`, entity).Scan(&digest, &locked, &after); err != nil {
		t.Fatal(err)
	}
	if digest != old.source || locked != 1 || after != revision+1 {
		t.Fatal("selection identity or lock changed, or cached pages were not retired", digest, locked, after, revision)
	}
	var retained string
	if err = db.QueryRow(`SELECT digest FROM artwork_uploads WHERE entity_id=?`, entity).Scan(&retained); err != nil || retained != old.source {
		t.Fatal("upload reference changed", retained, err)
	}
	var before, afterJSON string
	if err = db.QueryRow(`SELECT before_json,after_json FROM metadata_owner_history WHERE entity_id=?`, entity).Scan(&before, &afterJSON); err != nil || before != history || afterJSON != history {
		t.Fatal("undo history was rewritten", before, afterJSON, err)
	}
	var largeEdge int
	var large string
	if err = db.QueryRow(`SELECT large_blob,large_edge FROM artwork_sources WHERE digest=?`, old.source).Scan(&large, &largeEdge); err != nil || large == oldLarge || largeEdge != 1920 {
		t.Fatal("large slot was not re-made under the new edge", large, largeEdge, err)
	}
	f, _, err := s.Artwork(ctx, target.ID, "poster")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || max(cfg.Width, cfg.Height) > artworkLargeEdge {
		t.Fatal(cfg, err)
	}
	if data, err := io.ReadAll(reader); err != nil || len(data) != old.large.size {
		t.Fatal("old reader invalidated", err)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("dangling artwork reference")
	}
	if err = s.CompactArtworkStep(ctx); err != nil {
		t.Fatal("idempotent compaction", err)
	}
}
