package artwork

import (
	"strings"
	"testing"
)

// materialDigestFor returns a lowercase 64-hex digest. The trailing letter keeps
// every fixture non-vacuous for the uppercase-rejection case.
func materialDigestFor(n byte) string {
	return strings.Repeat(string("0123456789abcdef"[n%16]), 63) + "a"
}

func materialSpec(expected int64) MaterialSpec {
	return MaterialSpec{
		IntentID: "job-poster-778", SourceDigest: materialDigestFor(1),
		AcceptedMIME: MaterialJPEG, AcceptedWidth: 3000, AcceptedHeight: 2000,
		LargeEdge: MaterialEdge1920, EncoderRevision: 3, ExpectedSlots: expected,
	}
}

func materialSlots(expected int64) []MaterialSlot {
	slots := []MaterialSlot{
		{Slot: 0, Digest: materialDigestFor(2), MIME: MaterialJPEG, Width: 500, Height: 750, Bytes: 40 << 10},
		{Slot: 1, Digest: materialDigestFor(3), MIME: MaterialJPEG, Width: 1280, Height: 720, Bytes: 120 << 10, Outlier: false},
		{Slot: 2, Digest: materialDigestFor(4), MIME: MaterialPNG, Width: 780, Height: 1170, Bytes: 90 << 10},
	}
	if expected == 4 {
		slots = append(slots, MaterialSlot{Slot: 3, Digest: materialDigestFor(1), MIME: MaterialJPEG,
			Width: 3000, Height: 2000, Bytes: 2 << 20})
	}
	return slots
}

func materialLimits() MaterialLimits {
	return MaterialLimits{Slots: MaterialMaxSlots, TextBytes: 8 << 10, TotalBytes: 64 << 20}
}

// An intent fixes the source facts and the expected slot count before any file
// exists. It retains no bytes and performs no IO.
func TestIPMaterialIntentDeclaration(t *testing.T) {
	for _, expected := range []int64{3, 4} {
		prepared, err := PrepareMaterial(materialSpec(expected), nil, materialLimits())
		if err != nil || !prepared.Valid() || prepared.Spec().ExpectedSlots != expected ||
			prepared.Spec().IntentID != "job-poster-778" || prepared.TotalBytes() != 0 || len(prepared.Slots()) != 0 {
			t.Fatalf("intent %d: %+v, %v", expected, prepared, err)
		}
	}
}

// The installed manifest is complete and immutable; slot 3 is the retained
// original and its bytes stay charged next to the three encodes.
func TestIPMaterialInstalledManifest(t *testing.T) {
	for _, expected := range []int64{3, 4} {
		slots := materialSlots(expected)
		prepared, err := PrepareMaterial(materialSpec(expected), slots, materialLimits())
		if err != nil || !prepared.Valid() || len(prepared.Slots()) != int(expected) {
			t.Fatalf("manifest %d: %+v, %v", expected, prepared, err)
		}
		var want int64
		for _, slot := range slots {
			want += slot.Bytes
		}
		if prepared.TotalBytes() != want {
			t.Fatalf("total bytes %d != %d", prepared.TotalBytes(), want)
		}
		if expected == 4 {
			original := prepared.Slots()[3]
			if original.Digest != prepared.Spec().SourceDigest || original.MIME != prepared.Spec().AcceptedMIME {
				t.Fatalf("original slot lost: %+v", original)
			}
		}
		// The prepared value owns copies, not the caller's slice.
		slots[0].Digest = materialDigestFor(9)
		prepared.Slots()[1].Digest = materialDigestFor(9)
		if prepared.Slots()[0].Digest != materialDigestFor(2) || prepared.Slots()[1].Digest != materialDigestFor(3) {
			t.Fatal("prepared manifest borrowed caller memory")
		}
	}
	// WebP is only the retained original; an encoded slot may not be WebP.
	four := materialSlots(4)
	if _, err := PrepareMaterial(materialSpec(4), four, materialLimits()); err != nil {
		t.Fatalf("webp original refused: %v", err)
	}
	four[3].MIME = MaterialWebP
	spec := materialSpec(4)
	spec.AcceptedMIME = MaterialWebP
	if _, err := PrepareMaterial(spec, four, materialLimits()); err != nil {
		t.Fatalf("webp original refused: %v", err)
	}
	encoded := materialSlots(3)
	encoded[1].MIME = MaterialWebP
	if got, err := PrepareMaterial(materialSpec(3), encoded, materialLimits()); err != ErrMaterial || got.Valid() {
		t.Fatalf("encoded webp admitted: %+v, %v", got, err)
	}
}

func TestIPMaterialRefusesInvalidDeclaration(t *testing.T) {
	for name, mutate := range map[string]func(*MaterialSpec, *[]MaterialSlot, *MaterialLimits){
		"missing-limits": func(_ *MaterialSpec, _ *[]MaterialSlot, l *MaterialLimits) { *l = MaterialLimits{} },
		"slot-budget":    func(_ *MaterialSpec, _ *[]MaterialSlot, l *MaterialLimits) { l.Slots = 2 },
		"text-budget":    func(_ *MaterialSpec, _ *[]MaterialSlot, l *MaterialLimits) { l.TextBytes = 1 },
		"total-budget":   func(_ *MaterialSpec, _ *[]MaterialSlot, l *MaterialLimits) { l.TotalBytes = 1 },
		"total-over-cap": func(_ *MaterialSpec, _ *[]MaterialSlot, l *MaterialLimits) { l.TotalBytes = MaterialMaxTotalBytes + 1 },
		"empty-intent":   func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.IntentID = "" },
		"padded-intent":  func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.IntentID = " job " },
		"long-intent": func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) {
			s.IntentID = strings.Repeat("i", MaterialMaxIntent+1)
		},
		"nul-intent":   func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.IntentID = "job\x00" },
		"short-digest": func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.SourceDigest = "abc" },
		"upper-digest": func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) {
			s.SourceDigest = strings.ToUpper(s.SourceDigest)
		},
		"bad-source-mime":      func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.AcceptedMIME = "image/gif" },
		"zero-width":           func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.AcceptedWidth = 0 },
		"zero-height":          func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.AcceptedHeight = 0 },
		"bad-edge":             func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.LargeEdge = 1024 },
		"zero-encoder":         func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.EncoderRevision = 0 },
		"two-slots":            func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.ExpectedSlots = 2 },
		"five-slots":           func(s *MaterialSpec, _ *[]MaterialSlot, _ *MaterialLimits) { s.ExpectedSlots = 5 },
		"two-slot-manifest":    func(s *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { *p = materialSlots(3)[:2] },
		"gap":                  func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[1].Slot = 5 },
		"negative-slot":        func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[0].Slot = -1 },
		"duplicate-digest":     func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[1].Digest = (*p)[0].Digest },
		"zero-bytes":           func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[0].Bytes = 0 },
		"huge-bytes":           func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[0].Bytes = MaterialMaxSlotBytes + 1 },
		"zero-slot-width":      func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[0].Width = 0 },
		"bad-slot-mime":        func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[0].MIME = "image/gif" },
		"short-slot-digest":    func(_ *MaterialSpec, p *[]MaterialSlot, _ *MaterialLimits) { (*p)[0].Digest = "abc" },
		"manifest-total-bound": func(_ *MaterialSpec, p *[]MaterialSlot, l *MaterialLimits) { l.TotalBytes = (*p)[0].Bytes },
	} {
		t.Run(name, func(t *testing.T) {
			spec := materialSpec(3)
			slots := materialSlots(3)
			limits := materialLimits()
			mutate(&spec, &slots, &limits)
			got, err := PrepareMaterial(spec, slots, limits)
			if got.Valid() || err != ErrMaterial || len(got.Slots()) != 0 || got.TotalBytes() != 0 {
				t.Fatalf("invalid declaration admitted: %+v, %v", got, err)
			}
		})
	}
}

// The retained original must be the accepted source: a different digest,
// format or dimension is not a second copy of the same upload.
func TestIPMaterialRetainedOriginalMustMatchSource(t *testing.T) {
	for name, mutate := range map[string]func([]MaterialSlot){
		"digest": func(slots []MaterialSlot) { slots[3].Digest = materialDigestFor(9) },
		"mime":   func(slots []MaterialSlot) { slots[3].MIME = MaterialPNG },
		"width":  func(slots []MaterialSlot) { slots[3].Width = 3001 },
		"height": func(slots []MaterialSlot) { slots[3].Height = 2001 },
	} {
		t.Run(name, func(t *testing.T) {
			slots := materialSlots(4)
			mutate(slots)
			if got, err := PrepareMaterial(materialSpec(4), slots, materialLimits()); err != ErrMaterial || got.Valid() {
				t.Fatalf("foreign original admitted: %+v, %v", got, err)
			}
		})
	}
}
