package playbackv1

import (
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
)

func musicKind(kind string) bool { return kind == "song" || kind == "track" }

// musicQuality uses the existing numeric audio limit contract. Music's saved
// values are never accepted as fixed video ladder identifiers.
func musicQuality(saved string) Quality {
	if saved == "cd-quality" {
		return Quality{Mode: QualityCD}
	}
	if preset, ok := qualityladder.FindMusic(saved); ok && preset.AudioKbps > 0 {
		return Quality{Mode: QualityLimit, MaxAudioBitrateKbps: preset.AudioKbps}
	}
	return Quality{Mode: QualityOriginal}
}

func defaultQuality(saved, music, transport, kind string) Quality {
	if musicKind(kind) {
		if music != "" {
			return musicQuality(music)
		}
		// Retain an explicitly saved choice, including Original. Registry
		// defaults are removed by the source-aware preference readers.
		if saved == "" && playback.NormalizeTransportClass(transport) == "cellular" {
			return musicQuality("320")
		}
	}
	audio := musicKind(kind) || kind == "audiobook_file" || kind == "audiobook"
	return savedQuality(playback.DefaultSavedQuality(saved, transport, audio))
}

func musicQualityOptions(source SourceFacts) []QualityOption {
	out := []QualityOption{{Mode: QualityOriginal, Applicable: true}}
	for _, preset := range qualityladder.Music() {
		if preset.ID == "cd-quality" {
			out = append(out, QualityOption{Mode: QualityCD, Applicable: true})
			continue
		}
		if preset.AudioKbps == 0 {
			continue
		}
		out = append(out, QualityOption{Mode: QualityLimit, MaxAudioBitrateKbps: preset.AudioKbps,
			DataGBPerHour: preset.DataMBPerHour / 1000, Applicable: true,
			SameAsOriginal: source.BitrateKbps > 0 && source.BitrateKbps <= preset.AudioKbps})
	}
	return out
}
