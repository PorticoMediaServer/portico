package artwork

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

var ErrMaterial = errors.New("invalid artwork material preparation")

// MaterialMaxSlots is the frozen E intent/slot cardinality: three encoded
// variants, plus the retained original upload in slot 3. Three slots means the
// source image was fetched and is not physically retained; four means the
// caller's uploaded original is kept alongside its encodes.
const (
	MaterialMaxSlots      = 4
	MaterialMaxIntent     = 128
	MaterialMaxSlotBytes  = 64 << 20
	MaterialMaxTotalBytes = 128 << 20
)

// Material MIME values are declared here rather than borrowed from a provider:
// no provider vocabulary or fetch permission is involved.
const (
	MaterialJPEG = "image/jpeg"
	MaterialPNG  = "image/png"
	MaterialWebP = "image/webp"
)

// MaterialEdge is the frozen large-edge ladder, independent of any provider.
type MaterialEdge int64

const (
	MaterialEdge1280 MaterialEdge = 1280
	MaterialEdge1920 MaterialEdge = 1920
	MaterialEdge2560 MaterialEdge = 2560
)

// MaterialSpec is the intent declaration. It is a custody declaration, not
// authority: the writer independently rechecks current owner/lease/source and
// policy before it writes anything.
type MaterialSpec struct {
	IntentID                      string
	SourceDigest                  string
	AcceptedMIME                  string
	AcceptedWidth, AcceptedHeight int64
	LargeEdge                     MaterialEdge
	EncoderRevision               int64
	ExpectedSlots                 int64
}

// MaterialSlot is one immutable installed file. Slot 3 is the retained
// original upload and may be WebP; encoded slots are JPEG or PNG.
type MaterialSlot struct {
	Slot                 int64
	Digest, MIME         string
	Width, Height, Bytes int64
	Outlier              bool
}

// MaterialLimits is the caller's preparation envelope. It bounds the
// declaration itself; physical file bytes and writer memory are separate
// admission.
type MaterialLimits struct {
	Slots      int
	TextBytes  int64
	TotalBytes int64
}

// PreparedMaterial owns copies of the accepted declaration. It grants no file,
// provider, database or writer capability and performs no IO.
type PreparedMaterial struct {
	spec       MaterialSpec
	slots      []MaterialSlot
	totalBytes int64
	valid      bool
}

func (p PreparedMaterial) Valid() bool           { return p.valid }
func (p PreparedMaterial) Spec() MaterialSpec    { return p.spec }
func (p PreparedMaterial) Slots() []MaterialSlot { return slices.Clone(p.slots) }
func (p PreparedMaterial) TotalBytes() int64     { return p.totalBytes }

// PrepareMaterial validates one complete declaration. An empty slot list is an
// intent: it fixes the source facts and the expected 3/4 slot count before any
// file exists. A non-empty list is the installed handoff and must contain the
// complete immutable manifest, including the retained original in slot 3 when
// four slots were declared.
func PrepareMaterial(spec MaterialSpec, slots []MaterialSlot, limits MaterialLimits) (PreparedMaterial, error) {
	if limits.Slots <= 0 || limits.Slots > MaterialMaxSlots || limits.TextBytes <= 0 ||
		limits.TotalBytes <= 0 || limits.TotalBytes > MaterialMaxTotalBytes {
		return PreparedMaterial{}, ErrMaterial
	}
	remaining := limits.TextBytes
	if !chargeMaterialText(&remaining, spec.IntentID, spec.SourceDigest, spec.AcceptedMIME) ||
		!materialIntentText(spec.IntentID) || !materialDigest(spec.SourceDigest) ||
		!materialMIME(spec.AcceptedMIME) || spec.AcceptedWidth <= 0 || spec.AcceptedHeight <= 0 ||
		!materialEdge(spec.LargeEdge) || spec.EncoderRevision <= 0 ||
		(spec.ExpectedSlots != 3 && spec.ExpectedSlots != 4) ||
		spec.ExpectedSlots > int64(limits.Slots) {
		return PreparedMaterial{}, ErrMaterial
	}
	if len(slots) > 0 && int64(len(slots)) != spec.ExpectedSlots {
		return PreparedMaterial{}, ErrMaterial
	}
	seenDigests := make(map[string]struct{}, len(slots))
	var total int64
	for n, slot := range slots {
		if slot.Slot != int64(n) || !materialDigest(slot.Digest) || !materialMIME(slot.MIME) ||
			slot.Width <= 0 || slot.Height <= 0 || slot.Bytes <= 0 ||
			slot.Bytes > MaterialMaxSlotBytes || total > MaterialMaxTotalBytes-slot.Bytes ||
			!chargeMaterialText(&remaining, slot.Digest, slot.MIME) {
			return PreparedMaterial{}, ErrMaterial
		}
		if slot.Slot < 3 && slot.MIME == MaterialWebP {
			return PreparedMaterial{}, ErrMaterial
		}
		if _, ok := seenDigests[slot.Digest]; ok {
			return PreparedMaterial{}, ErrMaterial
		}
		seenDigests[slot.Digest] = struct{}{}
		total += slot.Bytes
	}
	if total > limits.TotalBytes {
		return PreparedMaterial{}, ErrMaterial
	}
	if len(slots) > 0 && spec.ExpectedSlots == 4 {
		// The uploaded original is the accepted source: same bytes, same
		// declared format and dimensions. It stays charged as slot 3.
		original := slots[3]
		if original.Digest != spec.SourceDigest || original.MIME != spec.AcceptedMIME ||
			original.Width != spec.AcceptedWidth || original.Height != spec.AcceptedHeight {
			return PreparedMaterial{}, ErrMaterial
		}
	}
	spec.IntentID, spec.SourceDigest, spec.AcceptedMIME = strings.Clone(spec.IntentID),
		strings.Clone(spec.SourceDigest), strings.Clone(spec.AcceptedMIME)
	out := PreparedMaterial{spec: spec, totalBytes: total, valid: true}
	for _, slot := range slots {
		slot.Digest, slot.MIME = strings.Clone(slot.Digest), strings.Clone(slot.MIME)
		out.slots = append(out.slots, slot)
	}
	return out, nil
}

func materialIntentText(value string) bool {
	return len(value) > 0 && len(value) <= MaterialMaxIntent && utf8.ValidString(value) &&
		strings.IndexByte(value, 0) < 0 && strings.TrimSpace(value) == value
}

func materialDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func materialMIME(value string) bool {
	return value == MaterialJPEG || value == MaterialPNG || value == MaterialWebP
}

func materialEdge(value MaterialEdge) bool {
	return value == MaterialEdge1280 || value == MaterialEdge1920 || value == MaterialEdge2560
}

func chargeMaterialText(remaining *int64, values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || int64(len(value)) > *remaining {
			return false
		}
		*remaining -= int64(len(value))
	}
	return true
}
