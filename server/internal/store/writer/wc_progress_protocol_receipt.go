package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// ProgressValue is the COMPLETE immutable native receipt, including original
// source and NULL witnesses. Current owner state never reconstructs this result.
type ProgressValue struct {
	CommandKind                   string
	ResultVersion                 int64
	AppliedMs                     int64
	SourceAcceptedOrdinal         int64
	SourceLastAcceptedSequence    int64
	SourceReportSequence          int64
	SourceOccurrenceGeneration    int64
	SourceActivityGeneration      int64
	SourceHistoryPolicyGeneration int64
	SourceClockIncarnation        [16]byte
	SourceStartEventId            sql.NullInt64
	SourcePendingFromMs           sql.NullInt64
	SourcePendingUntilMs          sql.NullInt64
	SourcePendingCursorMs         sql.NullInt64
	ResultStartEventId            sql.NullInt64
	ResultReportSequence          int64
	ResultPendingSequence         int64
	ResultPendingFromMs           sql.NullInt64
	ResultPendingUntilMs          sql.NullInt64
	ResultPendingCursorMs         sql.NullInt64
	ResultViewerRevision          sql.NullInt64
	ResultObservationRevision     sql.NullInt64
	ResultHistoryRevision         sql.NullInt64
	ResultPersonalRevision        sql.NullInt64
	CaptureCreatedStart           sql.NullInt64
	ConsumedFromMs                sql.NullInt64
	ConsumedUntilMs               sql.NullInt64
	ResultNextCursorMs            sql.NullInt64
	ResultRemainingUntilMs        sql.NullInt64
	Finished                      sql.NullInt64
	CreditedMs                    sql.NullInt64
	ResultCount                   int64
}
type ProgressResult struct {
	// A separate internal physical floor turn, never an Applied receipt.
	RetainedAcknowledgment *AckResult
	Outcome                ReceiptOutcome
	Value                  ProgressValue
	Digest                 ResultDigest
	Snapshot               ProgressSnapshot
}
type wcProgressLookup struct {
	owner    receiptOwnerFacts
	decision receiptDecision
	value    ProgressValue
	digest   ResultDigest
	snapshot ProgressSnapshot
}

func preflightProgressValue(v ProgressValue) error {
	if (v.CommandKind != "capture_report" && v.CommandKind != "drain_piece") || v.ResultVersion != 1 || v.AppliedMs < 0 || v.SourceAcceptedOrdinal < 0 || v.SourceLastAcceptedSequence < -1 || v.SourceReportSequence < 0 || v.SourceOccurrenceGeneration <= 0 || v.SourceActivityGeneration <= 0 || v.SourceHistoryPolicyGeneration <= 0 || v.SourceClockIncarnation == [16]byte{} || v.ResultCount != 1 {
		return ErrResolveRequired
	}
	for _, x := range []sql.NullInt64{v.SourceStartEventId, v.SourcePendingFromMs, v.SourcePendingUntilMs, v.SourcePendingCursorMs, v.ResultStartEventId, v.ResultPendingFromMs, v.ResultPendingUntilMs, v.ResultPendingCursorMs, v.ResultViewerRevision, v.ResultObservationRevision, v.ResultHistoryRevision, v.ResultPersonalRevision, v.CaptureCreatedStart, v.ConsumedFromMs, v.ConsumedUntilMs, v.ResultNextCursorMs, v.ResultRemainingUntilMs, v.Finished, v.CreditedMs} {
		if !x.Valid && x.Int64 != 0 || x.Valid && x.Int64 < 0 {
			return ErrResolveRequired
		}
	}
	if !v.ResultStartEventId.Valid || v.ResultStartEventId.Int64 <= 0 || !v.ResultPersonalRevision.Valid || v.ResultPersonalRevision.Int64 <= 0 || !v.ResultViewerRevision.Valid || !v.ResultObservationRevision.Valid || v.ResultObservationRevision.Int64 <= 0 || !v.ResultHistoryRevision.Valid || v.ResultHistoryRevision.Int64 <= 0 {
		return ErrResolveRequired
	}
	if v.SourceStartEventId.Valid && v.SourceStartEventId != v.ResultStartEventId {
		return ErrResolveRequired
	}
	for _, r := range [][3]sql.NullInt64{{v.SourcePendingFromMs, v.SourcePendingUntilMs, v.SourcePendingCursorMs}, {v.ResultPendingFromMs, v.ResultPendingUntilMs, v.ResultPendingCursorMs}} {
		if r[0].Valid != r[1].Valid || r[0].Valid != r[2].Valid || r[0].Valid && (r[0].Int64 < 0 || r[1].Int64 <= r[0].Int64 || r[2].Int64 < r[0].Int64 || r[2].Int64 >= r[1].Int64) {
			return ErrResolveRequired
		}
	}
	if v.CommandKind == "capture_report" {
		if !v.CaptureCreatedStart.Valid || v.CaptureCreatedStart.Int64 > 1 || v.SourceStartEventId.Valid && v.CaptureCreatedStart.Int64 != 0 || v.SourcePendingFromMs.Valid || !v.ResultPendingFromMs.Valid || v.SourceReportSequence <= v.SourceLastAcceptedSequence || v.ResultReportSequence != v.SourceReportSequence || v.ResultPendingSequence != v.SourceReportSequence || v.ConsumedFromMs.Valid || v.ConsumedUntilMs.Valid || v.ResultNextCursorMs.Valid || v.ResultRemainingUntilMs.Valid || v.Finished.Valid || v.CreditedMs.Valid {
			return ErrResolveRequired
		}
	} else {
		if v.CaptureCreatedStart.Valid || !v.SourceStartEventId.Valid || !v.SourcePendingFromMs.Valid || v.ResultReportSequence != v.SourceLastAcceptedSequence || v.ResultPendingSequence != v.SourceReportSequence || !v.ConsumedFromMs.Valid || !v.ConsumedUntilMs.Valid || !v.ResultNextCursorMs.Valid || !v.Finished.Valid || !v.CreditedMs.Valid {
			return ErrResolveRequired
		}
		from, until := v.ConsumedFromMs.Int64, v.ConsumedUntilMs.Int64
		if from != v.SourcePendingCursorMs.Int64 || until <= from || until > v.SourcePendingUntilMs.Int64 || from/wcQuarterMS != (until-1)/wcQuarterMS || until-from != v.CreditedMs.Int64 || v.ResultNextCursorMs.Int64 != until {
			return ErrResolveRequired
		}
		want := v.SourcePendingUntilMs.Int64
		if step := wcQuarterMS - from%wcQuarterMS; step < want-from {
			want = from + step
		}
		if until != want {
			return ErrResolveRequired
		}
		finished := until == v.SourcePendingUntilMs.Int64
		if v.Finished.Int64 != wc008Bool(finished) || v.ResultRemainingUntilMs.Valid == finished || v.ResultPendingFromMs.Valid == finished || !finished && (v.ResultRemainingUntilMs.Int64 != v.SourcePendingUntilMs.Int64 || v.ResultPendingFromMs != v.SourcePendingFromMs || v.ResultPendingUntilMs != v.SourcePendingUntilMs || v.ResultPendingCursorMs.Int64 != until) {
			return ErrResolveRequired
		}
	}
	return nil
}
func writeProgressValue(e *requestEncoder, id CommandIdentity, request RequestDigest, v ProgressValue) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("progress_accept")
	e.text(v.CommandKind)
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.CommandKind)
	e.number(uint64(v.ResultVersion))
	e.number(uint64(v.AppliedMs))
	e.number(uint64(v.SourceAcceptedOrdinal))
	e.number(uint64(v.SourceLastAcceptedSequence))
	e.number(uint64(v.SourceReportSequence))
	e.number(uint64(v.SourceOccurrenceGeneration))
	e.number(uint64(v.SourceActivityGeneration))
	e.number(uint64(v.SourceHistoryPolicyGeneration))
	e.bytes(v.SourceClockIncarnation[:])
	e.flag(v.SourceStartEventId.Valid)
	if v.SourceStartEventId.Valid {
		e.number(uint64(v.SourceStartEventId.Int64))
	}
	e.flag(v.SourcePendingFromMs.Valid)
	if v.SourcePendingFromMs.Valid {
		e.number(uint64(v.SourcePendingFromMs.Int64))
	}
	e.flag(v.SourcePendingUntilMs.Valid)
	if v.SourcePendingUntilMs.Valid {
		e.number(uint64(v.SourcePendingUntilMs.Int64))
	}
	e.flag(v.SourcePendingCursorMs.Valid)
	if v.SourcePendingCursorMs.Valid {
		e.number(uint64(v.SourcePendingCursorMs.Int64))
	}
	e.flag(v.ResultStartEventId.Valid)
	if v.ResultStartEventId.Valid {
		e.number(uint64(v.ResultStartEventId.Int64))
	}
	e.number(uint64(v.ResultReportSequence))
	e.number(uint64(v.ResultPendingSequence))
	e.flag(v.ResultPendingFromMs.Valid)
	if v.ResultPendingFromMs.Valid {
		e.number(uint64(v.ResultPendingFromMs.Int64))
	}
	e.flag(v.ResultPendingUntilMs.Valid)
	if v.ResultPendingUntilMs.Valid {
		e.number(uint64(v.ResultPendingUntilMs.Int64))
	}
	e.flag(v.ResultPendingCursorMs.Valid)
	if v.ResultPendingCursorMs.Valid {
		e.number(uint64(v.ResultPendingCursorMs.Int64))
	}
	e.flag(v.ResultViewerRevision.Valid)
	if v.ResultViewerRevision.Valid {
		e.number(uint64(v.ResultViewerRevision.Int64))
	}
	e.flag(v.ResultObservationRevision.Valid)
	if v.ResultObservationRevision.Valid {
		e.number(uint64(v.ResultObservationRevision.Int64))
	}
	e.flag(v.ResultHistoryRevision.Valid)
	if v.ResultHistoryRevision.Valid {
		e.number(uint64(v.ResultHistoryRevision.Int64))
	}
	e.flag(v.ResultPersonalRevision.Valid)
	if v.ResultPersonalRevision.Valid {
		e.number(uint64(v.ResultPersonalRevision.Int64))
	}
	e.flag(v.CaptureCreatedStart.Valid)
	if v.CaptureCreatedStart.Valid {
		e.number(uint64(v.CaptureCreatedStart.Int64))
	}
	e.flag(v.ConsumedFromMs.Valid)
	if v.ConsumedFromMs.Valid {
		e.number(uint64(v.ConsumedFromMs.Int64))
	}
	e.flag(v.ConsumedUntilMs.Valid)
	if v.ConsumedUntilMs.Valid {
		e.number(uint64(v.ConsumedUntilMs.Int64))
	}
	e.flag(v.ResultNextCursorMs.Valid)
	if v.ResultNextCursorMs.Valid {
		e.number(uint64(v.ResultNextCursorMs.Int64))
	}
	e.flag(v.ResultRemainingUntilMs.Valid)
	if v.ResultRemainingUntilMs.Valid {
		e.number(uint64(v.ResultRemainingUntilMs.Int64))
	}
	e.flag(v.Finished.Valid)
	if v.Finished.Valid {
		e.number(uint64(v.Finished.Int64))
	}
	e.flag(v.CreditedMs.Valid)
	if v.CreditedMs.Valid {
		e.number(uint64(v.CreditedMs.Int64))
	}
	e.number(uint64(v.ResultCount))
	return e.err
}

const wcProgressReceiptLookup stmt.ID = "wc.progress.protocol_receipt"

func wcProgressReceiptDefinition() stmt.Definition {
	return stmt.Definition{ID: wcProgressReceiptLookup, Owner: "store/writer WC progress protocol", CallSites: []string{"store/writer/wc_progress_protocol_receipt.go"}, Kind: stmt.Read,
		SQL:    `SELECT operation_id,request_digest,command_kind,result_version,applied_ms,source_accepted_ordinal,source_last_accepted_sequence,source_report_sequence,source_occurrence_generation,source_activity_generation,source_history_policy_generation,source_clock_incarnation,source_start_event_id,source_pending_from_ms,source_pending_until_ms,source_pending_cursor_ms,result_start_event_id,result_report_sequence,result_pending_sequence,result_pending_from_ms,result_pending_until_ms,result_pending_cursor_ms,result_viewer_revision,result_observation_revision,result_history_revision,result_personal_revision,capture_created_start,consumed_from_ms,consumed_until_ms,result_next_cursor_ms,result_remaining_until_ms,finished,credited_ms,result_count,result_digest,variable_bytes FROM playback_claim_command_receipts WHERE playback_id=? AND command_incarnation=? AND command_sequence=?`,
		Params: []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "exact_original", Bindings: []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"playback_claim_command_receipts"}}}
}
func lookupWCProgress(ctx context.Context, turn *commandTurn, v *wcProgressInvocation, s ProgressSnapshot) (out wcProgressLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out.snapshot = s
	out.owner = receiptOwnerFacts{true, v.disclosure.driver.scope.incarnation, s.Last, s.Acknowledged}
	expected := receiptIdentity{v.request.Receipt.Identity, v.request.Receipt.RequestDigest}
	if expected.command.Sequence <= s.Acknowledged {
		out.decision, err = classifyReceipt(expected, ProgressAccept, out.owner, receiptFacts{})
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, wcProgressReceiptLookup, v.request.PlaybackID, expected.command.Incarnation[:], expected.command.Sequence)
	if err != nil {
		return out, err
	}
	var operation, request, clock, digest []byte
	var n int64
	r := &out.value
	err = row.Scan(&operation, &request, &r.CommandKind, &r.ResultVersion, &r.AppliedMs, &r.SourceAcceptedOrdinal, &r.SourceLastAcceptedSequence, &r.SourceReportSequence, &r.SourceOccurrenceGeneration, &r.SourceActivityGeneration, &r.SourceHistoryPolicyGeneration, &clock, &r.SourceStartEventId, &r.SourcePendingFromMs, &r.SourcePendingUntilMs, &r.SourcePendingCursorMs, &r.ResultStartEventId, &r.ResultReportSequence, &r.ResultPendingSequence, &r.ResultPendingFromMs, &r.ResultPendingUntilMs, &r.ResultPendingCursorMs, &r.ResultViewerRevision, &r.ResultObservationRevision, &r.ResultHistoryRevision, &r.ResultPersonalRevision, &r.CaptureCreatedStart, &r.ConsumedFromMs, &r.ConsumedUntilMs, &r.ResultNextCursorMs, &r.ResultRemainingUntilMs, &r.Finished, &r.CreditedMs, &r.ResultCount, &digest, &n)
	found := receiptFacts{}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		if len(operation) != 16 || len(request) != 32 || len(clock) != 16 || len(digest) != 32 || n <= 0 {
			return out, ErrResolveRequired
		}
		copy(r.SourceClockIncarnation[:], clock)
		copy(out.digest[:], digest)
		actual := receiptIdentity{command: CommandIdentity{Incarnation: expected.command.Incarnation, Sequence: expected.command.Sequence}}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found = receiptFacts{exists: true, identity: actual, kind: ProgressAccept, resultCount: r.ResultCount, resultDigest: out.digest}
	}
	out.decision, err = classifyReceipt(expected, ProgressAccept, out.owner, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		if r.CommandKind != v.request.Kind {
			return out, ErrConflict
		}
		computed, e := canonicalResultDigest(expected.command, expected.digest, *r)
		if e != nil || !bytes.Equal(computed[:], out.digest[:]) {
			return out, errors.Join(ErrResolveRequired, e)
		}
	}
	return out, nil
}
