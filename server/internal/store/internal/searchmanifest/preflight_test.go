package searchmanifest

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
)

func TestRawEnvelopePreflightOverflow(t *testing.T) {
	for _, tt := range []struct {
		fixed  int64
		values []string
		want   bool
	}{
		{-1, nil, false},
		{math.MaxInt64 - 8, []string{""}, true},
		{math.MaxInt64 - 9, []string{"a"}, true},
		{math.MaxInt64 - 8, []string{"a"}, false},
		{math.MaxInt64 - 7, []string{""}, false},
		{math.MaxInt64 - 16, []string{"", ""}, true},
		{math.MaxInt64 - 16, []string{"", "a"}, false},
	} {
		if got := encodedStringsFit(tt.fixed, tt.values...); got != tt.want {
			t.Fatalf("raw LP envelope overflow: %d, %q: %v", tt.fixed, tt.values, got)
		}
	}
}

func TestRefusedStreamsDoNotHashLargeColumns(t *testing.T) {
	// Allocate before measurement. Refusal must not allocate a conversion copy
	// of this already supplied column or hash it before discovering failure.
	large := strings.Repeat("x", 8<<20)
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"advance-byte-overflow", func() error {
			cp := Checkpoint{Count: 1, PreparedBytes: math.MaxInt64, AfterField: 1, AfterOrdinal: 0}
			next, err := Advance(cp, Row{Field: 1, Ordinal: 1, Revision: "native:\x009", Prefix: large})
			if next != (Checkpoint{}) {
				t.Fatal("refusal returned a partial checkpoint")
			}
			return err
		}},
		{"prepare-late-order-failure", func() error {
			in := Input{Subject: Person, SearchGenerationID: 1, InputGeneration: 1, SourceRevision: 1}
			m, err := Prepare(in, []Row{
				{Field: 1, Ordinal: 0, Prefix: large},
				{Field: 1, Ordinal: 2},
			})
			if m != (Manifest{}) {
				t.Fatal("refusal returned a partial manifest")
			}
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			err := tt.run()
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrManifest) {
				t.Fatalf("invalid complete stream accepted: %v", err)
			}
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<10 {
				t.Fatalf("refusal allocated %d bytes before rejecting an 8MiB column", allocated)
			}
		})
	}
}
