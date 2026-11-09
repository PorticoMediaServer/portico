package subtitles

import "testing"

func FuzzTolerantSubtitleFormats(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), uint8(0))
	f.Add([]byte("<tt><p begin=\"1s\" dur=\"2s\">Hello</p></tt>"), uint8(2))
	f.Add([]byte("<SAMI><SYNC Start=1000><P>Hello<SYNC Start=2000><P>&nbsp;</SAMI>"), uint8(3))
	f.Fuzz(func(t *testing.T, raw []byte, choice uint8) {
		if len(raw) > 64<<10 {
			return
		}
		format := []string{"srt", "vtt", "ttml", "sami"}[int(choice)%4]
		cues, err := ParseTolerant(raw, format, 0)
		if err != nil {
			return
		}
		if len(cues) > MaxCues {
			t.Fatal("cue budget exceeded")
		}
		for _, c := range cues {
			if len(c.Text) > MaxCueBytes || c.StartMS < 0 || c.EndMS <= c.StartMS || c.EndMS > MaxDurationMS {
				t.Fatal("cue bounds exceeded")
			}
		}
	})
}
