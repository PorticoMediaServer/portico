package playback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"strconv"
)

// QualityRung kinds. Exactly one automatic rung exists in a published set; it is
// the server deciding. Original precedes it in the published menu.
const (
	QualityAutomatic = "automatic"
	QualityOriginal  = "original"
	QualityFixed     = "fixed"
)

// QualityRung is one selectable delivery target. A fixed rung carries its own
// ceilings; automatic and original carry none, because their ceiling is the
// resolved policy and the source respectively.
type QualityRung struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	Label               string  `json:"label"`
	MaxVideoBitrateBPS  int     `json:"maxVideoBitrateBps,omitempty"`
	MaxAudioBitrateBPS  int     `json:"maxAudioBitrateBps,omitempty"`
	TargetDisplayHeight int     `json:"targetDisplayHeight,omitempty"`
	Enabled             bool    `json:"enabled"`
	Reason              string  `json:"reason,omitempty"`
	SameAsOriginal      bool    `json:"sameAsOriginal"`
	DataGBPerHour       float64 `json:"dataGBPerHour,omitempty"`
}

// QualityPreset is one canonical video ceiling and its portable audio budget.
type QualityPreset = qualityladder.Preset

// QualityLadder derives from the one canonical vocabulary.
var QualityLadder = qualityladder.Fixed()

// LadderPreset finds a rung by id.
func LadderPreset(id string) (QualityPreset, bool) {
	for _, p := range QualityLadder {
		if p.ID == id {
			return p, true
		}
	}
	return QualityPreset{}, false
}

// Rung reason codes. They travel to the client, so they are stable.
const (
	RungReasonAboveSource     = "above_source_height"
	RungReasonTranscodeOff    = "transcoding_disabled"
	RungReasonExceedsPolicy   = "exceeds_network_policy"
	RungReasonSourceUnusable  = "source_unavailable"
	RungReasonOriginalRefused = "original_exceeds_policy"
)

// QualityOffers builds the published rung set for one source.
//
// Every canonical step remains visible. A step the source already satisfies
// keeps the original bytes; other steps require conversion support and remain
// subject to the resolved owner/member network policy.
func QualityOffers(sourceHeight int, available bool, transcoding bool, policy ResolvedDeliveryPolicy, sourceBitrateBPS ...int64) []QualityRung {
	rate := int64(0)
	if len(sourceBitrateBPS) > 0 {
		rate = sourceBitrateBPS[0]
	}
	out := []QualityRung{{ID: "original", Kind: QualityOriginal, Label: "Original", Enabled: available}, {ID: "automatic", Kind: QualityAutomatic, Label: "Automatic", Enabled: available}}
	for i := range out {
		if !available {
			out[i].Reason = RungReasonSourceUnusable
		}
	}
	for _, preset := range QualityLadder {
		same := preset.SameAsSource(sourceHeight, rate)
		rung := QualityRung{ID: preset.ID, Kind: QualityFixed, Label: preset.Label, MaxVideoBitrateBPS: preset.VideoBitrateBPS, MaxAudioBitrateBPS: preset.MaxAudioBitrateBPS, TargetDisplayHeight: preset.Height, Enabled: available, SameAsOriginal: same, DataGBPerHour: preset.DataGBPerHour}
		switch {
		case !available:
			rung.Reason = RungReasonSourceUnusable
		case !transcoding && !same:
			rung.Enabled, rung.Reason = false, RungReasonTranscodeOff
		// A same-as-source row still cannot evade a narrower owner/member lane.
		case policy.MaxVideoHeight > 0 && minPositive(sourceHeight, preset.Height) > policy.MaxVideoHeight:
			rung.Enabled, rung.Reason = false, RungReasonExceedsPolicy
		case policy.MaxVideoBitrateBPS > 0 && (!same && preset.VideoBitrateBPS > policy.MaxVideoBitrateBPS || same && rate > int64(policy.MaxVideoBitrateBPS)):
			rung.Enabled, rung.Reason = false, RungReasonExceedsPolicy
		}
		out = append(out, rung)
	}
	return out
}
func minPositive(a, b int) int {
	if a > 0 && a < b {
		return a
	}
	return b
}

// OffersRevision fences a rung selection. A client that names a rung also names
// the revision it read; the server refuses a selection made against a different
// ladder rather than silently applying a rung the viewer never saw.
func OffersRevision(sourceID string, rungs []QualityRung, policy ResolvedDeliveryPolicy) string {
	raw, _ := json.Marshal([]any{sourceID, rungs, policy.NetworkClass, policy.MaxVideoBitrateBPS, policy.MaxAudioBitrateBPS, policy.MaxVideoHeight, policy.AllowHDR})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SelectedRung resolves a selection id against a published set. An unknown or
// disabled id is refused; the caller republishes the set rather than guessing.
func SelectedRung(rungs []QualityRung, id string) (QualityRung, bool) {
	if id == "" {
		id = "original"
	}
	for _, r := range rungs {
		if r.ID == id {
			return r, r.Enabled
		}
	}
	return QualityRung{}, false
}

// QualityTarget is the rung a plan is actually built for, after the resolved
// policy has narrowed whatever the rung itself asked for.
type QualityTarget struct {
	RungID             string
	Kind               string
	MaxVideoBitrateBPS int
	MaxAudioBitrateBPS int
	MaxVideoHeight     int
	AllowHDR           bool
}

// Target narrows one rung by the resolved policy. The narrower of the two wins
// on every axis, so a fixed rung can never buy more than the network lane allows.
func (r QualityRung) Target(policy ResolvedDeliveryPolicy, sourceHeight int) QualityTarget {
	out := QualityTarget{RungID: r.ID, Kind: r.Kind, AllowHDR: policy.AllowHDR}
	narrow := func(a, b int) int {
		if a <= 0 {
			return b
		}
		if b <= 0 || a < b {
			return a
		}
		return b
	}
	switch r.Kind {
	case QualityOriginal:
		out.MaxVideoBitrateBPS, out.MaxAudioBitrateBPS, out.MaxVideoHeight = policy.MaxVideoBitrateBPS, policy.MaxAudioBitrateBPS, policy.MaxVideoHeight
	case QualityFixed:
		// Fixed choices are ceilings. They never force a conversion by name.
		out.Kind = QualityAutomatic
		if r.SameAsOriginal {
			out.MaxVideoBitrateBPS = policy.MaxVideoBitrateBPS
			out.MaxAudioBitrateBPS = policy.MaxAudioBitrateBPS
			out.MaxVideoHeight = policy.MaxVideoHeight
			return out
		}
		out.MaxVideoBitrateBPS = narrow(r.MaxVideoBitrateBPS, policy.MaxVideoBitrateBPS)
		out.MaxAudioBitrateBPS = narrow(r.MaxAudioBitrateBPS, policy.MaxAudioBitrateBPS)
		out.MaxVideoHeight = narrow(r.TargetDisplayHeight, policy.MaxVideoHeight)
	default:
		out.MaxVideoBitrateBPS = policy.MaxVideoBitrateBPS
		out.MaxAudioBitrateBPS = policy.MaxAudioBitrateBPS
		out.MaxVideoHeight = policy.MaxVideoHeight
	}
	if sourceHeight > 0 && out.MaxVideoHeight > sourceHeight {
		out.MaxVideoHeight = sourceHeight
	}
	return out
}

func (t QualityTarget) String() string {
	return t.RungID + ":" + strconv.Itoa(t.MaxVideoHeight) + ":" + strconv.Itoa(t.MaxVideoBitrateBPS)
}

// ErrQualityUnavailable is returned when a client names a rung this source and
// policy do not publish. The client republishes the offer set rather than
// falling back to a rung the viewer never chose.
var ErrQualityUnavailable = errors.New("The selected quality is no longer available. Choose another.")

// validQualitySelection accepts only the published rung identifiers.
func validQualitySelection(id string) bool {
	if id == "automatic" || id == "original" {
		return true
	}
	_, ok := LadderPreset(id)
	return ok
}
