package mediaanalysis

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"
)

// SegmentChapter is a chapter already read by the library scanner.
type SegmentChapter struct {
	Title          string
	StartUS, EndUS int64
}

// SegmentPlanningOptions are the owner's bounded search windows and minimum lengths.
type SegmentPlanningOptions = segmentPlanning

// ReadSegments uses the same bounded listening/picture recipes as the detector's
// accuracy harness. It never reads an entire episode just to find its opening.
func ReadSegments(ctx context.Context, decode Decode, key, kind, mode string, duration int64, audio, video bool,
	picture decoder.PreviewVideoFacts, chapters []SegmentChapter, policy SegmentPlanningOptions) (segmentdetect.Evidence, []segmentdetect.Detection, error) {
	plan := planSegmentsWithPolicy(kind, mode, duration, audio, video, policy)
	evidence, err := extractSegmentEvidence(ctx, decode, key, plan, duration, picture)
	if err != nil {
		return evidence, nil, err
	}
	var names []chapter
	for _, c := range chapters {
		names = append(names, chapter{Title: c.Title, Start: c.StartUS, End: c.EndUS})
	}
	return evidence, fileSegments(evidence, names), nil
}

// CompareSegments confirms only the listening matches supported by neighbouring
// episodes; chapter and picture suggestions remain suggestions.
func CompareSegments(self segmentdetect.Evidence, neighbours []segmentdetect.Evidence) []segmentdetect.Detection {
	var opening, closing []segmentdetect.Fingerprint
	for _, n := range neighbours {
		opening = append(opening, n.Opening)
		closing = append(closing, n.Closing)
	}
	var out []segmentdetect.Detection
	if d, ok := segmentdetect.Intro(self.Opening, segmentdetect.MatchRegion(self.Opening, opening), self.DurationUS); ok {
		out = append(out, d)
	}
	if d, ok := segmentdetect.Credits(self.Closing, segmentdetect.MatchRegion(self.Closing, closing), self.DurationUS); ok {
		out = append(out, d)
	}
	return out
}
