package disc

import "testing"

func TestDolbyVisionCandidateUsesExplicitUniqueRolesAcrossEveryClip(t *testing.T) {
	primary := Stream{ClipSequence: 0, PID: 4210, Kind: "video", Coding: 0x24, VideoRole: "primary"}
	el := Stream{ClipSequence: 0, PID: 4120, Kind: "video", Coding: 0x24, VideoRole: "enhancement", DolbyVisionEnhancementKnown: true, DolbyVisionEnhancementLayer: true}
	pip := Stream{ClipSequence: 0, PID: 4200, Kind: "video", Coding: 0x24, VideoRole: "secondary"}
	// Deliberately reverse PID/array ordering: the primary is neither the
	// first/lower video PID nor adjacent to the enhancement PID.
	good := Title{Clips: []Clip{{Sequence: 0}, {Sequence: 1}}, Streams: []Stream{pip, el, primary}}
	for _, stream := range append([]Stream(nil), good.Streams...) {
		stream.ClipSequence = 1
		good.Streams = append(good.Streams, stream)
	}
	if got, ok := good.DolbyVisionCandidate(); !ok || got.BasePID != primary.PID || got.EnhancementPID != el.PID {
		t.Fatalf("explicit roles lost: %+v %v", got, ok)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Title)
	}{
		{"unknown-role", func(t *Title) { t.Streams[0].VideoRole = "" }},
		{"extra-primary", func(t *Title) { t.Streams[0].VideoRole = "primary" }},
		{"extra-el", func(t *Title) { t.Streams[0] = el; t.Streams[0].PID++ }},
		{"missing-clip", func(t *Title) { t.Streams = t.Streams[:3] }},
		{"changed-pid", func(t *Title) { t.Streams[5].PID++ }},
		{"unknown-el", func(t *Title) { t.Streams[1].DolbyVisionEnhancementKnown = false }},
		{"wrong-codec", func(t *Title) { t.Streams[2].Coding = 0x1b }},
		{"conflicting-pmt", func(t *Title) { pid := 4113; t.Streams[1].DolbyVisionBasePID = &pid }},
		{"duplicate-pid", func(t *Title) { t.Streams[0].PID = t.Streams[1].PID }},
		{"no-clips", func(t *Title) { t.Clips = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := good
			copy.Streams = append([]Stream(nil), good.Streams...)
			test.mutate(&copy)
			if got, ok := copy.DolbyVisionCandidate(); ok {
				t.Fatalf("invented candidate: %+v", got)
			}
		})
	}
}
