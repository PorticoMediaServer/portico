package decoder

import (
	"reflect"
	"testing"
)

// No decoder process is needed to prove the bounded argv selection. The real
// TS-AAC origin/owned mux regression remains in linearinput/live_capture_test.
func TestLinearHLSNormalizesOnlySelectedCopiedAACForFragmentedMP4(t *testing.T) {
	for _, tc := range []struct {
		name   string
		plan   LinearEncoding
		filter bool
	}{
		{"selected HLS AAC copy", LinearEncoding{HLSInput: true, Video: true, Audio: true, Fragmented: true, CopyAAC: true}, true},
		{"selected TS input AAC copy", LinearEncoding{Video: true, Audio: true, Fragmented: true, CopyAAC: true}, true},
		{"audio-only AAC copy", LinearEncoding{HLSInput: true, Audio: true, Fragmented: true, CopyAAC: true}, true},
		{"copied HEVC with AAC", LinearEncoding{HLSInput: true, Video: true, Audio: true, Fragmented: true, CopyHEVC: true, CopyAAC: true}, true},
		{"unqualified copied codec", LinearEncoding{HLSInput: true, Video: true, Audio: true, Fragmented: true}, false},
		{"ordinary TS", LinearEncoding{HLSInput: true, Video: true, Audio: true, CopyAAC: true}, false},
		{"converted audio overrides copy fact", LinearEncoding{HLSInput: true, Video: true, Audio: true, Fragmented: true, CopyAAC: true, ConvertAudio: true}, false},
		{"no audio overrides copy fact", LinearEncoding{HLSInput: true, Video: true, Fragmented: true, CopyAAC: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const input = "http://127.0.0.1:43210/input/index.m3u8"
			const output = "http://127.0.0.1:43210/output/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"
			args, hardware, err := linearHLSArgs(input, output, tc.plan)
			if err != nil {
				t.Fatal(err)
			}
			if hardware {
				t.Fatal("ordinary copy/audio conversion acquired a GPU")
			}
			var withoutFilter []string
			count := 0
			for i := 0; i < len(args); i++ {
				if args[i] == "-bsf:a" {
					count++
					if i+1 >= len(args) || args[i+1] != "aac_adtstoasc" {
						t.Fatalf("unexpected audio filter: %v", args)
					}
					i++
					continue
				}
				withoutFilter = append(withoutFilter, args[i])
			}
			wantCount := 0
			if tc.filter {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("audio filter count=%d, want %d: %v", count, wantCount, args)
			}
			baseline := tc.plan
			baseline.CopyAAC = false
			originalArgs, _, err := linearHLSArgs(input, output, baseline)
			if err != nil || !reflect.DeepEqual(withoutFilter, originalArgs) {
				t.Fatalf("changed unrelated input/output/mapping/conversion/guard argv: %v; baseline %v: %v", args, originalArgs, err)
			}
			if tc.plan.CopyHEVC && argValue(args, "-tag:v") != "hvc1" {
				t.Fatal("lost original HEVC copy tag")
			}
		})
	}
}
