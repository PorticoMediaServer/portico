package catalog

import (
	"context"
	"encoding/binary"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoIDSource visits a lane's raw integer primary key in strictly ascending
// order. Next must perform at most one indexed raw-row read: rejecting a row is
// represented by match=false, never by an unbounded WHERE/filter-before-LIMIT.
// Check validates the captured informational basis, not a library content clock.
// The adapter owns authority checks before and after delivery as well.
type fileInfoIDSource interface {
	AccountedBytes() int64
	Check(context.Context) error
	Next(context.Context, bool, int64) (id int64, match, exists bool, err error)
}

type fileInfoIDSpec struct {
	Limit     int
	Anchor    int64
	HasAnchor bool
	Direction byte
}

type fileInfoIDSummary struct {
	Total int64
	// FirstOrdinal is the 1-based global ordinal of IDs[0] within the whole
	// accepted lane, or 0 for an empty page. It lets delivery place a short or
	// backward page without re-walking or retaining resident pages.
	FirstOrdinal   int64
	Earlier, Later bool
	IDs            []int64
}

// This reducer is for integer-keyed independent lanes (for example stream
// indices). It does not substitute integer-ID order for BestVersionOrder on
// physical files. A file-order adapter needs its own exact ordered source.
// Only <=100 integer IDs and their temporary accepted ordinals are retained.
type fileInfoIDScan struct {
	source      fileInfoIDSource
	spec        fileInfoIDSpec
	bytes       int64
	after       int64
	hasAfter    bool
	found       bool
	total       int64
	count       int
	start       int
	ids         [100]int64
	ordinals    [100]int64
	left, right bool
}

// The charge covers the fixed arrays, factory descriptors, reducer, payload,
// and bounded driver bookkeeping in addition to the source's audited captures.
const fileInfoIDScanBytes int64 = 8192

func fileInfoIDPlan(source fileInfoIDSource, spec fileInfoIDSpec) (readprep.Plan, error) {
	if source == nil || source.AccountedBytes() < 0 || source.AccountedBytes() > math.MaxInt64-fileInfoIDScanBytes ||
		spec.Limit < 1 || spec.Limit > 100 || spec.Direction < fileInfoForward || spec.Direction > fileInfoReload {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	initial := fileInfoIDScanBytes + source.AccountedBytes()
	return readprep.Plan{InitialBytes: initial, New: func(ctx context.Context, budget *readprep.Budget) (readprep.Computation, error) {
		if err := budget.Resize(initial); err != nil {
			return nil, err
		}
		if err := source.Check(ctx); err != nil {
			return nil, err
		}
		return &fileInfoIDScan{source: source, spec: spec, bytes: initial}, nil
	}}, nil
}

func (s *fileInfoIDScan) AccountedBytes() int64 { return s.bytes }

func (s *fileInfoIDScan) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	if err := budget.Resize(s.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	if err := s.source.Check(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	started := time.Now()
	done := false
	for n := 0; n < 128; n++ {
		if err := ctx.Err(); err != nil {
			return readprep.StepResult{}, err
		}
		id, match, exists, err := s.source.Next(ctx, s.hasAfter, s.after)
		if err != nil {
			return readprep.StepResult{}, err
		}
		if !exists {
			done = true
			break
		}
		if s.hasAfter && id <= s.after {
			return readprep.StepResult{}, readprep.ErrInvalid
		}
		s.after, s.hasAfter = id, true
		if match {
			if s.total == math.MaxInt64 {
				return readprep.StepResult{}, readprep.ErrInvalid
			}
			s.total++
			// The anchor proves itself only by arriving as an accepted raw ID;
			// its position in the page is irrelevant.
			if s.spec.HasAnchor && id == s.spec.Anchor {
				s.found = true
			}
			s.keep(id)
		}
		// Every raw reject consumes a unit. Time is checked after every row;
		// EOF is established only by an actual empty raw read.
		if time.Since(started) >= 8*time.Millisecond {
			break
		}
	}
	if err := s.source.Check(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	if !done {
		return readprep.StepResult{}, nil
	}
	// A genuine EOF without the anchor proves the caller's cursor is not an
	// accepted ID in this lane, so the window is stale rather than empty.
	if s.spec.HasAnchor && !s.found {
		return readprep.StepResult{}, readprep.ErrStale
	}
	// Binary payload has a fixed bound, including nineteen-digit IDs/totals.
	// This allocation was reserved in the initial fixed charge, before New.
	first := int64(0)
	if s.count > 0 {
		first = s.ordinals[s.start]
	}
	payload := make([]byte, 20+8*s.count)
	payload[0] = 2
	payload[1] = byte(s.count)
	binary.BigEndian.PutUint64(payload[4:12], uint64(s.total))
	binary.BigEndian.PutUint64(payload[12:20], uint64(first))
	if s.count > 0 {
		s.left = s.ordinals[s.start] > 1
		last := (s.start + s.count - 1) % s.spec.Limit
		s.right = s.ordinals[last] < s.total
	}
	if s.left {
		payload[2] = 1
	}
	if s.right {
		payload[3] = 1
	}
	for i := 0; i < s.count; i++ {
		binary.BigEndian.PutUint64(payload[20+8*i:], uint64(s.ids[(s.start+i)%s.spec.Limit]))
	}
	return readprep.StepResult{Done: true, Payload: payload}, nil
}

func (s *fileInfoIDScan) keep(id int64) {
	eligible := true
	if s.spec.HasAnchor {
		switch s.spec.Direction {
		case fileInfoForward:
			eligible = id > s.spec.Anchor
		case fileInfoReload:
			eligible = id >= s.spec.Anchor
		case fileInfoBackward:
			eligible = id < s.spec.Anchor
		}
	}
	if !eligible {
		if id < s.spec.Anchor {
			s.left = true
		} else {
			s.right = true
		}
		return
	}
	if s.count < s.spec.Limit {
		position := (s.start + s.count) % s.spec.Limit
		s.ids[position], s.ordinals[position] = id, s.total
		s.count++
	} else if s.spec.Direction == fileInfoBackward {
		s.ids[s.start], s.ordinals[s.start] = id, s.total
		s.start = (s.start + 1) % s.spec.Limit
	}
}

// The caller reserves <=100 IDs in its delivery lease before decoding. Reject
// malformed versions, lengths and ordinal ranges before allocating; no
// user-controlled scalar is decoded.
func decodeFileInfoIDs(payload []byte) (fileInfoIDSummary, error) {
	var out fileInfoIDSummary
	if len(payload) < 20 || payload[0] != 2 || payload[1] > 100 || payload[2] > 1 || payload[3] > 1 || len(payload) != 20+8*int(payload[1]) {
		return out, readprep.ErrStale
	}
	out.Total = int64(binary.BigEndian.Uint64(payload[4:12]))
	out.FirstOrdinal = int64(binary.BigEndian.Uint64(payload[12:20]))
	if !fileInfoOrdinalRangeOK(out.FirstOrdinal, int64(payload[1]), out.Total) {
		return fileInfoIDSummary{}, readprep.ErrStale
	}
	out.Earlier, out.Later = payload[2] == 1, payload[3] == 1
	out.IDs = make([]int64, int(payload[1]))
	for i := range out.IDs {
		out.IDs[i] = int64(binary.BigEndian.Uint64(payload[20+8*i:]))
		if i > 0 && out.IDs[i] <= out.IDs[i-1] {
			return fileInfoIDSummary{}, readprep.ErrStale
		}
	}
	return out, nil
}

// fileInfoOrdinalRangeOK requires an empty page to carry no ordinal and a
// non-empty page to start inside the exact total it reports.
func fileInfoOrdinalRangeOK(first, count, total int64) bool {
	if first < 0 || total < count {
		return false
	}
	if count == 0 {
		return first == 0
	}
	return first >= 1 && first <= total-count+1
}
