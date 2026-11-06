package writer

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func wcTestPlayingProof() wcPlayingProof {
	return wcPlayingProof{playbackID: "playback-one", occurrenceGeneration: 1, reportSequence: 0,
		activityGeneration: 1, rememberGeneration: 1, clockIncarnation: [16]byte{1}}
}

func wcTestPlayingInput(spans ...wcPlayingInterval) []wcPlayingInput {
	out := make([]wcPlayingInput, len(spans))
	for i, span := range spans {
		out[i] = wcPlayingInput{wcTestPlayingProof(), span}
	}
	return out
}

func TestWCPlayingUnionPreservesExactGeometryAndOwnsInput(t *testing.T) {
	raw := wcTestPlayingInput(wcPlayingInterval{30, 40}, wcPlayingInterval{0, 10},
		wcPlayingInterval{5, 15}, wcPlayingInterval{0, 10}, wcPlayingInterval{15, 20}, wcPlayingInterval{21, 25})
	before := slices.Clone(raw)
	prepared, err := prepareWCPlaying(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []wcPlayingInterval{{0, 20}, {21, 25}, {30, 40}}
	if !reflect.DeepEqual(prepared.intervals, want) || !reflect.DeepEqual(raw, before) {
		t.Fatalf("union=%v input mutated=%v", prepared.intervals, !reflect.DeepEqual(raw, before))
	}
	for i := range raw {
		raw[i] = wcPlayingInput{}
	}
	if !reflect.DeepEqual(prepared.intervals, want) || prepared.proof != wcTestPlayingProof() {
		t.Fatal("preparation retained mutable caller storage")
	}
}

func TestWCPlayingDoesNotCombineIndependentProofs(t *testing.T) {
	changes := []struct {
		name string
		edit func(*wcPlayingProof)
	}{
		{"occurrence", func(p *wcPlayingProof) { p.occurrenceGeneration++ }},
		{"playback", func(p *wcPlayingProof) { p.playbackID = "other-device" }},
		{"report", func(p *wcPlayingProof) { p.reportSequence++ }},
		{"activity_epoch", func(p *wcPlayingProof) { p.activityGeneration++ }},
		{"remember_epoch", func(p *wcPlayingProof) { p.rememberGeneration++ }},
		{"clock_reanchor", func(p *wcPlayingProof) { p.clockIncarnation[1]++ }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			raw := wcTestPlayingInput(wcPlayingInterval{1000, 2000}, wcPlayingInterval{1000, 2000})
			tc.edit(&raw[1].proof)
			if p, err := prepareWCPlaying(raw); p != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("mixed proof accepted", err)
			}
			// Independently accepted occurrences remain additive. A caller must
			// not pass these through a wall-time union shared by both proofs.
			var total int64
			for i := range raw {
				p, err := prepareWCPlaying(raw[i : i+1])
				if err != nil {
					t.Fatal(err)
				}
				piece, err := p.pieceAt(0, 1000)
				if err != nil {
					t.Fatal(err)
				}
				total += piece.untilMS - piece.fromMS
			}
			if total != 2000 {
				t.Fatalf("independent contributions=%d, want 2000", total)
			}
		})
	}
}

func TestWCPlayingRawPreparationBoundAndInvalidInput(t *testing.T) {
	if wcMaxPlayingIntervals != 256 {
		t.Fatal("review the preparation recipe and its boundary controls")
	}
	raw := slices.Repeat(wcTestPlayingInput(wcPlayingInterval{0, 1}), wcMaxPlayingIntervals)
	if p, err := prepareWCPlaying(raw); err != nil || len(p.intervals) != 1 {
		t.Fatal("raw boundary refused", err)
	}
	raw = append(raw, wcPlayingInput{}) // must refuse BEFORE inspecting/deduplicating
	if p, err := prepareWCPlaying(raw); p != nil || !errors.Is(err, ErrCap) {
		t.Fatal("oversized duplicate capture not refused", err)
	}
	for _, spans := range [][]wcPlayingInterval{nil, {{-1, 1}}, {{0, 0}}, {{2, 1}}, {{0, 1}, {4, 3}}} {
		if p, err := prepareWCPlaying(wcTestPlayingInput(spans...)); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid interval accepted", err)
		}
	}
	invalid := []func(*wcPlayingProof){
		func(p *wcPlayingProof) { p.playbackID = "" },
		func(p *wcPlayingProof) { p.playbackID = strings.Repeat("x", wcMaxPlaybackIDBytes+1) },
		func(p *wcPlayingProof) { p.playbackID = "private\x00value" },
		func(p *wcPlayingProof) { p.playbackID = "\xff" },
		func(p *wcPlayingProof) { p.occurrenceGeneration = 0 },
		func(p *wcPlayingProof) { p.reportSequence = -1 },
		func(p *wcPlayingProof) { p.activityGeneration = 0 },
		func(p *wcPlayingProof) { p.rememberGeneration = 0 },
		func(p *wcPlayingProof) { p.clockIncarnation = [16]byte{} },
	}
	for _, edit := range invalid {
		raw := wcTestPlayingInput(wcPlayingInterval{0, 1})
		edit(&raw[0].proof)
		if p, err := prepareWCPlaying(raw); p != nil || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid proof accepted or echoed", err)
		}
	}
}

func TestWCPlayingSingleQuarterDrainAndInt64Edges(t *testing.T) {
	for _, span := range []wcPlayingInterval{{0, 1}, {0, wcQuarterMS}, {wcQuarterMS - 1, wcQuarterMS + 1},
		{100, 12*60*60*1000 + 100}, {math.MaxInt64 - 1, math.MaxInt64}, {math.MaxInt64 - wcQuarterMS, math.MaxInt64}} {
		p, err := prepareWCPlaying(wcTestPlayingInput(span))
		if err != nil {
			t.Fatal(err)
		}
		cursor, count, total := span.fromMS, 0, int64(0)
		for cursor < span.untilMS {
			piece, err := p.pieceAt(0, cursor)
			if err != nil || piece.fromMS != cursor || piece.untilMS <= cursor || piece.untilMS > span.untilMS ||
				piece.fromMS/wcQuarterMS != (piece.untilMS-1)/wcQuarterMS {
				t.Fatalf("invalid quarter piece %+v: %v", piece, err)
			}
			total += piece.untilMS - piece.fromMS
			cursor = piece.untilMS
			count++
		}
		if total != span.untilMS-span.fromMS || (span.fromMS == 100 && count != 49) || len(p.intervals) != 1 {
			t.Fatalf("drain lost time/expanded descriptor: total=%d count=%d", total, count)
		}
		for _, bad := range []int64{span.fromMS - 1, span.untilMS} {
			if _, err := p.pieceAt(0, bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid pending cursor accepted", err)
			}
		}
	}
	// Even a full int64-length interval stays one descriptor with ONE result.
	p, err := prepareWCPlaying(wcTestPlayingInput(wcPlayingInterval{0, math.MaxInt64}))
	if err != nil {
		t.Fatal(err)
	}
	piece, err := p.pieceAt(0, math.MaxInt64-1)
	if err != nil || piece != (wcPlayingInterval{math.MaxInt64 - 1, math.MaxInt64}) || len(p.intervals) != 1 {
		t.Fatal("overflow or eager quarter expansion", piece, err)
	}
	for _, invalid := range []*wcPreparedPlaying{nil, {}, {valid: true}} {
		if _, err := invalid.pieceAt(0, 0); !errors.Is(err, ErrInvalid) {
			t.Fatal("zero preparation accepted", err)
		}
	}
	for _, index := range []int{-1, 1} {
		if _, err := p.pieceAt(index, 0); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid interval index accepted", err)
		}
	}
}

func TestWCPlayingFractionalWindowEdges(t *testing.T) {
	// R24's historical fractional-timezone boundary: a report window may
	// begin at UTC 00:44:30 inside a quarter. Equal coarse totals must retain
	// their different exact placement, including a span crossing that edge.
	const edge int64 = (44*60 + 30) * 1000
	for _, tc := range []struct {
		span wcPlayingInterval
		want int64
	}{
		{wcPlayingInterval{edge - 1000, edge}, 0},
		{wcPlayingInterval{edge, edge + 1000}, 1000},
		{wcPlayingInterval{edge - 500, edge + 500}, 500},
	} {
		p, err := prepareWCPlaying(wcTestPlayingInput(tc.span, tc.span))
		if err != nil {
			t.Fatal(err)
		}
		var clipped int64
		for cursor := tc.span.fromMS; cursor < tc.span.untilMS; {
			piece, err := p.pieceAt(0, cursor)
			if err != nil {
				t.Fatal(err)
			}
			clipped += max(int64(0), min(piece.untilMS, 45*60*1000)-max(piece.fromMS, edge))
			cursor = piece.untilMS
		}
		if clipped != tc.want {
			t.Fatalf("fine edge got %d, want %d", clipped, tc.want)
		}
	}
}

func TestWCPlayingUnionMatchesIndependentCoverageOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := range 500 {
		raw := make([]wcPlayingInput, 1+rng.Intn(32))
		var expected [128]bool
		for i := range raw {
			from := rng.Intn(len(expected))
			until := from + 1 + rng.Intn(len(expected)-from)
			raw[i] = wcPlayingInput{wcTestPlayingProof(), wcPlayingInterval{int64(from), int64(until)}}
			for at := from; at < until; at++ {
				expected[at] = true
			}
		}
		p, err := prepareWCPlaying(raw)
		if err != nil {
			t.Fatal(err)
		}
		var actual [128]bool
		for i, span := range p.intervals {
			if i > 0 && p.intervals[i-1].untilMS >= span.fromMS {
				t.Fatal("union not disjoint/canonical")
			}
			for at := span.fromMS; at < span.untilMS; at++ {
				actual[at] = true
			}
		}
		if actual != expected {
			t.Fatalf("playing gap filled or proved time lost on trial %d", trial)
		}
	}
}
