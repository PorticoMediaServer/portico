package catalog

import (
	"context"
	"encoding/binary"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Occurrences contain only internal coordinates. Public IDs, paths, labels and
// technical scalars hydrate after ready; none enter the shared payload.
type fileInfoOccurrence struct{ Item, Asset int64 }
type fileInfoOrderedRow struct {
	Occurrence      fileInfoOccurrence
	Sequence        int64
	Match, Eligible bool
}

// Compare performs the exact lane order on two point keys, keeping scalar sort
// values inside SQLite. Next advances one raw indexed row, including rejects or
// empty-child events. Each comparison is also a separately budgeted operation.
type fileInfoOrderedSource interface {
	AccountedBytes() int64
	Check(context.Context) error
	Next(context.Context) (fileInfoOrderedRow, bool, error)
	Compare(context.Context, fileInfoOccurrence, fileInfoOccurrence) (int, error)
}

type fileInfoOrderedSpec struct {
	Limit     int
	Anchor    fileInfoOccurrence
	HasAnchor bool
	Direction byte
}
type fileInfoOrderedSummary struct {
	Total, Eligible int64
	// FirstOrdinal is the 1-based global ordinal of Occurrences[0] in the whole
	// accepted lane order, or 0 for an empty page. It lets delivery place a short
	// or backward page without re-walking, sorting or retaining pages.
	FirstOrdinal   int64
	Earlier, Later bool
	Occurrences    []fileInfoOccurrence
}

type fileInfoOrderedScan struct {
	source                                    fileInfoOrderedSource
	spec                                      fileInfoOrderedSpec
	bytes                                     int64
	sequence, total, eligible, window, before int64
	found                                     bool
	items                                     [101]fileInfoOccurrence
	count                                     int
	pending                                   fileInfoOccurrence
	stage, lo, hi                             int // 0 raw, 1 anchor comparison, 2 bounded insertion search
}

const fileInfoOrderedScanBytes int64 = 16384

func fileInfoOrderedPlan(source fileInfoOrderedSource, spec fileInfoOrderedSpec) (readprep.Plan, error) {
	if source == nil || source.AccountedBytes() < 0 || source.AccountedBytes() > math.MaxInt64-fileInfoOrderedScanBytes || spec.Limit < 1 || spec.Limit > 100 || spec.Direction < fileInfoForward || spec.Direction > fileInfoReload || (!spec.HasAnchor && spec.Direction != fileInfoForward) || spec.HasAnchor && (spec.Anchor.Item <= 0 || spec.Anchor.Asset <= 0) {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	bytes := fileInfoOrderedScanBytes + source.AccountedBytes()
	return readprep.Plan{InitialBytes: bytes, New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(bytes); err != nil {
			return nil, err
		}
		if err := source.Check(ctx); err != nil {
			return nil, err
		}
		return &fileInfoOrderedScan{source: source, spec: spec, bytes: bytes}, nil
	}}, nil
}
func (s *fileInfoOrderedScan) AccountedBytes() int64 { return s.bytes }
func (s *fileInfoOrderedScan) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if err := b.Resize(s.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	if err := s.source.Check(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	started := time.Now()
	done := false
	for op := 0; op < 128; op++ {
		if err := ctx.Err(); err != nil {
			return readprep.StepResult{}, err
		}
		switch s.stage {
		case 0:
			row, exists, err := s.source.Next(ctx)
			if err != nil {
				return readprep.StepResult{}, err
			}
			if !exists {
				done = true
				break
			}
			if row.Sequence <= s.sequence {
				return readprep.StepResult{}, readprep.ErrInvalid
			}
			s.sequence = row.Sequence
			if row.Match {
				if row.Occurrence.Item <= 0 || row.Occurrence.Asset <= 0 || s.total == math.MaxInt64 {
					return readprep.StepResult{}, readprep.ErrInvalid
				}
				s.total++
				if row.Eligible {
					s.eligible++
				}
				s.pending = row.Occurrence
				if s.spec.HasAnchor {
					s.stage = 1
				} else {
					s.window++
					s.stage, s.lo, s.hi = 2, 0, s.count
				}
			}
		case 1:
			cmp, err := s.source.Compare(ctx, s.pending, s.spec.Anchor)
			if err != nil {
				return readprep.StepResult{}, err
			}
			if cmp == 0 {
				if s.pending != s.spec.Anchor {
					return readprep.StepResult{}, readprep.ErrInvalid
				}
				s.found = true
			}
			if cmp < 0 {
				s.before++
			}
			keep := cmp > 0
			if s.spec.Direction == fileInfoBackward {
				keep = cmp < 0
			} else if s.spec.Direction == fileInfoReload {
				keep = cmp >= 0
			}
			if keep {
				s.window++
				s.stage, s.lo, s.hi = 2, 0, s.count
			} else {
				s.stage = 0
			}
		case 2:
			if s.lo < s.hi {
				mid := s.lo + (s.hi-s.lo)/2
				cmp, err := s.source.Compare(ctx, s.pending, s.items[mid])
				if err != nil {
					return readprep.StepResult{}, err
				}
				if cmp == 0 {
					return readprep.StepResult{}, readprep.ErrInvalid
				}
				if cmp < 0 {
					s.hi = mid
				} else {
					s.lo = mid + 1
				}
			} else {
				copy(s.items[s.lo+1:s.count+1], s.items[s.lo:s.count])
				s.items[s.lo] = s.pending
				s.count++
				if s.count > s.spec.Limit {
					if s.spec.Direction == fileInfoBackward {
						copy(s.items[:s.count-1], s.items[1:s.count])
					}
					s.count--
				}
				s.stage = 0
			}
		default:
			return readprep.StepResult{}, readprep.ErrInvalid
		}
		if done || time.Since(started) >= 8*time.Millisecond {
			break
		}
	}
	if err := s.source.Check(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	if !done {
		return readprep.StepResult{}, nil
	}
	if s.spec.HasAnchor && !s.found {
		return readprep.StepResult{}, readprep.ErrStale
	}
	earlier, later := false, false
	switch s.spec.Direction {
	case fileInfoBackward:
		earlier = s.window > int64(s.count)
		later = s.total > s.before
	case fileInfoReload:
		earlier = s.before > 0
		later = s.window > int64(s.count)
	default:
		earlier = s.spec.HasAnchor && s.total > s.window
		later = s.window > int64(s.count)
	}
	// `before` counts accepted rows strictly before the anchor, which fixes the
	// page's first global ordinal without a sort or a resident page.
	first := int64(0)
	if s.count > 0 {
		switch s.spec.Direction {
		case fileInfoBackward:
			first = s.before - int64(s.count) + 1
		case fileInfoReload:
			first = s.before + 1
		default:
			if s.spec.HasAnchor {
				first = s.before + 2
			} else {
				first = 1
			}
		}
	}
	payload := make([]byte, 28+16*s.count)
	payload[0], payload[1] = 2, byte(s.count)
	if earlier {
		payload[2] = 1
	}
	if later {
		payload[3] = 1
	}
	binary.BigEndian.PutUint64(payload[4:12], uint64(s.total))
	binary.BigEndian.PutUint64(payload[12:20], uint64(s.eligible))
	binary.BigEndian.PutUint64(payload[20:28], uint64(first))
	for i, o := range s.items[:s.count] {
		binary.BigEndian.PutUint64(payload[28+16*i:36+16*i], uint64(o.Item))
		binary.BigEndian.PutUint64(payload[36+16*i:44+16*i], uint64(o.Asset))
	}
	return readprep.StepResult{Done: true, Payload: payload}, nil
}

func decodeFileInfoOrderedSummary(payload []byte) (fileInfoOrderedSummary, error) {
	var out fileInfoOrderedSummary
	if len(payload) < 28 || payload[0] != 2 || payload[1] > 100 || payload[2] > 1 || payload[3] > 1 || len(payload) != 28+16*int(payload[1]) {
		return out, readprep.ErrInvalid
	}
	out.Total = int64(binary.BigEndian.Uint64(payload[4:12]))
	out.Eligible = int64(binary.BigEndian.Uint64(payload[12:20]))
	out.FirstOrdinal = int64(binary.BigEndian.Uint64(payload[20:28]))
	out.Earlier, out.Later = payload[2] == 1, payload[3] == 1
	if out.Eligible < 0 || out.Eligible > out.Total || !fileInfoOrdinalRangeOK(out.FirstOrdinal, int64(payload[1]), out.Total) {
		return fileInfoOrderedSummary{}, readprep.ErrInvalid
	}
	// Validate bounded coordinates and duplicates before allocating the delivery.
	for i := 0; i < int(payload[1]); i++ {
		item := int64(binary.BigEndian.Uint64(payload[28+16*i : 36+16*i]))
		asset := int64(binary.BigEndian.Uint64(payload[36+16*i : 44+16*i]))
		if item <= 0 || asset <= 0 {
			return fileInfoOrderedSummary{}, readprep.ErrInvalid
		}
		for j := 0; j < i; j++ {
			if binary.BigEndian.Uint64(payload[28+16*j:36+16*j]) == uint64(item) && binary.BigEndian.Uint64(payload[36+16*j:44+16*j]) == uint64(asset) {
				return fileInfoOrderedSummary{}, readprep.ErrInvalid
			}
		}
	}
	out.Occurrences = make([]fileInfoOccurrence, int(payload[1]))
	for i := range out.Occurrences {
		out.Occurrences[i] = fileInfoOccurrence{int64(binary.BigEndian.Uint64(payload[28+16*i : 36+16*i])), int64(binary.BigEndian.Uint64(payload[36+16*i : 44+16*i]))}
	}
	return out, nil
}
