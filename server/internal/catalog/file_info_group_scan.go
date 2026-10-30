package catalog

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Informational group types mirror the exact Options directory: one global
// composite when every offered part is distinct, one composite per multi-file
// distinct-part shape, otherwise one singleton per offered file.
const (
	fileInfoGroupSingleton byte = iota + 1
	fileInfoGroupGlobalComposite
	fileInfoGroupShapeComposite
)

// The private group wire frame is one header and fixed 74-byte records:
// version(1) count(1) flags(1) reserved(1) files(8) eligible(8) total(8)
// first(8), then for each group the seven original low words (56), the group
// type (1), the signed size high word (8), the signed duration high word (8)
// and the 0/1 duration-bad flag (1).
const (
	fileInfoGroupWireVersion byte = 3
	fileInfoGroupRecordBytes      = 74
)

// fileInfoGroupNumeric is the fixed-size directory coordinate. ShapeHead is the
// shape's first BestVersionOrder head; Head is this group's original offered
// head even when that head is not displayed. OfferedMembers counts every
// offered file in the group, Members only those whose link.available survives
// the display intersection. SizeBytes and DurationMS hold the signed low words
// of exact 128-bit sums; SizeHigh and DurationHigh hold the signed high words,
// so the exact value is low + high*2^64. DurationBad marks a group whose
// duration total cannot be represented because at least one raw seconds->ms
// conversion was not finite or was outside int64. No label, path, edition or
// scalar sort key is held here; those hydrate transiently after ready.
type fileInfoGroupNumeric struct {
	ShapeHead, Head, Shape, OfferedMembers, Members, SizeBytes, DurationMS int64
	SizeHigh, DurationHigh                                                 int64
	Type                                                                   byte
	DurationBad                                                            bool
}

// fileInfoGroupFixedSum is a signed 128-bit two's-complement accumulator for
// exact size and duration totals. The exact value is Low + High*2^64. A carry
// out of the low word increments High and a borrow decrements it, which is the
// ordinary signed 128-bit addition rule. The high word cannot itself overflow:
// at most MaxInt64 rows, each an int64, bound the magnitude below 2^126. No big
// integer is allocated anywhere in the reducer.
type fileInfoGroupFixedSum struct {
	Low, High int64
}

func (s *fileInfoGroupFixedSum) Add(v int64) {
	low := s.Low + v
	switch {
	case v > 0 && low < s.Low:
		s.High++
	case v < 0 && low > s.Low:
		s.High--
	}
	s.Low = low
}

type fileInfoGroupSummary struct {
	Files, Eligible, Total, FirstOrdinal int64
	Earlier, Later, CanSplit             bool
	Groups                               []fileInfoGroupNumeric
}

// fileInfoGroupDescriptorOK is the single acceptance rule for one numeric group
// descriptor. It is used by the summary decoder and by the member lane, so a
// group the reducer would not have emitted can never be handed to a walk. The
// two composite types keep the original offered head as both ShapeHead and Head.
// A singleton is exactly one offered file; its own Head may be a later clustered
// head and need not equal the shape's first offered ShapeHead.
func fileInfoGroupDescriptorOK(g fileInfoGroupNumeric) bool {
	if g.ShapeHead <= 0 || g.Head <= 0 || g.Shape <= 0 || g.OfferedMembers < 1 || g.Members < 1 || g.Members > g.OfferedMembers {
		return false
	}
	switch g.Type {
	case fileInfoGroupSingleton:
		return g.OfferedMembers == 1 && g.Members == 1
	case fileInfoGroupGlobalComposite, fileInfoGroupShapeComposite:
		return g.Head == g.ShapeHead && g.OfferedMembers >= 2
	}
	return false
}

type fileInfoGroupSpec struct {
	Limit     int
	Anchor    fileInfoOccurrence
	HasAnchor bool
	Direction byte
}

const (
	fileInfoGroupStagePass1 = iota
	fileInfoGroupStagePass2
	fileInfoGroupStageFinishShape
	fileInfoGroupStageReplay
	fileInfoGroupStageEmit
	fileInfoGroupStageDone
)

// The fixed charge covers the factory descriptor, source adapter bookkeeping,
// the 101-slot bounded insertion array, the reducer, and the encoder payload.
// It never scales with F, with the number of shapes, or with labels/strings.
const fileInfoGroupScanBytes int64 = 64 << 10

// One adapter call is one budgeted operation: a raw row, a file point
// comparison, or a group point comparison. A group comparison may resolve its
// shape head and then its head inside the same bounded call, but never walks a
// variable number of rows. A step may charge a raw row plus its head comparison,
// so the loop reserves the final operation and stays at or below 128.
const (
	fileInfoGroupStepOps     = 128
	fileInfoGroupStepCeiling = fileInfoGroupStepOps - 1
	fileInfoGroupCompareOps  = 1
)

type fileInfoGroupScan struct {
	source                  fileInfoGroupSource
	spec                    fileInfoGroupSpec
	canManage, canSplitKind bool
	bytes                   int64
	stage                   int
	resume                  int

	// First pass: all raw links in (part,asset) order.
	partKey                             fileInfoGroupKey
	hasPart                             bool
	rawFiles, offered, eligible         int64
	sizeTotal, durationTotal            fileInfoGroupFixedSum
	globalDurationBad                   bool
	previousOfferedPart                 int64
	hasPreviousOfferedPart              bool
	globalDistinct                      bool
	globalPart, globalHead, globalShape int64
	hasGlobalHead                       bool

	// Second pass: all raw links in (shape,part,asset) order.
	shapeKey                    fileInfoGroupKey
	hasShape                    bool
	currentShape                int64
	hasCurrent                  bool
	shapeHead, shapeHeadPart    int64
	hasShapeHead                bool
	shapeOffered, shapeEligible int64
	shapeSize, shapeDuration    fileInfoGroupFixedSum
	shapeDurationBad            bool
	shapeDistinct               bool
	shapePreviousPart           int64
	hasShapePreviousPart        bool
	pending                     fileInfoGroupRow
	hasPending                  bool

	// One-shape replay for duplicate-part shapes.
	replayShape int64
	replayKey   fileInfoGroupKey
	hasReplay   bool

	// Bounded directory insertion, ordered by (shape head, group head).
	total, before, window int64
	found                 bool
	anyMemberOne          bool
	items                 [101]fileInfoGroupNumeric
	count                 int
	cand                  fileInfoGroupNumeric
	hasCand               bool
	insertStage           int
	insertLo, insertHi    int
}

func fileInfoGroupPlan(db *sql.DB, item int64, canManage, canSplitKind bool, spec fileInfoGroupSpec, captureBytes int64, check fileInfoBasisCheck) (readprep.Plan, error) {
	source, err := newFileInfoGroupSource(db, item, captureBytes, check)
	if err != nil {
		return readprep.Plan{}, err
	}
	return fileInfoGroupPlanSource(source, canManage, canSplitKind, spec)
}

func fileInfoGroupPlanSource(source fileInfoGroupSource, canManage, canSplitKind bool, spec fileInfoGroupSpec) (readprep.Plan, error) {
	if source == nil || source.AccountedBytes() < 0 || source.AccountedBytes() > math.MaxInt64-fileInfoGroupScanBytes ||
		spec.Limit < 1 || spec.Limit > 100 || spec.Direction < fileInfoForward || spec.Direction > fileInfoReload ||
		!spec.HasAnchor && spec.Direction != fileInfoForward ||
		spec.HasAnchor && (spec.Anchor.Item <= 0 || spec.Anchor.Asset <= 0) {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	bytes := fileInfoGroupScanBytes + source.AccountedBytes()
	return readprep.Plan{InitialBytes: bytes, New: func(ctx context.Context, budget *readprep.Budget) (readprep.Computation, error) {
		if err := budget.Resize(bytes); err != nil {
			return nil, err
		}
		if err := source.Check(ctx); err != nil {
			return nil, err
		}
		return &fileInfoGroupScan{
			source: source, spec: spec, canManage: canManage, canSplitKind: canSplitKind, bytes: bytes,
			stage: fileInfoGroupStagePass1, globalDistinct: true, shapeDistinct: true,
		}, nil
	}}, nil
}

func (s *fileInfoGroupScan) AccountedBytes() int64 { return s.bytes }

func (s *fileInfoGroupScan) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	if err := budget.Resize(s.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	if err := s.source.Check(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	started := time.Now()
	for ops := 0; ops < fileInfoGroupStepCeiling; {
		if err := ctx.Err(); err != nil {
			return readprep.StepResult{}, err
		}
		cost, payload, err := s.step(ctx)
		if err != nil {
			return readprep.StepResult{}, err
		}
		if cost <= 0 {
			return readprep.StepResult{}, readprep.ErrInvalid
		}
		ops += cost
		if payload != nil {
			if err := s.source.Check(ctx); err != nil {
				return readprep.StepResult{}, err
			}
			return readprep.StepResult{Done: true, Payload: payload}, nil
		}
		if time.Since(started) >= 8*time.Millisecond {
			break
		}
	}
	if err := s.source.Check(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{}, nil
}

func (s *fileInfoGroupScan) step(ctx context.Context) (int, []byte, error) {
	switch s.stage {
	case fileInfoGroupStagePass1:
		return s.stepPass1(ctx)
	case fileInfoGroupStagePass2:
		return s.stepPass2(ctx)
	case fileInfoGroupStageFinishShape:
		return s.stepFinishShape()
	case fileInfoGroupStageReplay:
		return s.stepReplay(ctx)
	case fileInfoGroupStageEmit:
		return s.stepEmit(ctx)
	case fileInfoGroupStageDone:
		return s.stepDone()
	default:
		return 0, nil, readprep.ErrInvalid
	}
}

// First pass uses only the part-major link index. Every raw row is spent,
// including rejects. Offered-part duplicates become a single previous-part
// integer; the global head is one bounded point comparison per offered row in
// the minimum offered part, never an in-memory F array.
func (s *fileInfoGroupScan) stepPass1(ctx context.Context) (int, []byte, error) {
	row, exists, err := s.source.NextPart(ctx, s.partKey, s.hasPart)
	if err != nil {
		return 0, nil, err
	}
	if !exists {
		switch {
		case s.offered > 1 && s.globalDistinct:
			if s.eligible == 0 {
				s.stage = fileInfoGroupStageDone
				return 1, nil, nil
			}
			s.cand = fileInfoGroupNumeric{
				ShapeHead: s.globalHead, Head: s.globalHead, Shape: s.globalShape,
				OfferedMembers: s.offered, Members: s.eligible,
				SizeBytes: s.sizeTotal.Low, SizeHigh: s.sizeTotal.High,
				DurationMS: s.durationTotal.Low, DurationHigh: s.durationTotal.High,
				DurationBad: s.globalDurationBad, Type: fileInfoGroupGlobalComposite,
			}
			s.hasCand, s.insertStage, s.resume = true, 0, fileInfoGroupStageDone
			s.stage = fileInfoGroupStageEmit
		case s.offered == 0:
			s.stage = fileInfoGroupStageDone
		default:
			s.stage = fileInfoGroupStagePass2
		}
		return 1, nil, nil
	}
	if s.hasPart && (row.Part < s.partKey.Part || row.Part == s.partKey.Part && row.Asset <= s.partKey.Asset) {
		return 0, nil, readprep.ErrInvalid
	}
	s.partKey, s.hasPart = fileInfoGroupKey{Shape: row.Shape, Part: row.Part, Asset: row.Asset}, true
	if row.Asset <= 0 || row.Shape <= 0 {
		return 0, nil, readprep.ErrStale
	}
	if s.rawFiles == math.MaxInt64 {
		return 0, nil, readprep.ErrInvalid
	}
	s.rawFiles++
	if !row.Offered {
		return 1, nil, nil
	}
	if s.offered == math.MaxInt64 || s.eligible == math.MaxInt64 {
		return 0, nil, readprep.ErrInvalid
	}
	s.offered++
	if row.Available {
		s.eligible++
	}
	s.sizeTotal.Add(row.Size)
	if milliseconds, ok := fileInfoGroupMilliseconds(row.Duration); ok {
		s.durationTotal.Add(milliseconds)
	} else {
		s.globalDurationBad = true
	}
	if s.hasPreviousOfferedPart && row.Part == s.previousOfferedPart {
		s.globalDistinct = false
	}
	s.previousOfferedPart, s.hasPreviousOfferedPart = row.Part, true
	cost := 1
	if !s.hasGlobalHead {
		s.globalPart, s.globalHead, s.globalShape, s.hasGlobalHead = row.Part, row.Asset, row.Shape, true
		return cost, nil, nil
	}
	if row.Part == s.globalPart {
		cmp, err := s.source.CompareFiles(ctx, row.Asset, s.globalHead)
		if err != nil {
			return 0, nil, err
		}
		if cmp == 0 {
			return 0, nil, readprep.ErrInvalid
		}
		if cmp < 0 {
			s.globalHead, s.globalShape = row.Asset, row.Shape
		}
		cost++
	}
	return cost, nil, nil
}

// Second pass walks one shape at a time. A lookahead row from the next shape is
// held as numeric coordinates until the current shape has been emitted or
// replayed, so no raw row is lost or read twice on account of the boundary.
func (s *fileInfoGroupScan) stepPass2(ctx context.Context) (int, []byte, error) {
	if s.hasPending {
		s.hasPending = false
		return s.processShapeRow(ctx, s.pending)
	}
	row, exists, err := s.source.NextShape(ctx, s.shapeKey, s.hasShape)
	if err != nil {
		return 0, nil, err
	}
	if !exists {
		if s.hasCurrent {
			s.stage = fileInfoGroupStageFinishShape
			return 1, nil, nil
		}
		s.stage = fileInfoGroupStageDone
		return 1, nil, nil
	}
	key := fileInfoGroupKey{Shape: row.Shape, Part: row.Part, Asset: row.Asset}
	if s.hasShape && !fileInfoGroupKeyAfter(key, s.shapeKey) {
		return 0, nil, readprep.ErrInvalid
	}
	s.shapeKey, s.hasShape = key, true
	if row.Asset <= 0 || row.Shape <= 0 {
		return 0, nil, readprep.ErrStale
	}
	if s.hasCurrent && row.Shape != s.currentShape {
		s.pending, s.hasPending = row, true
		s.stage = fileInfoGroupStageFinishShape
		return 1, nil, nil
	}
	return s.processShapeRow(ctx, row)
}

func (s *fileInfoGroupScan) processShapeRow(ctx context.Context, row fileInfoGroupRow) (int, []byte, error) {
	if !s.hasCurrent {
		s.clearShape()
		s.hasCurrent, s.currentShape = true, row.Shape
	}
	if row.Shape != s.currentShape {
		return 0, nil, readprep.ErrStale
	}
	s.stage = fileInfoGroupStagePass2
	if !row.Offered {
		return 1, nil, nil
	}
	if s.shapeOffered == math.MaxInt64 || s.shapeEligible == math.MaxInt64 {
		return 0, nil, readprep.ErrInvalid
	}
	s.shapeOffered++
	if row.Available {
		s.shapeEligible++
	}
	s.shapeSize.Add(row.Size)
	if milliseconds, ok := fileInfoGroupMilliseconds(row.Duration); ok {
		s.shapeDuration.Add(milliseconds)
	} else {
		s.shapeDurationBad = true
	}
	if s.hasShapePreviousPart && row.Part == s.shapePreviousPart {
		s.shapeDistinct = false
	}
	s.shapePreviousPart, s.hasShapePreviousPart = row.Part, true
	cost := 1
	if !s.hasShapeHead {
		s.shapeHead, s.shapeHeadPart, s.hasShapeHead = row.Asset, row.Part, true
		return cost, nil, nil
	}
	if row.Part == s.shapeHeadPart {
		cmp, err := s.source.CompareFiles(ctx, row.Asset, s.shapeHead)
		if err != nil {
			return 0, nil, err
		}
		if cmp == 0 {
			return 0, nil, readprep.ErrInvalid
		}
		if cmp < 0 {
			s.shapeHead = row.Asset
		}
		cost++
	}
	return cost, nil, nil
}

// A shape with distinct offered parts is one composite. Any other shape with
// more than one offered file is replayed once in raw order to emit its eligible
// singletons; the replay reads only that shape's index range.
func (s *fileInfoGroupScan) stepFinishShape() (int, []byte, error) {
	if s.hasCurrent {
		switch {
		case s.shapeOffered > 1 && s.shapeDistinct:
			if s.shapeEligible > 0 {
				s.cand = fileInfoGroupNumeric{
					ShapeHead: s.shapeHead, Head: s.shapeHead, Shape: s.currentShape,
					OfferedMembers: s.shapeOffered, Members: s.shapeEligible,
					SizeBytes: s.shapeSize.Low, SizeHigh: s.shapeSize.High,
					DurationMS: s.shapeDuration.Low, DurationHigh: s.shapeDuration.High,
					DurationBad: s.shapeDurationBad, Type: fileInfoGroupShapeComposite,
				}
				s.hasCand, s.insertStage, s.resume = true, 0, fileInfoGroupStagePass2
				s.clearShape()
				s.stage = fileInfoGroupStageEmit
				return 1, nil, nil
			}
		case s.shapeOffered == 1 && s.shapeEligible == 1:
			s.cand = fileInfoGroupNumeric{
				ShapeHead: s.shapeHead, Head: s.shapeHead, Shape: s.currentShape,
				OfferedMembers: 1, Members: 1,
				SizeBytes: s.shapeSize.Low, SizeHigh: s.shapeSize.High,
				DurationMS: s.shapeDuration.Low, DurationHigh: s.shapeDuration.High,
				DurationBad: s.shapeDurationBad, Type: fileInfoGroupSingleton,
			}
			s.hasCand, s.insertStage, s.resume = true, 0, fileInfoGroupStagePass2
			s.clearShape()
			s.stage = fileInfoGroupStageEmit
			return 1, nil, nil
		case s.shapeOffered > 1 && !s.shapeDistinct:
			s.replayShape, s.hasReplay = s.currentShape, false
			s.stage = fileInfoGroupStageReplay
			return 1, nil, nil
		}
		s.clearShape()
	}
	if s.hasPending {
		s.stage = fileInfoGroupStagePass2
		return 1, nil, nil
	}
	s.stage = fileInfoGroupStageDone
	return 1, nil, nil
}

func (s *fileInfoGroupScan) stepReplay(ctx context.Context) (int, []byte, error) {
	row, exists, err := s.source.NextShapeFile(ctx, s.replayShape, s.replayKey, s.hasReplay)
	if err != nil {
		return 0, nil, err
	}
	if !exists {
		s.hasReplay = false
		s.clearShape()
		s.stage = fileInfoGroupStagePass2
		return 1, nil, nil
	}
	key := fileInfoGroupKey{Shape: row.Shape, Part: row.Part, Asset: row.Asset}
	if row.Shape != s.replayShape {
		return 0, nil, readprep.ErrStale
	}
	if s.hasReplay && !fileInfoGroupKeyAfter(key, s.replayKey) {
		return 0, nil, readprep.ErrInvalid
	}
	s.replayKey, s.hasReplay = key, true
	if !row.Offered || !row.Available {
		return 1, nil, nil
	}
	var duration int64
	durationBad := false
	if milliseconds, ok := fileInfoGroupMilliseconds(row.Duration); ok {
		duration = milliseconds
	} else {
		durationBad = true
	}
	s.cand = fileInfoGroupNumeric{
		ShapeHead: s.shapeHead, Head: row.Asset, Shape: s.replayShape,
		OfferedMembers: 1, Members: 1, SizeBytes: row.Size,
		DurationMS: duration, DurationBad: durationBad, Type: fileInfoGroupSingleton,
	}
	s.hasCand, s.insertStage, s.resume = true, 0, fileInfoGroupStageReplay
	s.stage = fileInfoGroupStageEmit
	return 1, nil, nil
}

// Emission counts every surviving group, compares it with the signed anchor and
// inserts it into the fixed top-Limit array by resumable binary comparison. A
// group dropped by the display intersection never reaches this stage.
func (s *fileInfoGroupScan) stepEmit(ctx context.Context) (int, []byte, error) {
	if !s.hasCand {
		s.stage = s.resume
		return 1, nil, nil
	}
	switch s.insertStage {
	case 0:
		if s.total == math.MaxInt64 || s.before == math.MaxInt64 || s.window == math.MaxInt64 {
			return 0, nil, readprep.ErrInvalid
		}
		s.total++
		if s.cand.Members == 1 {
			s.anyMemberOne = true
		}
		cost := 1
		if s.spec.HasAnchor {
			cmp, err := s.source.CompareGroups(ctx, fileInfoGroupOccurrence(s.cand), s.spec.Anchor)
			if err != nil {
				return 0, nil, err
			}
			cost += fileInfoGroupCompareOps
			if cmp == 0 {
				if fileInfoGroupOccurrence(s.cand) != s.spec.Anchor {
					return 0, nil, readprep.ErrInvalid
				}
				s.found = true
			}
			if cmp < 0 {
				s.before++
			}
			keep := cmp > 0
			switch s.spec.Direction {
			case fileInfoBackward:
				keep = cmp < 0
			case fileInfoReload:
				keep = cmp >= 0
			}
			if !keep {
				s.hasCand = false
				s.stage = s.resume
				return cost, nil, nil
			}
			s.window++
		} else {
			s.window++
		}
		s.insertLo, s.insertHi, s.insertStage = 0, s.count, 1
		return cost, nil, nil
	case 1:
		if s.insertLo < s.insertHi {
			mid := s.insertLo + (s.insertHi-s.insertLo)/2
			cmp, err := s.source.CompareGroups(ctx, fileInfoGroupOccurrence(s.cand), fileInfoGroupOccurrence(s.items[mid]))
			if err != nil {
				return 0, nil, err
			}
			if cmp == 0 {
				return 0, nil, readprep.ErrInvalid
			}
			if cmp < 0 {
				s.insertHi = mid
			} else {
				s.insertLo = mid + 1
			}
			return fileInfoGroupCompareOps, nil, nil
		}
		s.insertStage = 2
		return 1, nil, nil
	case 2:
		copy(s.items[s.insertLo+1:s.count+1], s.items[s.insertLo:s.count])
		s.items[s.insertLo] = s.cand
		s.count++
		if s.count > s.spec.Limit {
			if s.spec.Direction == fileInfoBackward {
				copy(s.items[:s.count-1], s.items[1:s.count])
			}
			s.count--
		}
		s.hasCand = false
		s.stage = s.resume
		return 1, nil, nil
	default:
		return 0, nil, readprep.ErrInvalid
	}
}

func (s *fileInfoGroupScan) clearShape() {
	s.currentShape, s.hasCurrent = 0, false
	s.shapeHead, s.shapeHeadPart, s.hasShapeHead = 0, 0, false
	s.shapeOffered, s.shapeEligible = 0, 0
	s.shapeSize, s.shapeDuration = fileInfoGroupFixedSum{}, fileInfoGroupFixedSum{}
	s.shapeDurationBad = false
	s.shapeDistinct, s.shapePreviousPart, s.hasShapePreviousPart = true, 0, false
}

func (s *fileInfoGroupScan) stepDone() (int, []byte, error) {
	if s.spec.HasAnchor && !s.found {
		return 0, nil, readprep.ErrStale
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
	canSplit := s.canManage && s.canSplitKind && s.total >= 2 && s.anyMemberOne
	payload := make([]byte, 36+fileInfoGroupRecordBytes*s.count)
	payload[0], payload[1], payload[3] = fileInfoGroupWireVersion, byte(s.count), 0
	if earlier {
		payload[2] |= 1
	}
	if later {
		payload[2] |= 2
	}
	if canSplit {
		payload[2] |= 4
	}
	binary.BigEndian.PutUint64(payload[4:12], uint64(s.rawFiles))
	binary.BigEndian.PutUint64(payload[12:20], uint64(s.eligible))
	binary.BigEndian.PutUint64(payload[20:28], uint64(s.total))
	binary.BigEndian.PutUint64(payload[28:36], uint64(first))
	for i := 0; i < s.count; i++ {
		g, base := s.items[i], 36+fileInfoGroupRecordBytes*i
		binary.BigEndian.PutUint64(payload[base:base+8], uint64(g.ShapeHead))
		binary.BigEndian.PutUint64(payload[base+8:base+16], uint64(g.Head))
		binary.BigEndian.PutUint64(payload[base+16:base+24], uint64(g.Shape))
		binary.BigEndian.PutUint64(payload[base+24:base+32], uint64(g.OfferedMembers))
		binary.BigEndian.PutUint64(payload[base+32:base+40], uint64(g.Members))
		binary.BigEndian.PutUint64(payload[base+40:base+48], uint64(g.SizeBytes))
		binary.BigEndian.PutUint64(payload[base+48:base+56], uint64(g.DurationMS))
		payload[base+56] = g.Type
		binary.BigEndian.PutUint64(payload[base+57:base+65], uint64(g.SizeHigh))
		binary.BigEndian.PutUint64(payload[base+65:base+73], uint64(g.DurationHigh))
		if g.DurationBad {
			payload[base+73] = 1
		}
	}
	return 1, payload, nil
}

// The caller reserves <=100 fixed numeric descriptors before decoding. The
// version, length, flags, count, global counters and ordinal range are checked
// before the one bounded slice is allocated; each descriptor is then validated
// before it is published. The per-group record is 74 bytes: the seven original
// signed low words, the group type, the signed size and duration high words,
// and one 0/1 duration-bad flag. No user scalar is decoded. A negative low word
// is not by itself invalid because the high word may carry the exact value.
func decodeFileInfoGroupSummary(payload []byte) (fileInfoGroupSummary, error) {
	var out fileInfoGroupSummary
	if len(payload) < 36 || payload[0] != fileInfoGroupWireVersion || payload[1] > 100 || payload[2]&^7 != 0 || payload[3] != 0 ||
		len(payload) != 36+fileInfoGroupRecordBytes*int(payload[1]) {
		return out, readprep.ErrInvalid
	}
	out.Files = int64(binary.BigEndian.Uint64(payload[4:12]))
	out.Eligible = int64(binary.BigEndian.Uint64(payload[12:20]))
	out.Total = int64(binary.BigEndian.Uint64(payload[20:28]))
	out.FirstOrdinal = int64(binary.BigEndian.Uint64(payload[28:36]))
	out.Earlier, out.Later, out.CanSplit = payload[2]&1 != 0, payload[2]&2 != 0, payload[2]&4 != 0
	if out.Files < 0 || out.Eligible < 0 || out.Eligible > out.Files || out.Total < 0 || out.Total > out.Eligible ||
		!fileInfoOrdinalRangeOK(out.FirstOrdinal, int64(payload[1]), out.Total) {
		return fileInfoGroupSummary{}, readprep.ErrInvalid
	}
	out.Groups = make([]fileInfoGroupNumeric, int(payload[1]))
	for i := range out.Groups {
		base := 36 + fileInfoGroupRecordBytes*i
		if payload[base+73] > 1 {
			return fileInfoGroupSummary{}, readprep.ErrInvalid
		}
		g := fileInfoGroupNumeric{
			ShapeHead:      int64(binary.BigEndian.Uint64(payload[base : base+8])),
			Head:           int64(binary.BigEndian.Uint64(payload[base+8 : base+16])),
			Shape:          int64(binary.BigEndian.Uint64(payload[base+16 : base+24])),
			OfferedMembers: int64(binary.BigEndian.Uint64(payload[base+24 : base+32])),
			Members:        int64(binary.BigEndian.Uint64(payload[base+32 : base+40])),
			SizeBytes:      int64(binary.BigEndian.Uint64(payload[base+40 : base+48])),
			DurationMS:     int64(binary.BigEndian.Uint64(payload[base+48 : base+56])),
			Type:           payload[base+56],
			SizeHigh:       int64(binary.BigEndian.Uint64(payload[base+57 : base+65])),
			DurationHigh:   int64(binary.BigEndian.Uint64(payload[base+65 : base+73])),
			DurationBad:    payload[base+73] == 1,
		}
		if !fileInfoGroupDescriptorOK(g) {
			return fileInfoGroupSummary{}, readprep.ErrInvalid
		}
		for j := 0; j < i; j++ {
			if out.Groups[j].ShapeHead == g.ShapeHead && out.Groups[j].Head == g.Head {
				return fileInfoGroupSummary{}, readprep.ErrInvalid
			}
		}
		out.Groups[i] = g
	}
	return out, nil
}

func fileInfoGroupOccurrence(g fileInfoGroupNumeric) fileInfoOccurrence {
	return fileInfoOccurrence{Item: g.ShapeHead, Asset: g.Head}
}

func fileInfoGroupKeyAfter(a, b fileInfoGroupKey) bool {
	if a.Shape != b.Shape {
		return a.Shape > b.Shape
	}
	if a.Part != b.Part {
		return a.Part > b.Part
	}
	return a.Asset > b.Asset
}

// Per-part milliseconds are truncated in Go before summing, exactly as the
// Options oracle converts each part's seconds. A single group sum must not round
// an aggregate. The conversion succeeds only when the finite millisecond product
// lies inside the int64 range; NaN, either infinity or a larger finite product
// marks the row's group duration unrepresentable instead of failing the whole
// basis. The caller keeps the low word and the bad flag.
func fileInfoGroupMilliseconds(seconds float64) (int64, bool) {
	const minMilliseconds = float64(-1 << 63)
	const maxMilliseconds = float64(1 << 63)
	value := seconds * 1000
	if math.IsNaN(value) || value < minMilliseconds || value >= maxMilliseconds {
		return 0, false
	}
	return int64(value), true
}
