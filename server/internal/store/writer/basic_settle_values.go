package writer

import (
	"database/sql"
	"math"
)

// Inert native source0/leaf captures. A caller must prove the retained same-
// library leaf, sole source0 role and absence of external obligations in SQL.
// These values confer no permission or completion/publication authority.
type basicLeafTechnicalBefore struct {
	duration                                              sql.NullFloat64
	resolution                                            sql.NullInt64
	durationKey                                           float64
	resolutionKey                                         sql.NullInt64 // NULL is the legitimate unknown RB cohort
	scalarRevision, aggregateRevision, membershipSequence int64
}
type basicFirstLinkTechnical struct {
	shape    sql.NullInt64
	duration float64
}
type basicLeafTechnicalAfter struct {
	basicLeafTechnicalBefore
	scalarChanged, aggregateChanged bool
}

// FIRST is an additive acceptance of an as-yet-unaccepted native link. Its
// actual target may extend a MAX; this is not a maximum-removal/replacement
// recipe. An already accepted link requires that separate bounded protocol.
// INT #252: source0 changes bump only scalar_source_revision, indexed value
// changes bump only aggregate_revision; no membership is added or clock spent.
func projectBasicFirstLeafTechnical(before basicLeafTechnicalBefore, link basicFirstLinkTechnical, duration float64, height int64) (basicLeafTechnicalAfter, error) {
	if before.scalarRevision <= 0 || before.aggregateRevision <= 0 || before.membershipSequence <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || height < 0 || math.IsNaN(before.durationKey) || math.IsInf(before.durationKey, 0) {
		return basicLeafTechnicalAfter{}, ErrInvalid
	}
	if link.shape.Valid || link.shape.Int64 != 0 || link.duration != 0 {
		return basicLeafTechnicalAfter{}, &Refusal{Code: RefusalUnqualified}
	}
	if (before.duration.Valid && (math.IsNaN(before.duration.Float64) || math.IsInf(before.duration.Float64, 0))) || (!before.duration.Valid && before.duration.Float64 != 0) || (!before.resolution.Valid && before.resolution.Int64 != 0) {
		return basicLeafTechnicalAfter{}, ErrInvalid
	}
	expectedDuration := float64(0)
	if before.duration.Valid {
		expectedDuration = before.duration.Float64
	}
	expectedResolution := int64(0)
	if before.resolution.Valid {
		expectedResolution = before.resolution.Int64
	}
	// The entity key is either the stored scalar value or the 0 the fresh layer
	// writes for an absent scalar. A NULL key is the RB unknown cohort: it is
	// consistent only when the scalar is absent too, and it becomes a value on
	// this write.
	switch {
	case before.resolutionKey.Valid:
		if before.resolutionKey.Int64 != expectedResolution {
			return basicLeafTechnicalAfter{}, ErrConflict
		}
	case before.resolution.Valid:
		return basicLeafTechnicalAfter{}, ErrConflict
	}
	if before.durationKey != expectedDuration {
		return basicLeafTechnicalAfter{}, ErrConflict
	}
	next := basicLeafTechnicalAfter{basicLeafTechnicalBefore: before}
	if !next.duration.Valid || duration > next.duration.Float64 {
		next.duration = sql.NullFloat64{Float64: duration, Valid: true}
	}
	if !next.resolution.Valid || height > next.resolution.Int64 {
		next.resolution = sql.NullInt64{Int64: height, Valid: true}
	}
	next.durationKey = next.duration.Float64
	next.resolutionKey = sql.NullInt64{Int64: next.resolution.Int64, Valid: true}
	next.scalarChanged = next.duration != before.duration || next.resolution != before.resolution
	next.aggregateChanged = next.durationKey != before.durationKey || next.resolutionKey != before.resolutionKey
	if next.scalarChanged {
		if before.scalarRevision == math.MaxInt64 {
			return basicLeafTechnicalAfter{}, ErrCap
		}
		next.scalarRevision++
	}
	if next.aggregateChanged {
		if before.aggregateRevision == math.MaxInt64 {
			return basicLeafTechnicalAfter{}, ErrCap
		}
		next.aggregateRevision++
	}
	return next, nil
}
