package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// FIRST capture consumes W's sealed, currently authorized owned report and its
// original snapshot. These are data, never a caller permission flag/minter.
// W retains the source/policy/clock/resource capability, original classifier,
// bounded events and physical terminal. This component never reports Applied.
// Paused/admin-only/pre-start/disposal and occurrence activation stay separate.
type wc008CaptureReport struct {
	source                           wc008ProgressCommand
	original                         wc008HistorySnapshot
	positionSeconds, durationSeconds float64
	startedPercent                   int64
	completed                        bool // completion fact from W's sealed legacy-policy verifier
	state                            string
}

type wc008CaptureEffect struct {
	createdStart                                                           bool
	startEventID, startVariableBytes, completionVariableBytes              int64
	viewerRevision, observationRevision, historyRevision, personalRevision int64
	controlVariableBytes                                                   int64
}

const (
	wc008CaptureGateEmpty      stmt.ID = "wc.capture.gate_empty"
	wc008CaptureGateStarted    stmt.ID = "wc.capture.gate_started"
	wc008CaptureClaimEmpty     stmt.ID = "wc.capture.claim_empty"
	wc008CaptureClaimStarted   stmt.ID = "wc.capture.claim_started"
	wc008CaptureLeaf           stmt.ID = "wc.capture.leaf"
	wc008CaptureLeafUpdate     stmt.ID = "wc.capture.leaf_update"
	wc008CaptureSnapshot       stmt.ID = "wc.capture.snapshot"
	wc008CaptureSnapshotInsert stmt.ID = "wc.capture.snapshot_insert"
	wc008CaptureViewer         stmt.ID = "wc.capture.viewer"
	wc008CaptureFence          stmt.ID = "wc.capture.item_fence"
	wc008CaptureProfileFence   stmt.ID = "wc.capture.profile_fence"
	wc008CaptureReceipt        stmt.ID = "wc.capture.receipt"
	wc008CaptureInstantRead    stmt.ID = "wc.capture.instant_read"
	wc008CaptureInstantUpdate  stmt.ID = "wc.capture.instant_update"
	wc008CaptureInstantInsert  stmt.ID = "wc.capture.instant_insert"
)

// Identical to the existing report threshold: clamp known duration first,
// percentage /30s unknown/1s audiobook part, and completion-first. No time credit.
func (q wc008CaptureReport) qualifiedStart() (bool, error) {
	maxSeconds := float64(int64(1)<<40) / 1000
	if math.IsNaN(q.positionSeconds) || math.IsInf(q.positionSeconds, 0) || q.positionSeconds < 0 || q.positionSeconds > maxSeconds || math.IsNaN(q.durationSeconds) || math.IsInf(q.durationSeconds, 0) || q.durationSeconds < 0 || q.durationSeconds > maxSeconds || q.startedPercent < 0 || q.startedPercent > 100 {
		return false, ErrInvalid
	}
	position := q.positionSeconds
	if q.durationSeconds > 0 {
		position = math.Min(position, q.durationSeconds)
	}
	threshold := 30.0
	if q.durationSeconds > 0 {
		threshold = q.durationSeconds * float64(q.startedPercent) / 100
	}
	kind, ok := q.original.values[9].(int64)
	if !ok {
		return false, ErrInvalid
	}
	if kind == 9 {
		threshold = 1
	}
	return position >= threshold || q.completed, nil
}

func wc008FenceAllows(ctx context.Context, turn *commandTurn, id stmt.ID, ordinal int64, args ...any) error {
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var through int64
	if err = row.Scan(&through); errors.Is(err, sql.ErrNoRows) {
		return turn.checkLifetimes(ctx)
	} else if err != nil {
		return err
	}
	if through < 0 {
		return ErrResolveRequired
	}
	if ordinal <= through {
		return ErrConflict
	}
	return nil
}

func applyWC008Capture(ctx context.Context, turn *commandTurn, q wc008CaptureReport) (out wc008CaptureEffect, err error) {
	if turn == nil {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	p := q.source
	if err = p.validateSource(); err != nil {
		return out, err
	}
	if p.startEventID < 0 || p.proof.reportSequence <= p.lastAcceptedSequence || p.cursorMS != p.fromMS || p.untilMS > p.appliedMS || (q.state != "playing" && q.state != "paused" && q.state != "ended") {
		return out, ErrConflict
	}
	qualified, err := q.qualifiedStart()
	if err != nil {
		return out, err
	}
	if err = wc008FenceAllows(ctx, turn, wc008CaptureFence, p.acceptedOrdinal, p.profileKey, p.subjectID); err != nil {
		return out, err
	}
	if err = wc008FenceAllows(ctx, turn, wc008CaptureProfileFence, p.acceptedOrdinal, p.profileKey); err != nil {
		return out, err
	}
	gate := wc008CaptureGateEmpty
	args := []any{p.proof.playbackID, p.profileKey, p.subjectID, p.libraryID, p.authority, p.accountKey, p.profileID, p.memberIncarnation, p.profileLifetime, p.identity.Incarnation[:], p.last, p.acknowledged, p.acceptedOrdinal, p.lastAcceptedSequence, p.proof.occurrenceGeneration, p.proof.activityGeneration, p.proof.rememberGeneration, p.oldControlBytes}
	if p.startEventID > 0 {
		gate = wc008CaptureGateStarted
		args = append(args, p.startEventID)
	}
	row, err := wc008DraftQuery(ctx, turn, gate, args...)
	if err != nil {
		return out, err
	}
	var oldStart, oldCompletion, kind int64
	if err = row.Scan(&oldStart, &oldCompletion, &kind); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if (oldStart != 0 && oldStart != 1) || (oldCompletion != 0 && oldCompletion != 1) || oldStart != wc008Bool(p.startEventID > 0) {
		return out, ErrResolveRequired
	}
	if k, ok := q.original.values[9].(int64); !ok || k != kind {
		return out, ErrConflict
	}
	if p.startEventID == 0 && !qualified {
		return out, ErrConflict
	} // no prefix acceptance of unrepresented pre-start time
	snapshot := q.original
	if p.startEventID > 0 {
		snapshot, err = readWC008Start(ctx, turn, p)
		if err != nil {
			return out, err
		}
		out.startEventID = p.startEventID
	}
	if err = wc008CheckCaptureSnapshot(snapshot, p); err != nil {
		return out, err
	}
	if p.startEventID == 0 {
		snapshot.values[0], snapshot.values[1], snapshot.values[6] = p.appliedAt, "started", p.proof.reportSequence
		snapshot.values[23], snapshot.values[34], snapshot.values[35] = int64(0), p.appliedMS, nil
		out.startEventID, out.startVariableBytes, err = appendWC008History(ctx, turn, snapshot)
		if err != nil {
			return out, err
		}
		out.createdStart = true
	}
	startDelta, completeDelta := wc008Bool(out.createdStart), int64(0)
	if q.completed && oldCompletion == 0 {
		completeDelta = 1
		snapshot.values[0], snapshot.values[1], snapshot.values[6] = p.appliedAt, "completed", p.proof.reportSequence
		snapshot.values[23], snapshot.values[25], snapshot.values[35] = int64(0), int64(1), nil
		_, out.completionVariableBytes, err = appendWC008History(ctx, turn, snapshot)
		if err != nil {
			return out, err
		}
	}
	// Report-owned resume/recency/counts commit before any pending obligation is exposed.
	var semantic int64
	out.personalRevision, semantic, err = wc008CapturePersonal(ctx, turn, q, startDelta, completeDelta, qualified)
	if err != nil {
		return out, err
	}
	at, ok := snapshot.values[34].(int64)
	if !ok || at < 0 {
		return out, ErrResolveRequired
	}
	if startDelta != 0 || completeDelta != 0 {
		if err = wc008CaptureInstant(ctx, turn, p, at, startDelta, completeDelta); err != nil {
			return out, err
		}
		stamp := time.UnixMilli(at).UTC()
		month := time.Date(stamp.Year(), stamp.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		for _, b := range [3]struct {
			grain string
			start int64
		}{{"quarter", at - at%wcQuarterMS}, {"day", at - at%86400000}, {"month", month}} {
			if err = wc008ProgressBucketDelta(ctx, turn, p, b.grain, b.start, startDelta, completeDelta, 0, true); err != nil {
				return out, err
			}
			if err = wc008ProgressBucketDelta(ctx, turn, p, b.grain, b.start, startDelta, completeDelta, 0, false); err != nil {
				return out, err
			}
		}
	}
	// A semantic start/completion increments both semantic and observational data.
	viewer, err := wc008DraftQuery(ctx, turn, wc008CaptureViewer, semantic, p.profileKey, p.libraryKey, int64(math.MaxInt64)-semantic, int64(math.MaxInt64))
	if err != nil {
		return out, err
	}
	if err = viewer.Scan(&out.viewerRevision, &out.observationRevision); err != nil {
		return out, err
	}
	out.historyRevision, err = wc008DraftInteger(ctx, turn, wc008ProgressHistoryRevision, p.profileKey, p.proof.activityGeneration, p.proof.rememberGeneration, int64(math.MaxInt64))
	if err != nil {
		return out, err
	}
	claim := wc008CaptureClaimEmpty
	clock := p.proof.clockIncarnation
	args = []any{p.proof.reportSequence, wc008Bool(oldStart == 1 || out.createdStart), wc008Bool(oldCompletion == 1 || q.completed), out.startEventID, p.fromMS, p.untilMS, p.fromMS, p.proof.activityGeneration, p.proof.rememberGeneration, clock[:], p.proof.reportSequence, out.startEventID, p.proof.occurrenceGeneration, p.proof.playbackID, p.identity.Incarnation[:], p.last, p.acknowledged, p.lastAcceptedSequence, p.proof.occurrenceGeneration, p.oldControlBytes}
	if p.startEventID > 0 {
		claim = wc008CaptureClaimStarted
		args = append(args, p.startEventID)
	}
	out.controlVariableBytes, err = wc008DraftInteger(ctx, turn, claim, args...)
	if err != nil {
		return out, err
	}
	return out, turn.checkLifetimes(ctx)
}

func wc008Bool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func wc008CheckCaptureSnapshot(s wc008HistorySnapshot, p wc008ProgressCommand) error {
	if s.values[2] != p.accountKey || s.values[3] != p.profileID || s.values[4] != p.proof.playbackID || s.values[5] != p.proof.occurrenceGeneration || s.values[8] != p.libraryKey || s.values[32] != p.subjectID || s.values[39] != p.memberIncarnation || s.values[40] != p.profileLifetime {
		return ErrConflict
	}
	id, ok := s.values[7].([]byte)
	if !ok || !bytes.Equal(id, p.subjectPublicID[:]) {
		return ErrConflict
	}
	return nil
}

// Existing canonical personal snapshot is required; capture never initializes a
// missing owner/history representation or overwrites unrelated manual fields.
func wc008CapturePersonal(ctx context.Context, turn *commandTurn, q wc008CaptureReport, start, complete int64, qualified bool) (int64, int64, error) {
	p := q.source
	row, err := wc008DraftQuery(ctx, turn, wc008CaptureLeaf, p.profileKey, p.subjectID, p.subjectPublicID[:], p.proof.activityGeneration)
	if err != nil {
		return 0, 0, err
	}
	var revision, watched, starts, plays int64
	var watchedAt, lastStarted, lastCompleted, lastPlayed sql.NullString
	if err = row.Scan(&revision, &watched, &starts, &plays, &watchedAt, &lastStarted, &lastCompleted, &lastPlayed); err != nil {
		return 0, 0, err
	}
	if revision <= 0 || starts < 0 || plays < 0 || starts > math.MaxInt64-start || plays > math.MaxInt64-complete {
		return 0, 0, ErrResolveRequired
	}
	saved, err := wc008DraftQuery(ctx, turn, wc008CaptureSnapshot, p.profileKey, p.subjectID, revision)
	if err != nil {
		return 0, 0, err
	}
	var raw string
	if err = saved.Scan(&raw); err != nil {
		return 0, 0, ErrResolveRequired
	}
	var state map[string]json.RawMessage
	if err = json.Unmarshal([]byte(raw), &state); err != nil || state == nil {
		return 0, 0, ErrResolveRequired
	}
	var oldR int64
	var oldWatched bool
	if json.Unmarshal(state["revision"], &oldR) != nil || oldR != revision || json.Unmarshal(state["watched"], &oldWatched) != nil || oldWatched != (watched == 1) {
		return 0, 0, ErrResolveRequired
	}
	nextWatched := watched
	position := int64(0)
	if q.completed {
		nextWatched = 1
		watchedAt = sql.NullString{String: p.appliedAt, Valid: true}
	} else if qualified {
		nextWatched = 0
		watchedAt = sql.NullString{}
	}
	seconds := q.positionSeconds
	if q.durationSeconds > 0 {
		seconds = math.Min(seconds, q.durationSeconds)
	}
	kind := q.original.values[9].(int64)
	if qualified && !q.completed || kind == 9 {
		position = int64(math.Round(seconds * 1000))
	}
	if kind == 7 {
		position = 0
	}
	semantic := wc008Bool(start != 0 || complete != 0 || nextWatched != watched || !lastPlayed.Valid || lastPlayed.String == "")
	// Routine resume checkpoints keep the existing semantic recently-played clock.
	// A real occurrence/count/completion boundary can advance it, never backwards.
	boundary := start != 0 || complete != 0 || nextWatched != watched || !lastPlayed.Valid || lastPlayed.String == "" || q.state == "ended"
	if old, parseErr := time.Parse(time.RFC3339Nano, lastPlayed.String); boundary && (parseErr != nil || time.UnixMilli(p.appliedMS).After(old)) {
		lastPlayed = sql.NullString{String: p.appliedAt, Valid: true}
	}
	if revision > math.MaxInt64-semantic {
		return 0, 0, ErrResolveRequired
	}
	nextR := revision + semantic
	if start != 0 {
		lastStarted = sql.NullString{String: p.appliedAt, Valid: true}
	}
	if complete != 0 {
		lastCompleted = sql.NullString{String: p.appliedAt, Valid: true}
	}
	var sourceRevision any
	if q.original.values[30] != nil {
		sourceRevision = q.original.values[30]
	}
	_, err = wc008DraftInteger(ctx, turn, wc008CaptureLeafUpdate, nextR, nextWatched, wc008NullText(watchedAt), starts+start, plays+complete, wc008NullText(lastPlayed), wc008NullText(lastStarted), wc008NullText(lastCompleted), position, int64(0), wc008Bool(q.completed), p.proof.playbackID, sourceRevision, p.subjectPublicID[:], p.appliedAt, q.state, p.profileKey, p.subjectID, p.subjectPublicID[:], p.proof.activityGeneration, revision, starts, plays)
	if err != nil {
		return 0, 0, err
	}
	if semantic != 0 {
		put := func(key string, v any) { data, _ := json.Marshal(v); state[key] = data }
		put("revision", nextR)
		put("watched", nextWatched == 1)
		put("progressSeconds", float64(position)/1000)
		put("lastPlayedAt", lastPlayed.String)
		data, err := json.Marshal(state)
		if err != nil {
			return 0, 0, err
		}
		if err = wc008DraftChanged(ctx, turn, wc008CaptureSnapshotInsert, p.profileKey, p.subjectID, nextR, string(data), p.appliedAt); err != nil {
			return 0, 0, err
		}
	}
	return nextR, semantic, turn.checkLifetimes(ctx)
}

func wc008NullText(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}

func wc008CaptureInstant(ctx context.Context, turn *commandTurn, p wc008ProgressCommand, at, started, completed int64) error {
	args := []any{p.profileKey, p.proof.activityGeneration, at, p.subjectID}
	row, err := wc008DraftQuery(ctx, turn, wc008CaptureInstantRead, args...)
	if err != nil {
		return err
	}
	var a, b int64
	err = row.Scan(&a, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return wc008DraftChanged(ctx, turn, wc008CaptureInstantInsert, append(args, started, completed)...)
	}
	if err != nil {
		return err
	}
	if a < 0 || b < 0 || a > math.MaxInt64-started || b > math.MaxInt64-completed {
		return ErrResolveRequired
	}
	values := append([]any{a + started, b + completed}, args...)
	values = append(values, a, b)
	return wc008DraftChanged(ctx, turn, wc008CaptureInstantUpdate, values...)
}

func storeWC008CaptureResult(ctx context.Context, turn *commandTurn, q wc008CaptureReport, e wc008CaptureEffect, request RequestDigest, result ResultDigest) (n int64, err error) {
	if turn == nil {
		return 0, ErrInvalid
	}
	defer func() { n, err = finishCommand(ctx, commandLifetime(turn), n, err) }()
	p := q.source
	if e.startEventID <= 0 || e.personalRevision <= 0 || e.viewerRevision < 0 || e.observationRevision <= 0 || e.historyRevision <= 0 || e.controlVariableBytes <= 0 || request == (RequestDigest{}) || result == (ResultDigest{}) || p.receiptRows < 0 || p.receiptRows == math.MaxInt64 || p.receiptVariableBytes < 0 || (p.receiptRows == 0) != (p.receiptVariableBytes == 0) || e.createdStart != (p.startEventID == 0) {
		return 0, ErrResolveRequired
	}
	var oldStart any
	if p.startEventID > 0 {
		oldStart = p.startEventID
	}
	n, err = wc008DraftInteger(ctx, turn, wc008CaptureReceipt, p.proof.playbackID, p.identity.Incarnation[:], p.identity.Sequence, p.identity.OperationID[:], request[:], "capture_report", p.resultVersion, p.appliedMS, p.acceptedOrdinal, p.lastAcceptedSequence, p.proof.reportSequence, p.proof.occurrenceGeneration, p.proof.activityGeneration, p.proof.rememberGeneration, p.proof.clockIncarnation[:], oldStart, e.startEventID, p.proof.reportSequence, p.proof.reportSequence, p.fromMS, p.untilMS, p.fromMS, e.viewerRevision, e.observationRevision, e.historyRevision, e.personalRevision, wc008Bool(e.createdStart), int64(1), result[:])
	if err != nil {
		return 0, err
	}
	if n <= 0 || p.receiptVariableBytes > math.MaxInt64-n {
		return 0, ErrResolveRequired
	}
	err = wc008DraftChanged(ctx, turn, wc008ProgressOwnerAdvance, p.identity.Sequence, p.receiptRows+1, p.receiptVariableBytes+n, p.proof.playbackID, p.identity.Incarnation[:], p.last, p.acknowledged, p.receiptRows, p.receiptVariableBytes)
	return n, err
}
