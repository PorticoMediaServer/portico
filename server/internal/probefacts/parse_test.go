package probefacts

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const validProbe = `{"format":{"format_name":"mov,mp4","start_time":"-0.125000","duration":"1.001000","filename":"private-input-not-retained","tags":{"ignored":"metadata"}},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","time_base":"1/90000","start_pts":-11250,"duration_ts":90090,"width":16,"height":16,"disposition":{"default":1}},{"index":1,"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2,"disposition":{"default":0}}]}`

func TestProbeFactsPreserveExactAndMissingClocks(t *testing.T) {
	facts, err := Parse([]byte(validProbe))
	if err != nil {
		t.Fatal(err)
	}
	if *facts.Format.StartSeconds != (Rational{-1, 8}) || *facts.Format.DurationSeconds != (Rational{1001, 1000}) {
		t.Fatal("decimal clock lost precision")
	}
	v := facts.Streams[0]
	if *v.TimeBase != (Rational{1, 90000}) || *v.StartTimestamp != -11250 || *v.DurationTimestamp != 90090 || !v.Default || v.Video.Width != 16 {
		t.Fatal("stream clock mismatch")
	}
	a := facts.Streams[1]
	if a.TimeBase != nil || a.DurationTimestamp != nil || a.StartTimestamp != nil || a.Audio.SampleRate != 48000 || a.Audio.Channels != 2 {
		t.Fatal("missing clock fabricated")
	}
}

func TestProbeFactsRejectAmbiguityAndMalformedClocks(t *testing.T) {
	cases := []string{
		strings.Replace(validProbe, `"index":0`, `"index":0,"index":9`, 1),
		validProbe + ` {}`,
		strings.Replace(validProbe, `"index":0`, `"index":0,"INDEX":9`, 1),
		strings.Replace(validProbe, `"format":`, `"FORMAT":{},"format":`, 1),
		strings.Replace(validProbe, `"streams":`, `"ſtreams":[],"streams":`, 1),
		strings.Replace(validProbe, `"1/90000"`, `"1/0"`, 1),
		strings.Replace(validProbe, `"1/90000"`, `"0/1"`, 1),
		strings.Replace(validProbe, `"1.001000"`, `"NaN"`, 1),
		strings.Replace(validProbe, `"1.001000"`, `"1e30"`, 1),
		strings.Replace(validProbe, `"1.001000"`, `"9223372036854775808"`, 1),
		strings.Replace(validProbe, `"duration_ts":90090`, `"duration_ts":-1`, 1),
		strings.Replace(validProbe, `"index":1`, `"index":0`, 1),
		strings.Replace(validProbe, `"width":16`, `"width":65537`, 1),
	}
	for i, input := range cases {
		if _, err := Parse([]byte(input)); !errors.Is(err, ErrFacts) {
			t.Fatalf("case%d accepted: %v", i, err)
		}
	}
}

func TestProbeFactsBoundOutputAndStructure(t *testing.T) {
	inputs := []string{strings.Repeat(" ", MaxOutputBytes+1), strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18)}
	streams := make([]string, MaxStreams+1)
	for i := range streams {
		streams[i] = fmt.Sprintf(`{"index":%d,"codec_type":"video","codec_name":"h264"}`, i)
	}
	inputs = append(inputs, `{"format":{"format_name":"mov"},"streams":[`+strings.Join(streams, ",")+`]}`)
	for i, input := range inputs {
		if _, err := Parse([]byte(input)); !errors.Is(err, ErrFacts) {
			t.Fatalf("case%d accepted", i)
		}
	}
}
