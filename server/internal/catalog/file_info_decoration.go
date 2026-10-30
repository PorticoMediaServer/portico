package catalog

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoDecoration is the fixed-size numeric decoration for one occurrence of
// a completed numeric page. Occurrence is the same private locator the payload
// names: for files, sources and members it is the child/item plus asset; for a
// group it is the root item plus that group's own head. Audio and Subtitles are
// raw row counts. VideoRow is the first raw video stream ROWID, or zero when the
// file has no video stream. No label, path, token, codec, title or JSON string is
// retained here or in the cache.
type fileInfoDecoration struct {
	Occurrence                 fileInfoOccurrence
	Audio, Subtitles, VideoRow int64
}

// fileInfoDecorationState is allocated only after the wrapped numeric plan (and
// the optional whole-surface walk) has completed. Keeping it behind a pointer
// means the underlying and surface stages perform no wrapper-owned array
// allocation at all: the two fixed 100-slot arrays exist only while the
// decoration rows are being resolved.
type fileInfoDecorationState struct {
	coords      [fileInfoDecorationLimit]fileInfoOccurrence
	decorations [fileInfoDecorationLimit]fileInfoDecoration
}

const (
	// The wrapper's fixed charge covers its own descriptor, the two 100-slot
	// arrays, the bounded decoded numeric summary, driver/encoder bookkeeping and
	// the small final frame. It never scales with the database or the page.
	fileInfoDecorationFixedBytes int64 = 32 << 10
	// One decoration record is five int64 fields. The final frame is the exact
	// numeric payload, one count byte, the 16-byte winner occurrence, then
	// exactly that many records.
	fileInfoDecorationRecordBytes = 40
	fileInfoDecorationLimit       = 100
	// One Step performs at most this many raw point/row statements. Every
	// statement is followed by the cooperative time check below.
	fileInfoDecorationStepOps = 128
)

const fileInfoDecorationStepTime = 8 * time.Millisecond

const (
	fileInfoDecorationStageUnderlying = iota
	fileInfoDecorationStageSurface
	fileInfoDecorationStageDecorate
	fileInfoDecorationStageDone
)

// fileInfoDecorationOptions adds the optional whole-surface owner-disc walk.
// Surface and DiscCheck are supplied together by the root for an owner and are
// both nil for a non-owner. Surface must be a fresh single-use ordered source
// over the complete accepted file surface (the original flat source with its
// ChildVisible callback for a files container, or the direct root source for a
// groups page). DiscCheck is the root's accepted owner-pin/facts point for one
// occurrence; it performs no decoration and returns only a boolean.
//
// The wrapper owns the surface only for one bounded walk between the numeric
// plan and per-head metadata. The walk is O(F) time and O(1) wrapper memory: it
// stores only one candidate and one winner, never a page or an occurrence array.
// It releases both references after the walk, so the plan factory and the
// decoration stage cannot retain them. The conservative whole charge stays
// reserved until the final payload peak rather than shrinking and regrowing
// across the stage boundary.
type fileInfoDecorationOptions struct {
	Surface   fileInfoOrderedSource
	DiscCheck func(context.Context, fileInfoOccurrence) (bool, error)
}

// The accepted scalar frame is the same rule file_info_sql_source and the group
// source use: ReadInfoAsset overlays the first pre-fence OLD delta per field in
// (slot,field) order, so an active slot-0 delta owns the field while it is before
// activation (phase<3) and a queued slot-1 delta is accepted only once the
// active slot-0 change has activated. Probing slot 1 first would publish a
// queued OLD while the active OLD still owns the field.
const (
	fileInfoDecorationAcceptedSize = `COALESCE(
 (SELECT o.old_integer FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=2 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),
 a.size)`
	fileInfoDecorationAcceptedModified = `COALESCE(
 (SELECT o.old_integer FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=3 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),
 a.modified_ns)`
)

// The parent point resolves both accepted scalars inside SQLite and compares
// them with the technical parent rows by their primary keys. Neither the token
// nor either accepted scalar ever crosses into Go. A materializing slot-0 change
// is exactly the state in which compactcatalog.ReadInfoAsset refuses the frame,
// so it truthfully produces no decodable technical lanes. A missing or
// mismatched technical parent row is a false lane, never an approximation from
// the current raw asset row.
const fileInfoDecorationParentQuery = `WITH accepted AS (
 SELECT a.id,a.token,` + fileInfoDecorationAcceptedSize + ` AS size,` + fileInfoDecorationAcceptedModified + ` AS modified,
 EXISTS(SELECT 1 FROM catalog_info_changes c WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.slot=0 AND c.phase IN(2,3)) AS materializing
 FROM catalog_assets a WHERE a.id=?
)
SELECT accepted.materializing,
 EXISTS(SELECT 1 FROM asset_stream_facts f WHERE f.asset_id=accepted.token AND f.size=accepted.size AND f.modified_ns=accepted.modified),
 EXISTS(SELECT 1 FROM asset_subtitle_facts f WHERE f.asset_id=accepted.token AND f.size=accepted.size AND f.modified_ns=accepted.modified)
FROM accepted`

// Streams are walked in the existing (asset_id,stream_index) primary-key order,
// one raw row per statement. The type decision happens after LIMIT, so a video
// or subtitle-type stream still consumes its own raw event. The first video
// ROWID is recorded, but the walk continues because a later audio stream still
// belongs to the audio count.
const (
	fileInfoDecorationStreamFirst = `SELECT s.stream_index,s.rowid,s.type FROM asset_streams s
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 ORDER BY s.stream_index LIMIT 1`
	fileInfoDecorationStreamSeek = `SELECT s.stream_index,s.rowid,s.type FROM asset_streams s
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 AND s.stream_index>?
 ORDER BY s.stream_index LIMIT 1`
)

// Subtitles are walked through asset_subtitles_asset(asset_id,id), one raw row
// per statement. The seek derives the last text id from the last raw ROWID inside
// SQLite, so no subtitle id or label enters Go. The first column is the anchor
// existence proof: a vanished anchor is stale instead of a silent restart. The
// second column is the next raw ROWID, or NULL at the end of the indexed range.
const (
	fileInfoDecorationSubtitleFirst = `SELECT s.rowid FROM asset_subtitles s INDEXED BY asset_subtitles_asset
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 ORDER BY s.id LIMIT 1`
	fileInfoDecorationSubtitleSeek = `SELECT
 EXISTS(SELECT 1 FROM asset_subtitles a WHERE a.rowid=? AND a.asset_id=(SELECT token FROM catalog_assets WHERE id=?)),
 (SELECT s.rowid FROM asset_subtitles s INDEXED BY asset_subtitles_asset
  WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
  AND s.id>(SELECT a.id FROM asset_subtitles a WHERE a.rowid=? AND a.asset_id=(SELECT token FROM catalog_assets WHERE id=?))
  ORDER BY s.id LIMIT 1)`
)

// fileInfoDecoratedPlan wraps one already-built numeric preparation. The wrapped
// plan is the exact ordered reducer for files, sources and members, or the exact
// group reducer for groups. New constructs it exactly once. The wrapper adds no
// alternate lane, no label/token/JSON cache and no resident page.
//
// Accounting composition, stated exactly:
//   - InitialBytes is the wrapped InitialBytes plus the fixed 32 KiB wrapper
//     charge plus the owner-audited captures plus the optional surface's own
//     AccountedBytes. Submission therefore admits the full peak before any
//     worker allocates anything.
//   - Inside New the wrapper reserves that whole charge, then constructs the
//     wrapped plan exactly once through budget.WithOverhead(fixed + captures +
//     surface). The wrapped plan's own New/Step Resize reserves its own bytes
//     PLUS that overhead, so the still-live wrapper, capture and surface bytes
//     remain charged while another worker admits work. The child view is used
//     for that one call only and is never retained across a Step. Resize
//     failures stay sticky on the original root budget. The wrapper restores the
//     whole charge immediately after the call and before the worker observes the
//     quantum.
//   - No wrapper-owned dynamic allocation happens during the underlying stage:
//     state and payload are nil. The two fixed arrays are allocated only after
//     the wrapped Step reports Done.
//   - On Done the numeric payload's ownership transfers to the wrapper and its
//     full capacity is retained and charged. The wrapped computation is
//     discarded, so its reservation is free before the optional surface walk.
//   - The optional surface and its root disc point are charged from submission
//     and are used for one fresh whole-surface walk after the numeric payload.
//     That walk keeps only the winning numeric occurrence. The surface and the
//     disc callback are then released before per-head metadata starts; the
//     conservative whole charge stays reserved until the final payload peak.
//   - Before the final payload allocation the wrapper re-reserves the fixed
//     charge plus the original payload capacity plus the final payload capacity.
//     The original payload is copied once and then dropped.
//
// Check is called before and after every quantum, including the factory. It is
// the parent-owned live opening/item/source-authority proof; a child or asset
// lifetime is never inferred from the resident page.
type fileInfoDecoratedComputation struct {
	underlying readprep.Computation
	db         *sql.DB
	lane       byte
	target     fileInfoOccurrence
	check      fileInfoBasisCheck

	wholeBytes int64
	charged    int64
	overhead   int64
	stage      int

	// Optional whole-surface owner-disc walk. These references are nil for a
	// non-owner and are dropped as soon as the walk ends.
	surface     fileInfoOrderedSource
	discCheck   func(context.Context, fileInfoOccurrence) (bool, error)
	surfaceAt   int
	surfaceSeq  int64
	surfaceRow  fileInfoOrderedRow
	surfaceDone bool
	best        fileInfoOccurrence
	hasBest     bool
	winner      fileInfoOccurrence

	base  []byte
	state *fileInfoDecorationState
	count int

	next int
	sub  int // 0 parent point, 1 streams, 2 subtitles, 3 publish one decoration

	audio, subtitles, videoRow int64
	audioOK, subtitleOK        bool
	streamAnchor               int64
	hasStream                  bool
	subtitleAnchor             int64
	hasSubtitle                bool
}

// fileInfoDecoratedPlan is the compatibility entry point for callers that do
// not need a whole-surface owner-disc winner. It delegates to the one internal
// implementation with both optional roots absent.
func fileInfoDecoratedPlan(plan readprep.Plan, db *sql.DB, lane byte, target fileInfoOccurrence, captureBytes int64, check fileInfoBasisCheck) (readprep.Plan, error) {
	return fileInfoDecoratedPlanOptions(plan, db, lane, target, captureBytes, check, fileInfoDecorationOptions{})
}

// fileInfoDecoratedSurfacePlan is the options-compatible entry point. It
// delegates to the one internal implementation so the original
// fileInfoDecoratedPlan call shape and the owner-surface call shape cannot
// diverge in validation, accounting or stage order.
func fileInfoDecoratedSurfacePlan(plan readprep.Plan, db *sql.DB, lane byte, target fileInfoOccurrence, captureBytes int64, check fileInfoBasisCheck, options fileInfoDecorationOptions) (readprep.Plan, error) {
	return fileInfoDecoratedPlanOptions(plan, db, lane, target, captureBytes, check, options)
}

// fileInfoDecoratedPlanOptions builds the bounded decoration wrapper for one
// completed numeric lane. lane is files, sources, members or groups. target is
// the root item (and, for exact lanes, the lane target); groups use target.Item
// plus each group's own Head, while files/sources/members keep each payload
// occurrence. captureBytes is the owner-audited size of the caller's own fixed
// captures (the optional surface's AccountedBytes is added separately) and check
// is the live authority probe. No other lane may be decorated: raw track/disc
// occurrences name rows, not assets whose technical parent could be inferred
// safely.
//
// options adds the optional whole-surface owner-disc walk. It runs once between
// the numeric payload and per-head metadata, is charged from submission, and
// retains only the winning numeric occurrence. A non-owner passes zero options,
// which skips the walk and leaves the winner zero.
func fileInfoDecoratedPlanOptions(plan readprep.Plan, db *sql.DB, lane byte, target fileInfoOccurrence, captureBytes int64, check fileInfoBasisCheck, options fileInfoDecorationOptions) (readprep.Plan, error) {
	if plan.New == nil || plan.InitialBytes < 0 || db == nil || captureBytes < 0 || check == nil ||
		target.Item <= 0 || target.Asset < 0 || !fileInfoDecorationLane(lane) {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	if (options.Surface == nil) != (options.DiscCheck == nil) {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	surfaceBytes := int64(0)
	if options.Surface != nil {
		surfaceBytes = options.Surface.AccountedBytes()
		if surfaceBytes < 0 {
			return readprep.Plan{}, readprep.ErrInvalid
		}
	}
	if captureBytes > math.MaxInt64-fileInfoDecorationFixedBytes ||
		surfaceBytes > math.MaxInt64-fileInfoDecorationFixedBytes-captureBytes ||
		plan.InitialBytes > math.MaxInt64-fileInfoDecorationFixedBytes-captureBytes-surfaceBytes {
		return readprep.Plan{}, readprep.ErrInvalid
	}
	initial := plan.InitialBytes + fileInfoDecorationFixedBytes + captureBytes + surfaceBytes
	// The underlying reducer is always called through a child view that carries
	// the still-live wrapper, capture and surface bytes as overhead. Its own
	// Resize therefore cannot temporarily uncharge those captures while another
	// worker admits work.
	overhead := fileInfoDecorationFixedBytes + captureBytes + surfaceBytes
	surface, discCheck := options.Surface, options.DiscCheck
	return readprep.Plan{
		ColdSearch:   plan.ColdSearch,
		ReuseHandle:  plan.ReuseHandle,
		ShouldYield:  plan.ShouldYield,
		InitialBytes: initial,
		New: func(ctx context.Context, budget *readprep.Budget) (readprep.Computation, error) {
			if err := budget.Resize(initial); err != nil {
				return nil, err
			}
			w := &fileInfoDecoratedComputation{
				db: db, lane: lane, target: target, check: check,
				surface: surface, discCheck: discCheck,
				wholeBytes: initial, charged: initial, overhead: overhead,
			}
			// The plan factory must not keep the optional surface/disc captures
			// reachable after it has handed them to the one computation.
			surface, discCheck = nil, nil
			if err := w.authority(ctx); err != nil {
				return nil, err
			}
			child, err := budget.WithOverhead(overhead)
			if err != nil {
				return nil, err
			}
			underlying, err := plan.New(ctx, child)
			if err != nil {
				return nil, err
			}
			if underlying == nil || underlying.AccountedBytes() < 0 || underlying.AccountedBytes() > initial-overhead {
				return nil, readprep.ErrInvalid
			}
			w.underlying = underlying
			// The wrapped factory may have shrunk the reservation to its own
			// bytes. Restore the wrapper's whole charge before the worker checks
			// AccountedBytes, and prove the basis once more.
			if err = budget.Resize(initial); err != nil {
				return nil, err
			}
			if err = w.authority(ctx); err != nil {
				return nil, err
			}
			return w, nil
		},
	}, nil
}

func fileInfoDecorationLane(lane byte) bool {
	switch lane {
	case fileInfoLaneFiles, fileInfoLaneSources, fileInfoLaneGroups, fileInfoLaneMembers:
		return true
	}
	return false
}

func (w *fileInfoDecoratedComputation) AccountedBytes() int64 { return w.charged }

func (w *fileInfoDecoratedComputation) authority(ctx context.Context) error {
	return w.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}

func (w *fileInfoDecoratedComputation) admit(ctx context.Context, budget *readprep.Budget) error {
	if err := budget.Resize(w.charged); err != nil {
		return err
	}
	return w.authority(ctx)
}

func (w *fileInfoDecoratedComputation) settle(ctx context.Context, budget *readprep.Budget) error {
	if err := w.authority(ctx); err != nil {
		return err
	}
	return budget.Resize(w.charged)
}

func (w *fileInfoDecoratedComputation) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	if w == nil || budget == nil {
		return readprep.StepResult{}, readprep.ErrInvalid
	}
	if err := w.admit(ctx, budget); err != nil {
		return readprep.StepResult{}, err
	}
	switch w.stage {
	case fileInfoDecorationStageUnderlying:
		return w.stepUnderlying(ctx, budget)
	case fileInfoDecorationStageSurface:
		return w.stepSurface(ctx, budget)
	case fileInfoDecorationStageDecorate:
		return w.stepDecorate(ctx, budget)
	default:
		return readprep.StepResult{}, readprep.ErrInvalid
	}
}

// stepUnderlying spends one complete underlying quantum. The wrapper performs no
// raw SQL of its own here and allocates nothing. When the wrapped plan finishes,
// the numeric payload becomes wrapper-owned and the decoration state is built;
// the completion is returned as "not done" so decoration starts on a clean
// quantum with its own 128-statement budget.
func (w *fileInfoDecoratedComputation) stepUnderlying(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	child, err := budget.WithOverhead(w.overhead)
	if err != nil {
		return readprep.StepResult{}, err
	}
	out, err := w.underlying.Step(ctx, child)
	if err != nil {
		return readprep.StepResult{}, err
	}
	if w.underlying.AccountedBytes() < 0 || w.underlying.AccountedBytes() > w.wholeBytes-w.overhead {
		return readprep.StepResult{}, readprep.ErrInvalid
	}
	if err = w.settle(ctx, budget); err != nil {
		return readprep.StepResult{}, err
	}
	if !out.Done {
		if out.Payload != nil {
			return readprep.StepResult{}, readprep.ErrInvalid
		}
		return readprep.StepResult{}, nil
	}
	if out.Payload == nil {
		return readprep.StepResult{}, readprep.ErrInvalid
	}
	if err = w.takePayload(ctx, budget, out.Payload); err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{}, nil
}

// takePayload keeps ownership of the exact numeric payload and chooses the
// optional whole-surface walk before any per-head metadata is read. The surface
// object is already part of the submission charge; this transition allocates
// nothing beyond retaining the numeric payload.
func (w *fileInfoDecoratedComputation) takePayload(ctx context.Context, budget *readprep.Budget, payload []byte) error {
	post := w.wholeBytes
	if need := fileInfoDecorationFixedBytes + int64(cap(payload)); need > post {
		post = need
	}
	if err := budget.Resize(post); err != nil {
		return err
	}
	w.charged, w.base = post, payload
	w.underlying = nil
	if w.surface != nil {
		w.stage = fileInfoDecorationStageSurface
		w.surfaceAt, w.surfaceSeq = 0, 0
		w.surfaceDone = false
		w.best, w.hasBest = fileInfoOccurrence{}, false
		return nil
	}
	return w.prepare(ctx, budget)
}

// prepare turns the exact numeric payload into the bounded occurrence
// coordinates after the optional surface walk has fixed the winner. The surface
// and disc callback are released here, before the decoration state is allocated;
// only the numeric payload and the fixed arrays remain. The numeric payload
// length is already validated by the existing decoders, which also cap the page
// at 100.
func (w *fileInfoDecoratedComputation) prepare(ctx context.Context, budget *readprep.Budget) error {
	if w.base == nil {
		return readprep.ErrInvalid
	}
	// The one surface walk is over. Neither its source nor the root disc point
	// is needed for per-head metadata, and neither remains reachable from the
	// plan factory after New.
	w.surface, w.discCheck = nil, nil
	post := w.wholeBytes
	if need := fileInfoDecorationFixedBytes + int64(cap(w.base)); need > post {
		post = need
	}
	if err := budget.Resize(post); err != nil {
		return err
	}
	w.charged = post
	w.state = &fileInfoDecorationState{}
	switch w.lane {
	case fileInfoLaneGroups:
		summary, err := decodeFileInfoGroupSummary(w.base)
		if err != nil {
			return err
		}
		if len(summary.Groups) > fileInfoDecorationLimit {
			return readprep.ErrInvalid
		}
		w.count = len(summary.Groups)
		for i, g := range summary.Groups {
			// Groups deliberately use the root target item, not ShapeHead, plus
			// the group's own displayed head asset.
			w.state.coords[i] = fileInfoOccurrence{Item: w.target.Item, Asset: g.Head}
		}
	default:
		summary, err := decodeFileInfoOrderedSummary(w.base)
		if err != nil {
			return err
		}
		if len(summary.Occurrences) > fileInfoDecorationLimit {
			return readprep.ErrInvalid
		}
		w.count = len(summary.Occurrences)
		copy(w.state.coords[:w.count], summary.Occurrences)
	}
	for i := 0; i < w.count; i++ {
		if w.state.coords[i].Item <= 0 || w.state.coords[i].Asset <= 0 {
			return readprep.ErrInvalid
		}
	}
	w.stage = fileInfoDecorationStageDecorate
	w.next, w.sub = 0, 0
	return nil
}

// stepSurface spends one bounded quantum on the fresh whole-surface walk. Every
// Next, every surface Check and every Compare is one operation in the same
// 128-statement/8ms budget, and DiscCheck is one more operation for each matched
// occurrence. The walk keeps only the best numeric occurrence, never a page or
// an occurrence array.
func (w *fileInfoDecoratedComputation) stepSurface(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	started := time.Now()
	ops := 0
	if err := w.surfaceCheck(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	ops++
	// Reserve the final statement slot for the closing surface Check. The clock
	// is consulted after every Next, DiscCheck and Compare, including the opening
	// Check, so one bounded quantum never starts another statement past 8ms.
	for ops < fileInfoDecorationStepOps-1 && time.Since(started) < fileInfoDecorationStepTime {
		if err := ctx.Err(); err != nil {
			return readprep.StepResult{}, err
		}
		used, err := w.surfaceOne(ctx)
		if err != nil {
			return readprep.StepResult{}, err
		}
		ops += used
		if w.surfaceDone {
			break
		}
	}
	if err := w.surfaceCheck(ctx); err != nil {
		return readprep.StepResult{}, err
	}
	if err := w.settle(ctx, budget); err != nil {
		return readprep.StepResult{}, err
	}
	if !w.surfaceDone {
		return readprep.StepResult{}, nil
	}
	if w.hasBest {
		w.winner = w.best
	}
	return readprep.StepResult{}, w.prepare(ctx, budget)
}

func (w *fileInfoDecoratedComputation) surfaceCheck(ctx context.Context) error {
	if w.surface == nil {
		return readprep.ErrInvalid
	}
	return w.surface.Check(ctx)
}

func (w *fileInfoDecoratedComputation) surfaceOne(ctx context.Context) (int, error) {
	switch w.surfaceAt {
	case 0:
		row, exists, err := w.surface.Next(ctx)
		if err != nil {
			return 1, err
		}
		if !exists {
			w.surfaceDone = true
			return 1, nil
		}
		if row.Sequence <= w.surfaceSeq {
			return 1, readprep.ErrInvalid
		}
		w.surfaceSeq = row.Sequence
		if !row.Match {
			return 1, nil
		}
		if row.Occurrence.Item <= 0 || row.Occurrence.Asset <= 0 {
			return 1, readprep.ErrInvalid
		}
		w.surfaceRow, w.surfaceAt = row, 1
		return 1, nil
	case 1:
		if w.discCheck == nil {
			return 1, readprep.ErrInvalid
		}
		ok, err := w.discCheck(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), w.surfaceRow.Occurrence)
		if err != nil {
			return 1, err
		}
		if !ok {
			w.surfaceAt = 0
			return 1, nil
		}
		if !w.hasBest {
			w.best, w.hasBest = w.surfaceRow.Occurrence, true
			w.surfaceAt = 0
			return 1, nil
		}
		w.surfaceAt = 2
		return 1, nil
	case 2:
		cmp, err := w.surface.Compare(ctx, w.surfaceRow.Occurrence, w.best)
		if err != nil {
			return 1, err
		}
		if cmp < -1 || cmp > 1 {
			return 1, readprep.ErrInvalid
		}
		if cmp == 0 && w.surfaceRow.Occurrence != w.best {
			return 1, readprep.ErrInvalid
		}
		if cmp < 0 {
			w.best = w.surfaceRow.Occurrence
		}
		w.surfaceAt = 0
		return 1, nil
	default:
		return 0, readprep.ErrInvalid
	}
}

func (w *fileInfoDecoratedComputation) stepDecorate(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	started := time.Now()
	ops := 0
	for ops < fileInfoDecorationStepOps {
		if err := ctx.Err(); err != nil {
			return readprep.StepResult{}, err
		}
		used, done, err := w.decorateOne(ctx)
		if err != nil {
			return readprep.StepResult{}, err
		}
		ops += used
		if done {
			payload, err := w.encode(ctx, budget)
			if err != nil {
				return readprep.StepResult{}, err
			}
			if err = w.settle(ctx, budget); err != nil {
				return readprep.StepResult{}, err
			}
			w.stage = fileInfoDecorationStageDone
			return readprep.StepResult{Done: true, Payload: payload}, nil
		}
		if time.Since(started) >= fileInfoDecorationStepTime {
			break
		}
	}
	if err := w.settle(ctx, budget); err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{}, nil
}

// decorateOne performs at most one raw statement. Zero-statement transitions are
// the two "no technical parent" zeroings, a finished lane, and publishing one
// completed decoration. There is no nested raw loop: the outer quantum counts
// every statement and checks the clock after each returned row or point.
func (w *fileInfoDecoratedComputation) decorateOne(ctx context.Context) (int, bool, error) {
	if w.next >= w.count {
		return 0, true, nil
	}
	switch w.sub {
	case 0:
		if err := w.readParent(ctx); err != nil {
			return 0, false, err
		}
		return 1, false, nil
	case 1:
		if !w.audioOK {
			w.audio, w.videoRow = 0, 0
			w.sub = 2
			return 0, false, nil
		}
		used, done, err := w.readStream(ctx)
		if err != nil {
			return used, false, err
		}
		if !done {
			return used, false, nil
		}
		w.sub = 2
		return used, false, nil
	case 2:
		if !w.subtitleOK {
			w.subtitles = 0
			w.sub = 3
			return 0, false, nil
		}
		used, done, err := w.readSubtitle(ctx)
		if err != nil {
			return used, false, err
		}
		if !done {
			return used, false, nil
		}
		w.sub = 3
		return used, false, nil
	case 3:
		w.state.decorations[w.next] = fileInfoDecoration{
			Occurrence: w.state.coords[w.next],
			Audio:      w.audio,
			Subtitles:  w.subtitles,
			VideoRow:   w.videoRow,
		}
		w.next++
		w.sub = 0
		w.audio, w.subtitles, w.videoRow = 0, 0, 0
		w.audioOK, w.subtitleOK = false, false
		w.streamAnchor, w.hasStream = 0, false
		w.subtitleAnchor, w.hasSubtitle = 0, false
		return 0, w.next >= w.count, nil
	default:
		return 0, false, readprep.ErrInvalid
	}
}

func (w *fileInfoDecoratedComputation) readParent(ctx context.Context) error {
	asset := w.state.coords[w.next].Asset
	w.audio, w.subtitles, w.videoRow = 0, 0, 0
	w.audioOK, w.subtitleOK = false, false
	w.streamAnchor, w.hasStream = 0, false
	w.subtitleAnchor, w.hasSubtitle = 0, false
	var materializing, audioOK, subtitleOK bool
	err := dbwork.ReadHandle(ctx, w.db).QueryRowContext(ctx, fileInfoDecorationParentQuery, asset).
		Scan(&materializing, &audioOK, &subtitleOK)
	if errors.Is(err, sql.ErrNoRows) {
		return readprep.ErrStale
	}
	if err != nil {
		return err
	}
	if !materializing {
		w.audioOK, w.subtitleOK = audioOK, subtitleOK
	}
	w.sub = 1
	return nil
}

func (w *fileInfoDecoratedComputation) readStream(ctx context.Context) (int, bool, error) {
	asset := w.state.coords[w.next].Asset
	query, args := fileInfoDecorationStreamFirst, []any{asset}
	if w.hasStream {
		query, args = fileInfoDecorationStreamSeek, append(args, w.streamAnchor)
	}
	var index, rowid int64
	var kind string
	err := dbwork.ReadHandle(ctx, w.db).QueryRowContext(ctx, query, args...).Scan(&index, &rowid, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, true, nil
	}
	if err != nil {
		return 1, false, err
	}
	if rowid <= 0 || w.hasStream && index <= w.streamAnchor {
		return 1, false, readprep.ErrInvalid
	}
	w.streamAnchor, w.hasStream = index, true
	if kind == "audio" {
		if w.audio == math.MaxInt64 {
			return 1, false, readprep.ErrInvalid
		}
		w.audio++
	}
	if kind == "video" && w.videoRow == 0 {
		w.videoRow = rowid
	}
	return 1, false, nil
}

func (w *fileInfoDecoratedComputation) readSubtitle(ctx context.Context) (int, bool, error) {
	asset := w.state.coords[w.next].Asset
	if !w.hasSubtitle {
		var rowid int64
		err := dbwork.ReadHandle(ctx, w.db).QueryRowContext(ctx, fileInfoDecorationSubtitleFirst, asset).Scan(&rowid)
		if errors.Is(err, sql.ErrNoRows) {
			return 1, true, nil
		}
		if err != nil {
			return 1, false, err
		}
		if rowid <= 0 {
			return 1, false, readprep.ErrInvalid
		}
		w.subtitleAnchor, w.hasSubtitle = rowid, true
		return 1, false, w.addSubtitle()
	}
	var present bool
	var rowid sql.NullInt64
	err := dbwork.ReadHandle(ctx, w.db).QueryRowContext(ctx, fileInfoDecorationSubtitleSeek, w.subtitleAnchor, asset, asset, w.subtitleAnchor, asset).Scan(&present, &rowid)
	if err != nil {
		return 1, false, err
	}
	if !present {
		return 1, false, readprep.ErrStale
	}
	if !rowid.Valid {
		return 1, true, nil
	}
	if rowid.Int64 <= 0 {
		return 1, false, readprep.ErrInvalid
	}
	w.subtitleAnchor = rowid.Int64
	return 1, false, w.addSubtitle()
}

func (w *fileInfoDecoratedComputation) addSubtitle() error {
	if w.subtitles == math.MaxInt64 {
		return readprep.ErrInvalid
	}
	w.subtitles++
	return nil
}

// The exact wrapper frame is:
//
//	original numeric payload || count(1) || winner.Item(8) || winner.Asset(8) || decoration records (40*count)
//
// The winner is the zero occurrence for a non-owner or an empty surface. The
// numeric payload's own version/count header determines its exact length, so
// count is duplicated here as a cheap cross-check and the payload remains a
// prefix of the wrapper frame.
//
// encode reserves the whole final peak before allocating. The original numeric
// payload must still be charged because both buffers are live during the one
// copy. After the copy the original is dropped; its backing array becomes
// collectible and only the final payload (plus the already-charged fixed state)
// remains.
func (w *fileInfoDecoratedComputation) encode(ctx context.Context, budget *readprep.Budget) ([]byte, error) {
	if w.base == nil || w.count < 0 || w.count > fileInfoDecorationLimit {
		return nil, readprep.ErrInvalid
	}
	final := len(w.base) + 1 + 16 + fileInfoDecorationRecordBytes*w.count
	if final < len(w.base) || int64(final) > math.MaxInt64-fileInfoDecorationFixedBytes-int64(cap(w.base)) {
		return nil, readprep.ErrInvalid
	}
	need := fileInfoDecorationFixedBytes + int64(cap(w.base)) + int64(final)
	peak := w.charged
	if need > peak {
		peak = need
	}
	if err := budget.Resize(peak); err != nil {
		return nil, err
	}
	w.charged = peak
	payload := make([]byte, final)
	copy(payload, w.base)
	off := len(w.base)
	payload[off] = byte(w.count)
	off++
	binary.BigEndian.PutUint64(payload[off:off+8], uint64(w.winner.Item))
	binary.BigEndian.PutUint64(payload[off+8:off+16], uint64(w.winner.Asset))
	off += 16
	for i := 0; i < w.count; i++ {
		d := w.state.decorations[i]
		binary.BigEndian.PutUint64(payload[off:off+8], uint64(d.Occurrence.Item))
		binary.BigEndian.PutUint64(payload[off+8:off+16], uint64(d.Occurrence.Asset))
		binary.BigEndian.PutUint64(payload[off+16:off+24], uint64(d.Audio))
		binary.BigEndian.PutUint64(payload[off+24:off+32], uint64(d.Subtitles))
		binary.BigEndian.PutUint64(payload[off+32:off+40], uint64(d.VideoRow))
		off += fileInfoDecorationRecordBytes
	}
	w.base = nil
	return payload, nil
}

// decodeFileInfoDecoratedSummary splits one wrapper payload back into the exact
// original numeric payload, its bounded decoration array, and the optional
// whole-surface owner-disc winner. The numeric payload is returned as a subslice
// of raw, so the caller can pass it to the existing ordered/group decoder without
// a copy. Version 2 is the ordered numeric frame (28+16n); version 3 is the group
// numeric frame (36+74n, including the exact size/duration high words and the
// duration-bad flag). The wrapper's own count byte must equal the numeric header
// count, the winner is either the zero occurrence (non-owner/no winner) or a
// complete positive item/asset pair, and every decoration record is fixed width.
// No string or label is present in either half.
func decodeFileInfoDecoratedSummary(raw []byte) ([]byte, []fileInfoDecoration, fileInfoOccurrence, error) {
	var winner fileInfoOccurrence
	if len(raw) < 2 {
		return nil, nil, winner, readprep.ErrInvalid
	}
	count := int(raw[1])
	var baseLen int
	switch raw[0] {
	case 2:
		if count > fileInfoDecorationLimit || len(raw) < 28 {
			return nil, nil, winner, readprep.ErrInvalid
		}
		baseLen = 28 + 16*count
	case 3:
		if count > fileInfoDecorationLimit || len(raw) < 36 {
			return nil, nil, winner, readprep.ErrInvalid
		}
		baseLen = 36 + fileInfoGroupRecordBytes*count
	default:
		return nil, nil, winner, readprep.ErrInvalid
	}
	const trailer = 1 + 16
	if baseLen+trailer > len(raw) || int(raw[baseLen]) != count ||
		len(raw) != baseLen+trailer+fileInfoDecorationRecordBytes*count {
		return nil, nil, winner, readprep.ErrInvalid
	}
	base := raw[:baseLen]
	winner = fileInfoOccurrence{
		Item:  int64(binary.BigEndian.Uint64(raw[baseLen+1 : baseLen+9])),
		Asset: int64(binary.BigEndian.Uint64(raw[baseLen+9 : baseLen+17])),
	}
	if (winner.Item == 0) != (winner.Asset == 0) || winner.Item < 0 || winner.Asset < 0 {
		return nil, nil, fileInfoOccurrence{}, readprep.ErrInvalid
	}
	decorations := make([]fileInfoDecoration, count)
	for i := 0; i < count; i++ {
		off := baseLen + trailer + fileInfoDecorationRecordBytes*i
		d := fileInfoDecoration{
			Occurrence: fileInfoOccurrence{
				Item:  int64(binary.BigEndian.Uint64(raw[off : off+8])),
				Asset: int64(binary.BigEndian.Uint64(raw[off+8 : off+16])),
			},
			Audio:     int64(binary.BigEndian.Uint64(raw[off+16 : off+24])),
			Subtitles: int64(binary.BigEndian.Uint64(raw[off+24 : off+32])),
			VideoRow:  int64(binary.BigEndian.Uint64(raw[off+32 : off+40])),
		}
		if d.Occurrence.Item <= 0 || d.Occurrence.Asset <= 0 || d.Audio < 0 || d.Subtitles < 0 || d.VideoRow < 0 {
			return nil, nil, fileInfoOccurrence{}, readprep.ErrInvalid
		}
		decorations[i] = d
	}
	return base, decorations, winner, nil
}
