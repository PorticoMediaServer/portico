package mediaanalysis

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

func TestSegmentSearchUsesTheOwnersWindowsAndMinimumLengths(t *testing.T) {
	read := func(kind, mode string, seconds int64, policy SegmentPlanningOptions) []decoder.AnalysisSpec {
		t.Helper()
		var observed []decoder.AnalysisSpec
		decode := func(_ context.Context, _ string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
			observed = append(observed, spec)
			return consume(bytes.NewReader(nil))
		}
		_, _, err := ReadSegments(context.Background(), decode, "fixture", kind, mode, seconds*1e6, true, true, decoder.PreviewVideoFacts{}, nil, policy)
		if err != nil {
			t.Fatal(err)
		}
		return observed
	}
	usual := defaultSegmentPlanning()
	long := usual
	long.IntroWindow, long.CreditsWindow, long.FilmCreditsWindow = "long", "long", "long"
	for _, tc := range []struct {
		kind, mode string
		seconds    int64
		policy     SegmentPlanningOptions
		lengths    []int64
	}{
		{"episode", "standard", 1440, usual, []int64{360, 480}},
		{"episode", "standard", 1440, long, []int64{720, 900}},
		{"movie", "thorough", 3600, usual, []int64{900}},
		{"movie", "thorough", 3600, long, []int64{1800}},
	} {
		got := read(tc.kind, tc.mode, tc.seconds, tc.policy)
		if len(got) != len(tc.lengths) {
			t.Fatal(tc, got)
		}
		for i, length := range tc.lengths {
			if got[i].RegionUS != length*1e6 {
				t.Fatal(tc, got)
			}
		}
	}
	minimum := usual
	minimum.EpisodeMinutes, minimum.FilmMinutes = 30, 60
	if got := read("episode", "standard", 1440, minimum); len(got) != 0 {
		t.Fatal("a short episode was still decoded", got)
	}
	if got := read("movie", "thorough", 1800, minimum); len(got) != 0 {
		t.Fatal("a short film was still decoded", got)
	}
}
