package decoder

import (
	"strings"
	"testing"
)

func TestMusicPreparedRateDepthAndNoUpgradeR29(t *testing.T) {
	r := PreparedRecipe{AudioOnly: true, MusicQuality: "cd-quality", MusicCodec: "flac", Source: PreparedSource{AudioSampleRate: 176400, AudioBitDepth: 24, AudioChannels: []int{8}, AudioCodec: "flac"}}
	args, e := preparedArgs("http://127.0.0.1/input", r, false)
	if e != nil {
		t.Fatal(e)
	}
	o, e := PreparedMusicFormat(r)
	if e != nil || o.Rate != 44100 || o.Depth != 16 || o.Channels != 8 || !strings.Contains(strings.Join(args, " "), "resampler=soxr:precision=28:osf=dbl") || !strings.Contains(strings.Join(args, " "), "resampler=swr:osf=s16:output_sample_bits=16:dither_method=triangular") {
		t.Fatal(o, args, e)
	}
	r.Source.AudioSampleRate = 192000
	o, e = PreparedMusicFormat(r)
	if e != nil || o.Rate != 48000 {
		t.Fatal(o, e)
	}
	r.MusicQuality = "original"
	r.Source.AudioSampleRate = 88200
	args, e = preparedArgs("http://127.0.0.1/input", r, false)
	if e != nil || strings.Contains(strings.Join(args, " "), "aresample") {
		t.Fatal(args, e)
	}
	r.MusicQuality, r.MusicCodec = "320", "aac"
	r.Source.AudioCodec = "mp3"
	r.Source.AudioBitrateBPS = 128000
	o, e = PreparedMusicFormat(r)
	if e != nil || o.BitrateBPS != 128000 || o.Rate != 88200 {
		t.Fatal(o, e)
	}
	r.MusicQuality, r.MusicCodec = "cd-quality", "flac"
	r.Source.AudioCodec = "dsd_lsbf_planar"
	r.Source.AudioSampleRate = 352800
	args, e = preparedArgs("http://127.0.0.1/input", r, false)
	if e != nil || !strings.Contains(strings.Join(args, " "), "volume=-6dB:precision=double") {
		t.Fatal(args, e)
	}
	r.MusicStart, r.MusicEnd = 10, 9
	if _, e = preparedArgs("http://127.0.0.1/input", r, false); e == nil {
		t.Fatal("reversed cue accepted")
	}
}
