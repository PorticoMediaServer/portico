package decoder

import "testing"

func TestMusicPreparedBitrateDistinguishesMeasuredWavPackModes(t *testing.T) {
	lossless, hybrid := true, false
	for _, row := range []struct {
		name, codec string
		fact        *bool
		want        int
	}{
		{"lossless", "wavpack", &lossless, 320000},
		{"hybrid", "wavpack", &hybrid, 64000},
		{"unknown", "wavpack", nil, 64000},
		{"unrelated flag", "mp3", &lossless, 64000},
	} {
		for _, output := range []string{"aac", "opus"} {
			t.Run(row.name+output, func(t *testing.T) {
				recipe := PreparedRecipe{AudioOnly: true, MusicQuality: "320", MusicCodec: output, Source: PreparedSource{AudioCodec: row.codec, AudioLossless: row.fact, AudioSampleRate: 44100, AudioBitDepth: 16, AudioBitrateBPS: 64000, AudioChannels: []int{2}}}
				got, err := PreparedMusicFormat(recipe)
				if err != nil || got.BitrateBPS != row.want {
					t.Fatal(got, err, row.want)
				}
			})
		}
	}
}
