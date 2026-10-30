package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"strconv"
	"strings"
)

const (
	// QualityCDID names the CD-quality tier; QualityLimitPrefix plus the kbps
	// names a numeric audio limit ("limit-320"). A session's quality id uses the
	// same strings, so the current quality is always one of the offered rungs.
	QualityCDID        = "cd-quality"
	QualityLimitPrefix = "limit-"
	// cdMaxAudioBPS is 44.1 kHz x 16 bit x 2 channels. It is the ceiling the
	// fixed rung must carry; the tier itself is a fidelity ceiling.
	cdMaxAudioBPS = 1_411_200
)

// LimitQualityID is the stable rung id for a numeric audio limit.
func LimitQualityID(kbps int) string { return QualityLimitPrefix + strconv.Itoa(kbps) }

// audioOnlyKind reports kinds that play as audio: they never offer the video ladder.
func audioOnlyKind(kind string) bool {
	return kind == "song" || kind == "track" || kind == "audiobook_file" || kind == "audiobook"
}

// MusicQualityOffers is the published rung set for a song. Original and Automatic
// come first (the client contract), then the music tiers in the order playbackv1
// offers them. An audiobook has no music tier and publishes only those two.
func MusicQualityOffers(music bool, available bool, conversion bool, sourceBitrateBPS int64) []QualityRung {
	out := []QualityRung{{ID: "original", Kind: QualityOriginal, Label: "Original", Enabled: available}, {ID: "automatic", Kind: QualityAutomatic, Label: "Automatic", Enabled: available}}
	if !available {
		out[0].Reason, out[1].Reason = RungReasonSourceUnusable, RungReasonSourceUnusable
	}
	if !music {
		return out
	}
	for _, preset := range qualityladder.Music() {
		switch {
		case preset.ID == "original":
			continue
		case preset.ID == "cd-quality":
			rung := QualityRung{ID: QualityCDID, Kind: QualityFixed, Label: "CD quality", MaxAudioBitrateBPS: cdMaxAudioBPS, Enabled: available}
			if !available {
				rung.Reason = RungReasonSourceUnusable
			}
			out = append(out, rung)
		case preset.AudioKbps > 0:
			same := sourceBitrateBPS > 0 && sourceBitrateBPS <= int64(preset.AudioKbps)*1000
			rung := QualityRung{ID: LimitQualityID(preset.AudioKbps), Kind: QualityFixed, Label: strconv.Itoa(preset.AudioKbps) + " kbps", MaxAudioBitrateBPS: preset.AudioKbps * 1000, Enabled: available, SameAsOriginal: same, DataGBPerHour: preset.DataMBPerHour / 1000}
			switch {
			case !available:
				rung.Reason = RungReasonSourceUnusable
			case !conversion && !same:
				rung.Enabled, rung.Reason = false, RungReasonTranscodeOff
			}
			out = append(out, rung)
		}
	}
	return out
}

// currentRungID maps a stored plan quality id onto a rung the source offers, so
// the current quality is never absent from the published set.
func currentRungID(id string, rungs []QualityRung) string {
	has := func(want string) bool {
		for _, r := range rungs {
			if r.ID == want {
				return true
			}
		}
		return false
	}
	if id == "" {
		id = "original"
	}
	if has(id) {
		return id
	}
	if strings.HasPrefix(id, "automatic:") && has("automatic") {
		return "automatic"
	}
	// A numeric audio limit that is not a published step lands on the highest
	// published limit at or under it, else the lowest published one.
	if kbps, ok := strings.CutPrefix(id, QualityLimitPrefix); ok {
		want, _ := strconv.Atoi(kbps)
		best, bestKbps, lowest, lowestKbps := "", 0, "", 0
		for _, r := range rungs {
			if !strings.HasPrefix(r.ID, QualityLimitPrefix) {
				continue
			}
			k := r.MaxAudioBitrateBPS / 1000
			if k <= want && k > bestKbps {
				best, bestKbps = r.ID, k
			}
			if lowest == "" || k < lowestKbps {
				lowest, lowestKbps = r.ID, k
			}
		}
		if best != "" {
			return best
		}
		if lowest != "" {
			return lowest
		}
	}
	return "original"
}
