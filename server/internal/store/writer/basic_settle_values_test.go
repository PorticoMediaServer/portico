package writer

import (
	"database/sql"
	"errors"
	"math"
	"testing"
)

func TestBasicFirstLeafTechnicalRevisionPins(t *testing.T) {
	before := basicLeafTechnicalBefore{scalarRevision: 17, aggregateRevision: 23, membershipSequence: 89}
	got, err := projectBasicFirstLeafTechnical(before, basicFirstLinkTechnical{}, 120, 1080)
	if err != nil || got.duration != (sql.NullFloat64{Float64: 120, Valid: true}) || got.resolution != (sql.NullInt64{Int64: 1080, Valid: true}) || got.durationKey != 120 || got.resolutionKey != 1080 || got.scalarRevision != 18 || got.aggregateRevision != 24 || got.membershipSequence != 89 || !got.scalarChanged || !got.aggregateChanged {
		t.Fatal(got, err)
	}
	// A newly accepted missing-height value is real source0 evidence, while its
	// normalized sort0 was already published. Only its scalar source advances.
	before = basicLeafTechnicalBefore{duration: sql.NullFloat64{Float64: 240, Valid: true}, durationKey: 240, scalarRevision: 17, aggregateRevision: math.MaxInt64, membershipSequence: 89}
	got, err = projectBasicFirstLeafTechnical(before, basicFirstLinkTechnical{}, 120, 0)
	if err != nil || got.scalarRevision != 18 || got.aggregateRevision != math.MaxInt64 || got.aggregateChanged || !got.scalarChanged || got.durationKey != 240 || got.resolution != (sql.NullInt64{Int64: 0, Valid: true}) || got.membershipSequence != 89 {
		t.Fatal(got, err)
	}
}
func TestBasicFirstLeafTechnicalPreservesUnrelatedMaximaAndExhaustedClocks(t *testing.T) {
	before := basicLeafTechnicalBefore{duration: sql.NullFloat64{Float64: 240, Valid: true}, resolution: sql.NullInt64{Int64: 2160, Valid: true}, durationKey: 240, resolutionKey: 2160, scalarRevision: math.MaxInt64, aggregateRevision: math.MaxInt64, membershipSequence: 89}
	got, err := projectBasicFirstLeafTechnical(before, basicFirstLinkTechnical{}, 120, 1080)
	if err != nil || got.basicLeafTechnicalBefore != before || got.scalarChanged || got.aggregateChanged {
		t.Fatal(got, err)
	}
	for _, change := range []struct {
		duration float64
		height   int64
	}{{300, 1080}, {120, 4320}} {
		got, err = projectBasicFirstLeafTechnical(before, basicFirstLinkTechnical{}, change.duration, change.height)
		if !errors.Is(err, ErrCap) || got != (basicLeafTechnicalAfter{}) {
			t.Fatal(got, err)
		}
	}
}
func TestBasicFirstLeafTechnicalRefusesReplacementAndUnprovedReduction(t *testing.T) {
	before := basicLeafTechnicalBefore{scalarRevision: 17, aggregateRevision: 23, membershipSequence: 89}
	for _, link := range []basicFirstLinkTechnical{{shape: sql.NullInt64{Int64: 61, Valid: true}}, {duration: 100}} {
		got, err := projectBasicFirstLeafTechnical(before, link, 120, 1080)
		var refused *Refusal
		if !errors.As(err, &refused) || refused.Code != RefusalUnqualified || got != (basicLeafTechnicalAfter{}) {
			t.Fatal(got, err)
		}
	}
	before.durationKey = 100
	got, err := projectBasicFirstLeafTechnical(before, basicFirstLinkTechnical{}, 120, 1080)
	if !errors.Is(err, ErrConflict) || got != (basicLeafTechnicalAfter{}) {
		t.Fatal(got, err)
	}
}
