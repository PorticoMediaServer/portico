package downloads

import "testing"

func TestMusicTargetKeepsVerifiedLosslessWavPackRequestedBitrate(t *testing.T) {
	lossless, hybrid := true, false
	for _, row := range []struct {
		mode string
		fact *bool
		want int64
	}{{"lossless", &lossless, 320000}, {"hybrid", &hybrid, 64000}, {"unknown", nil, 64000}} {
		t.Run(row.mode, func(t *testing.T) {
			source := source{Kind: "song", AudioCodec: "wavpack", AudioLossless: row.fact, AudioRate: 44100, AudioDepth: 16, AudioChannels: 2, AudioBitrateBPS: 64000}
			for _, id := range []string{"music-aac-320-v1", "music-opus-320-v1"} {
				got, ok := musicTarget(id, source)
				if !ok || len(got.Audio) != 1 || got.Audio[0].BitRate != row.want {
					t.Fatal(id, got, ok, row.want)
				}
			}
		})
	}
}
