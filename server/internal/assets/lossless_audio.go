package assets

import "strings"

func LosslessAudio(codec string) bool {
	if strings.HasPrefix(codec, "pcm_") || strings.HasPrefix(codec, "dsd_") {
		return true
	}
	switch codec {
	case "flac", "alac", "ape", "tta", "mlp", "truehd":
		return true
	}
	return false
}

// WavPack may be hybrid/lossy. Its codec name alone does not prove losslessness.
func KnownLosslessAudio(codec string) *bool {
	if codec == "wavpack" {
		return nil
	}
	v := LosslessAudio(codec)
	return &v
}
