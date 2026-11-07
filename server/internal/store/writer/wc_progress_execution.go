package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

func wcProgressMemoryBytes(v *wcProgressInvocation) uint64 {
	// Bounded scalar protocol plus the complete owned snapshot/text/bind copies.
	// The actual whole-command partition is supplied by trusted composition.
	n := uint64(64 << 10)
	s := v.disclosure.driver.scope
	for _, text := range []string{s.playbackID, s.profileKey, s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryKey, s.familyID, s.serverID, s.tokenHash, s.memberLibraries, s.profileLibraries.String} {
		n += uint64(len(text)) * 32
	}
	if v.input != nil {
		for _, cell := range v.input.request.Original {
			n += uint64(len(cell.Text)+len(cell.Blob)) * 32
		}
	}
	return n
}

type wcProgressCompletion struct {
	out        ProgressResult
	ack        AckResult
	historical bool
}

func executeWCProgressInvocation(ctx context.Context, t *commandTurn, v *wcProgressInvocation, ack bool) (out ProgressResult, acknowledged AckResult, historical bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), wcProgressCompletion{out: out, ack: acknowledged, historical: historical}, err)
		out, acknowledged, historical, err = x.out, x.ack, x.historical, e
	}()
	if err = t.requireCommandMemory(ctx, ProgressAccept, wcProgressMemoryBytes(v), "wc_progress_memory"); err != nil {
		return
	}
	if v.birth != nil {
		return out, acknowledged, true, applyWCProgressFixtureBirth(ctx, t, v)
	}
	s, err := readWCProgressOwner(ctx, t, v.disclosure.driver.scope)
	if err != nil {
		return out, acknowledged, false, err
	}
	if v.snapshot {
		if ack || v.input != nil {
			return out, acknowledged, false, ErrInvalid
		}
		if v.retained != nil && !wcPendingMatches(v.retained, s) {
			return out, acknowledged, false, ErrConflict
		}
		out.Snapshot, err = readWCProgressSnapshot(ctx, t, v.disclosure.driver.scope, s)
		return out, acknowledged, true, err
	}
	lookup, err := lookupWCProgress(ctx, t, v, s)
	if err != nil {
		return out, acknowledged, false, err
	}
	if ack {
		if v.retained != nil {
			return out, acknowledged, false, ErrDisclosureDenied
		}
		now, e := t.commandTimeMS(ctx, ProgressAccept, 0)
		if e != nil {
			return out, acknowledged, false, e
		}
		acknowledged.AcknowledgedThrough, err = acknowledgeWCProgress(ctx, t, wcProgressAck{playbackID: v.request.PlaybackID, reference: v.request.Receipt, owner: lookup.owner, decision: lookup.decision, sourceKind: v.request.Kind, receiptRows: s.ReceiptRows, receiptVariableBytes: s.ReceiptBytes, afterSequence: s.AfterSequence, due: s.Due, appliedMS: now})
		return out, acknowledged, true, err
	}
	out.Outcome.Reference = v.request.Receipt
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, acknowledged, true, nil
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, acknowledged, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, acknowledged, false, ErrResolveRequired
	}
	if v.input == nil {
		return out, acknowledged, false, nil
	}
	original, e := progressReference(v.input)
	if e != nil || original != v.request || v.input.driver != v.disclosure.driver {
		return out, acknowledged, false, errors.Join(ErrConflict, e)
	}
	p := v.input.request.Source
	if p.last != s.Last || p.acknowledged != s.Acknowledged || p.receiptRows != s.ReceiptRows || p.receiptVariableBytes != s.ReceiptBytes || p.oldControlBytes != s.ControlBytes {
		return out, acknowledged, false, ErrConflict
	}
	p.appliedMS, err = t.commandTimeMS(ctx, ProgressAccept, 0)
	if err != nil {
		return out, acknowledged, false, err
	}
	p.appliedAt = time.UnixMilli(p.appliedMS).UTC().Format(time.RFC3339Nano)
	var capture wc008CaptureEffect
	var drain wc008ProgressEffect
	q := v.input.request
	if q.Kind == "capture_report" {
		if v.retained != nil || s.Pending {
			return out, acknowledged, false, ErrConflict
		}
		n, e := wc008DraftInteger(ctx, t, wcProgressSnapshotOctets, p.profileKey, p.subjectID)
		if e != nil || n < 0 {
			return out, acknowledged, false, errors.Join(ErrResolveRequired, e)
		}
		base := wcProgressMemoryBytes(v)
		if uint64(n) > (^uint64(0)-base)/32 {
			return out, acknowledged, false, ErrCap
		}
		if e = t.requireCommandMemory(ctx, ProgressAccept, base+uint64(n)*32, "wc_progress_memory"); e != nil {
			return out, acknowledged, false, e
		}
		snapshot, e := wcRestoreSnapshot(q.Original)
		if e != nil {
			return out, acknowledged, false, e
		}
		if p.startEventID == 0 {
			snapshot.values[34] = p.appliedMS
		}
		report := wc008CaptureReport{source: p, original: snapshot, positionSeconds: q.Position, durationSeconds: q.Duration, startedPercent: q.StartedPercent, completed: q.Completed, state: q.State}
		capture, err = applyWC008Capture(ctx, t, report)
		if err == nil {
			out.Value = wcCaptureValue(p, capture)
			out.Digest, err = canonicalResultDigest(p.identity, v.request.Receipt.RequestDigest, out.Value)
		}
		if err == nil {
			_, err = storeWC008CaptureResult(ctx, t, report, capture, v.request.Receipt.RequestDigest, out.Digest)
		}
	} else if q.Kind == "drain_piece" {
		if v.retained == nil || v.retained.driver != v.input.driver || v.retained.source != q.Source || !s.Pending || p.fromMS != s.From.Int64 || p.untilMS != s.Until.Int64 || p.cursorMS != s.Cursor.Int64 || p.startEventID != s.StartEvent.Int64 || p.proof.clockIncarnation != s.Clock || p.proof.occurrenceGeneration != s.Occurrence || p.proof.reportSequence != s.ReportSequence {
			return out, acknowledged, false, ErrConflict
		}
		drain, err = applyWC008ProgressMetrics(ctx, t, p)
		if err == nil {
			out.Value = wcDrainValue(p, drain)
			out.Digest, err = canonicalResultDigest(p.identity, v.request.Receipt.RequestDigest, out.Value)
		}
		if err == nil {
			_, err = storeWC008ProgressResult(ctx, t, p, drain, v.request.Receipt.RequestDigest, out.Digest)
		}
	} else {
		err = ErrInvalid
	}
	if err == nil {
		err = appendWCProgressEvents(ctx, t, v.disclosure.driver.scope, out.Value)
	}
	return out, acknowledged, false, err
}

func wcPresent(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
func wcStart(n int64) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return wcPresent(n)
}
func wcProgressBaseValue(p wc008ProgressCommand, kind string) ProgressValue {
	return ProgressValue{CommandKind: kind, ResultVersion: 1, AppliedMs: p.appliedMS, SourceAcceptedOrdinal: p.acceptedOrdinal, SourceLastAcceptedSequence: p.lastAcceptedSequence, SourceReportSequence: p.proof.reportSequence, SourceOccurrenceGeneration: p.proof.occurrenceGeneration, SourceActivityGeneration: p.proof.activityGeneration, SourceHistoryPolicyGeneration: p.proof.rememberGeneration, SourceClockIncarnation: p.proof.clockIncarnation, SourceStartEventId: wcStart(p.startEventID), ResultCount: 1, ResultPendingSequence: p.proof.reportSequence}
}
func wcCaptureValue(p wc008ProgressCommand, e wc008CaptureEffect) ProgressValue {
	v := wcProgressBaseValue(p, "capture_report")
	v.ResultStartEventId = wcPresent(e.startEventID)
	v.ResultReportSequence = p.proof.reportSequence
	v.ResultPendingFromMs, v.ResultPendingUntilMs, v.ResultPendingCursorMs = wcPresent(p.fromMS), wcPresent(p.untilMS), wcPresent(p.fromMS)
	v.ResultViewerRevision, v.ResultObservationRevision, v.ResultHistoryRevision, v.ResultPersonalRevision = wcPresent(e.viewerRevision), wcPresent(e.observationRevision), wcPresent(e.historyRevision), wcPresent(e.personalRevision)
	v.CaptureCreatedStart = wcPresent(wc008Bool(e.createdStart))
	return v
}
func wcDrainValue(p wc008ProgressCommand, e wc008ProgressEffect) ProgressValue {
	v := wcProgressBaseValue(p, "drain_piece")
	v.SourcePendingFromMs, v.SourcePendingUntilMs, v.SourcePendingCursorMs = wcPresent(p.fromMS), wcPresent(p.untilMS), wcPresent(p.cursorMS)
	v.ResultStartEventId = wcPresent(p.startEventID)
	v.ResultReportSequence = p.lastAcceptedSequence
	if !e.finished {
		v.ResultPendingFromMs, v.ResultPendingUntilMs, v.ResultPendingCursorMs = wcPresent(p.fromMS), wcPresent(p.untilMS), wcPresent(e.nextCursor)
	}
	v.ResultViewerRevision, v.ResultObservationRevision, v.ResultHistoryRevision, v.ResultPersonalRevision = wcPresent(e.viewerRevision), wcPresent(e.observationRevision), wcPresent(e.historyRevision), wcPresent(e.personalRevision)
	v.ConsumedFromMs, v.ConsumedUntilMs, v.ResultNextCursorMs = wcPresent(e.consumedFrom), wcPresent(e.consumedUntil), wcPresent(e.nextCursor)
	v.ResultRemainingUntilMs = e.remainingUntil
	v.Finished, v.CreditedMs = wcPresent(wc008Bool(e.finished)), wcPresent(e.creditedMS)
	return v
}

// Exact finite existing event vocabulary, no audience fanout. Each parent owns
// all appends and eligible deletions; feeds may wake only after physical return.
func appendWCProgressEvents(ctx context.Context, t *commandTurn, s wcProgressScope, v ProgressValue) error {
	audience := apievents.ProfileAudience("local", s.accountID, s.profileID)
	for _, h := range []struct {
		kind, resource string
		revision       int64
	}{{"personal.changed", "personal", v.ResultPersonalRevision.Int64}, {"progress.changed", "library", v.ResultObservationRevision.Int64}, {"history.changed", "history", v.ResultHistoryRevision.Int64}} {
		data, err := json.Marshal(struct {
			Playback    string `json:"playback"`
			Observation int64  `json:"observation"`
		}{s.playbackID, v.ResultObservationRevision.Int64})
		if err != nil {
			return err
		}
		if _, err = appendWCAPIEvent(ctx, t, wcAPIEvent{audience: audience, eventType: h.kind, resourceKind: h.resource, resourceID: s.libraryKey, revision: strconv.FormatInt(h.revision, 10), data: string(data), atMS: v.AppliedMs}); err != nil {
			return err
		}
	}
	return nil
}
func wcRestoreSnapshot(s wcSnapshot) (out wc008HistorySnapshot, err error) {
	for i, c := range s {
		switch c.Kind {
		case 0:
			out.values[i] = nil
		case 1:
			out.values[i] = c.Integer
		case 2:
			out.values[i] = c.Text
		case 3:
			out.values[i] = append([]byte(nil), c.Blob...)
		default:
			return out, ErrInvalid
		}
	}
	return out, nil
}
