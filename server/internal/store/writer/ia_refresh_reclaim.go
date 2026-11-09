package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Journal retirement is deliberately a bounded, resumable walk, not a cascade.
// One 127 turn removes at most iaRefreshReclaimRowsPerTurn of this journal's
// acknowledged family rows and receipt headers, frees exactly those encoded
// bytes, and saves its progress and result in the permanent owner's fixed slot.
// It never appends a cleanup receipt, never refunds on ACK alone, and never
// deletes a row it has not charged and read back on the same snapshot.
const (
	iaRefreshReclaimSlot         stmt.ID = "ia.refresh.gc.slot"
	iaRefreshReclaimReceiptPage  stmt.ID = "ia.refresh.gc.receipt_page"
	iaRefreshReclaimReceiptErase stmt.ID = "ia.refresh.gc.receipt_erase"
	iaRefreshReclaimAdvance      stmt.ID = "ia.refresh.gc.advance"
	iaRefreshReclaimAdvanceFirst stmt.ID = "ia.refresh.gc.advance_first"
)

func iaRefreshReclaimDeleteID(f uint8) stmt.ID {
	switch f {
	case 1, 2, 3, 4, 5, 6, 7:
		return stmt.ID("ia.refresh.gc.delete" + strconv.Itoa(int(f)))
	}
	return ""
}

// One resumable retirement position. Family 0 is the receipt-header phase;
// families 1-7 are the journal before-image families in dependency order.
type iaRefreshReclaimCursor struct {
	family uint8
	key    string
}

type iaRefreshReclaimProgress struct {
	family         uint8
	key            string
	rows, commands int64
	bytes          int64
	terminal       bool
}

type iaRefreshReclaimReceipt struct {
	sequence int64
	kind     string
	state    string
	key      string
}

// readIARefreshSlot is the closed fixed-slot reader. The slot group is
// all-or-nothing; a partial group can never be served as a saved result.
func readIARefreshSlot(ctx context.Context, turn *commandTurn, refreshID int64, id CommandIdentity) (found receiptFacts, saved iaRefreshResult, digest ResultDigest, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Found  receiptFacts
			Saved  iaRefreshResult
			Digest ResultDigest
		}{found, saved, digest}, err)
		found, saved, digest, err = v.Found, v.Saved, v.Digest, x
	}()
	if turn == nil || turn.transaction == nil || refreshID <= 0 || id.Incarnation == [16]byte{} || id.Sequence <= 0 {
		return found, saved, digest, ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, iaRefreshReclaimSlot, refreshID)
	if err != nil {
		return found, saved, digest, err
	}
	var sequence, applied, family, removedRows, removedBytes, terminal, revision, retainedRows, retainedBytes sql.NullInt64
	var key sql.NullString
	var operation, request, result []byte
	var state string
	err = row.Scan(&sequence, &operation, &request, &result, &applied, &family, &key, &removedRows, &removedBytes, &terminal,
		&state, &revision, &retainedRows, &retainedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return found, saved, digest, nil
	}
	if err != nil {
		return found, saved, digest, basicStageError(err)
	}
	if !sequence.Valid {
		if operation != nil || request != nil || result != nil || applied != (sql.NullInt64{}) || family != (sql.NullInt64{}) ||
			key != (sql.NullString{}) || removedRows != (sql.NullInt64{}) || removedBytes != (sql.NullInt64{}) ||
			terminal != (sql.NullInt64{}) {
			return found, saved, digest, ErrResolveRequired
		}
		return found, saved, digest, nil
	}
	if !applied.Valid || !family.Valid || !key.Valid || !removedRows.Valid || !removedBytes.Valid || !terminal.Valid ||
		!revision.Valid || !retainedRows.Valid || !retainedBytes.Valid ||
		len(operation) != 16 || len(request) != 32 || len(result) != 32 || sequence.Int64 <= 0 || applied.Int64 < 0 ||
		revision.Int64 <= 0 || revision.Int64 == math.MaxInt64 || retainedRows.Int64 < 0 || retainedBytes.Int64 < 0 ||
		state != "accepted" && state != "restored" && state != "retired" ||
		family.Int64 < 0 || family.Int64 > 7 || len(key.String) > 273 || terminal.Int64 != 0 && terminal.Int64 != 1 {
		return found, saved, digest, ErrResolveRequired
	}
	if sequence.Int64 != id.Sequence {
		return found, saved, digest, nil
	}
	found.exists = true
	found.kind = IARefreshFinalize
	found.identity.command.Incarnation = id.Incarnation
	found.identity.command.Sequence = sequence.Int64
	copy(found.identity.command.OperationID[:], operation)
	copy(found.identity.digest[:], request)
	found.resultCount = retainedRows.Int64
	copy(found.resultDigest[:], result)
	copy(digest[:], result)
	saved = iaRefreshResult{refreshID: refreshID, revision: revision.Int64, appliedMS: applied.Int64, state: state,
		family: family.Int64, key: key.String, rows: retainedRows.Int64, bytes: retainedBytes.Int64}
	return found, saved, digest, nil
}

func iaRefreshReclaimStart(f iaRefreshFinishFacts, token string) (out iaRefreshReclaimCursor, err error) {
	if !f.slotPresent() {
		return out, nil
	}
	if f.slotTerminal.Int64 == 1 {
		return out, ErrConflict
	}
	family, key := uint8(f.slotFamily.Int64), f.slotKey.String
	switch family {
	case 0:
		if key != "" {
			n, x := strconv.ParseInt(key, 10, 64)
			if x != nil || n <= 0 || n > f.last {
				return out, ErrResolveRequired
			}
		}
	case 1, 3, 5:
		if key != "" {
			return out, ErrResolveRequired
		}
	case 2, 4, 6, 7:
		if key == "" {
			return out, nil
		}
		k, x := iaRefreshDecodeCursor(family, key)
		if x != nil || k.family != family || (family == 2 || family == 4) && k.token != token || family >= 6 && k.index != 0 {
			return out, ErrResolveRequired
		}
	default:
		return out, ErrResolveRequired
	}
	return iaRefreshReclaimCursor{family, key}, nil
}

// iaRefreshReclaimTurn is the bounded cleanup half of command 127. It is a
// same-turn component: no runtime caller, no new scheduler, no second lease.
func iaRefreshReclaimTurn(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, b iaEntryBudget) (out iaRefreshOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if e == nil || e.request.purpose != IARefreshFinalize || (e.state != "accepted" && e.state != "restored") {
		return out, ErrInvalid
	}
	id := e.request.reference.Identity
	s := e.request.scope
	facts, err := readIARefreshFinishFacts(ctx, turn, e.refreshID, id.Incarnation[:])
	if err != nil {
		return out, err
	}
	if facts.state != e.state || facts.assetID != s.assetID || facts.token != s.token || facts.epoch != s.epoch ||
		facts.inputRevision != s.inputRevision || facts.revision != e.revision || facts.last != e.owner.lastSequence ||
		facts.ack != e.owner.acknowledgedSequence {
		return out, ErrConflict
	}
	// ACK refunds nothing and admits nothing; it only lets the NEXT turn replace
	// the fixed slot. A turn can therefore never overwrite an unacknowledged one.
	if facts.last != facts.ack || facts.slotPresent() && (facts.slotSequence.Int64 != facts.last || facts.ack < facts.slotSequence.Int64) {
		return out, ErrResolveRequired
	}
	// Output/proof pins: the owned publication fence is gone, the asset is still
	// the exact accepted row, and no newer refresh owns this asset's before-images.
	row, err := basicStageRead(ctx, turn, iaRefreshAsset, s.assetID, s.token, s.libraryID, s.epoch)
	if err != nil {
		return out, err
	}
	var pending, playback, prepared, invalidation int64
	if err = row.Scan(&pending, &playback, &prepared, &invalidation); err != nil {
		return out, basicStageError(err)
	}
	if pending != 0 || playback <= 0 || prepared <= 0 || invalidation <= 0 {
		return out, ErrConflict
	}
	if err = iaRefreshNoRows(ctx, turn, iaRefreshFinishActive, s.assetID, e.refreshID); err != nil {
		return out, err
	}
	start, err := iaRefreshReclaimStart(facts, s.token)
	if err != nil {
		return out, err
	}
	progress, err := iaRefreshReclaimQuantum(ctx, turn, e, start, b)
	if err != nil {
		return out, err
	}
	now := turn.transaction.writer.now()
	if err = turn.transaction.observeTime(now); err != nil {
		return out, err
	}
	resultState := facts.state
	if progress.terminal {
		resultState = "retired"
	}
	charge := iaRefreshReceiptCharge(iaRefreshKind(IARefreshFinalize), resultState, progress.key)
	// Each deleted family row and each deleted receipt header was one charged
	// control row in retained_rows; the permanent slot itself stays charged as
	// the one remaining row.
	slotDelta := int64(0)
	if !facts.slotPresent() {
		slotDelta = 1
	}
	rows := facts.rows - progress.rows - progress.commands + slotDelta
	bytes := facts.bytes + charge - progress.bytes
	if rows < 0 || rows > maxCanonicalResultBytes/96 || bytes < 0 || bytes > maxCanonicalResultBytes {
		return out, ErrConflict
	}
	result := iaRefreshResult{refreshID: e.refreshID, revision: facts.revision, appliedMS: now.UnixMilli(), state: resultState,
		family: int64(progress.family), key: progress.key, rows: rows, bytes: bytes}
	digest, err := iaRefreshResultDigest(e.request, result)
	if err != nil {
		return iaRefreshOutcome{}, err
	}
	// Charged bytes and actually freed bytes settle together. ACK alone moved
	// neither, and a refusal rolls both back with the deletions.
	if err = iaRefreshSettleBudget(ctx, turn, charge-progress.bytes); err != nil {
		return iaRefreshOutcome{}, err
	}
	slotSequence := int64(0)
	slotID := iaRefreshReclaimAdvanceFirst
	if facts.slotPresent() {
		slotSequence = facts.slotSequence.Int64
		slotID = iaRefreshReclaimAdvance
	}
	terminal := int64(0)
	if progress.terminal {
		terminal = 1
	}
	args := []any{facts.revision, resultState, id.Sequence, rows, bytes,
		id.Sequence, id.OperationID[:], e.request.reference.RequestDigest[:], digest[:], result.appliedMS,
		int64(progress.family), progress.key, progress.rows, progress.bytes, terminal,
		e.refreshID, id.Incarnation[:], facts.revision, facts.state, facts.last, facts.ack, facts.rows, facts.bytes}
	if facts.slotPresent() {
		args = append(args, slotSequence)
	}
	row, err = basicStageRead(ctx, turn, slotID, args...)
	if err != nil {
		return out, err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return out, basicStageError(err)
	}
	if got != facts.revision {
		return out, ErrConflict
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return iaRefreshOutcome{}, err
	}
	out.result = result
	out.digest = digest
	out.decision = receiptFresh
	return out, nil
}

func iaRefreshReclaimQuantum(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, start iaRefreshReclaimCursor, b iaEntryBudget) (out iaRefreshReclaimProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if e == nil || e.request.purpose != IARefreshFinalize || start.family > 8 {
		return out, ErrInvalid
	}
	family, key := start.family, start.key
	remaining := int64(iaRefreshReclaimRowsPerTurn)
	for remaining > 0 {
		switch family {
		case 0:
			after, x := iaRefreshReclaimReceiptCursor(key)
			if x != nil {
				return out, x
			}
			limit := int(min(remaining, int64(iaRefreshFinishPageLimit)))
			receipts, x := iaRefreshReclaimReceipts(ctx, turn, e, after, limit)
			if x != nil {
				return out, x
			}
			if len(receipts) == 0 {
				family, key = 1, ""
				continue
			}
			for _, r := range receipts {
				if x = iaRefreshReclaimReceiptDrop(ctx, turn, e, r.sequence); x != nil {
					return out, x
				}
				out.bytes += iaRefreshReceiptCharge(r.kind, r.state, r.key)
				out.commands++
				remaining--
			}
			if len(receipts) == limit {
				out.family, out.key = 0, strconv.FormatInt(receipts[len(receipts)-1].sequence, 10)
				return out, nil
			}
			family, key = 1, ""
		case 1, 3, 5:
			k := iaRefreshKey{family: family, token: e.request.scope.token}
			charge, exists, x := iaRefreshReclaimCharge(ctx, turn, e, k, b)
			if x != nil {
				return out, x
			}
			if exists {
				if x = iaRefreshReclaimDrop(ctx, turn, e, k); x != nil {
					return out, x
				}
				out.bytes += charge
				out.rows++
				remaining--
			}
			family, key = family+1, ""
		case 2, 4, 6, 7:
			below, after := int64(-1), ""
			if key != "" {
				decoded, x := iaRefreshDecodeCursor(family, key)
				if x != nil || decoded.family != family || (family == 2 || family == 4) && decoded.token != e.request.scope.token || family >= 6 && decoded.index != 0 {
					return out, ErrResolveRequired
				}
				below, after = decoded.index, decoded.token
			}
			limit := int(min(remaining, int64(iaRefreshFinishPageLimit)))
			var keys []iaRefreshKey
			var x error
			if family == 2 || family == 4 {
				keys, x = iaRefreshFinishPairPage(ctx, turn, e, family, below, limit, b)
			} else {
				keys, x = iaRefreshFinishIDPage(ctx, turn, e, family, after, limit, b)
			}
			if x != nil {
				return out, x
			}
			if len(keys) == 0 {
				family, key = family+1, ""
				continue
			}
			for _, k := range keys {
				charge, exists, x := iaRefreshReclaimCharge(ctx, turn, e, k, b)
				if x != nil {
					return out, x
				}
				if !exists {
					return out, ErrResolveRequired
				}
				if x = iaRefreshReclaimDrop(ctx, turn, e, k); x != nil {
					return out, x
				}
				out.bytes += charge
				out.rows++
				remaining--
			}
			if len(keys) == limit {
				out.family, out.key = family, keys[len(keys)-1].cursor()
				return out, nil
			}
			family, key = family+1, ""
		case 8:
			out.family, out.key, out.terminal = 7, "", true
			return out, nil
		default:
			return out, ErrResolveRequired
		}
	}
	out.family, out.key = family, key
	return out, nil
}

func iaRefreshReclaimReceiptCursor(key string) (int64, error) {
	if key == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(key, 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrResolveRequired
	}
	return n, nil
}

func iaRefreshReclaimReceipts(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, after int64, limit int) (out []iaRefreshReclaimReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if after < 0 || limit <= 0 || limit > iaRefreshFinishPageLimit {
		return nil, ErrInvalid
	}
	rows, err := turn.query(ctx, turn.steps[iaRefreshReclaimReceiptPage].statementStep,
		e.refreshID, e.request.reference.Identity.Incarnation[:], after, int64(limit))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r iaRefreshReclaimReceipt
		if err = rows.Scan(&r.sequence, &r.kind, &r.state, &r.key); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		kind := map[string]CommandID{"refresh_begin": IARefreshBegin, "refresh_step": IARefreshStep,
			"refresh_rollback": IARefreshRollback, "refresh_finalize": IARefreshFinalize}[r.kind]
		if kind == 0 || iaRefreshKind(kind) != r.kind || len(r.key) > 273 || r.sequence <= after {
			return nil, errors.Join(ErrResolveRequired, rows.Close())
		}
		out = append(out, r)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return out, nil
}

func iaRefreshReclaimReceiptDrop(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, sequence int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if sequence <= 0 {
		return ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, iaRefreshReclaimReceiptErase, e.refreshID, e.request.reference.Identity.Incarnation[:], sequence)
	if err != nil {
		return err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return basicStageError(err)
	}
	if got != sequence {
		return ErrConflict
	}
	return nil
}

// iaRefreshReclaimCharge recomputes the exact custody bytes this journal charged
// for one before-image row. The digest is over the same typed image IA20 charged,
// so a freed-byte refund is measured, never guessed.
func iaRefreshReclaimCharge(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, k iaRefreshKey, b iaEntryBudget) (charge int64, exists bool, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Charge int64
			Exists bool
		}{charge, exists}, err)
		charge, exists, err = v.Charge, v.Exists, x
	}()
	if !k.valid() {
		return 0, false, ErrInvalid
	}
	if err = b.check(ctx, turn, iaRefreshKeyAllowance); err != nil {
		return 0, false, err
	}
	old, _, _, exists, err := iaRefreshFinishRead(ctx, turn, e, k, true)
	if err != nil || !exists {
		return 0, false, err
	}
	_, charge, err = iaRefreshImageDigest(k, old)
	return charge, true, err
}

func iaRefreshReclaimDrop(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, k iaRefreshKey) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	id := iaRefreshReclaimDeleteID(k.family)
	if !k.valid() || id == "" {
		return ErrInvalid
	}
	args := append([]any{e.refreshID}, k.args()...)
	row, err := basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return basicStageError(err)
	}
	if got != e.refreshID {
		return ErrConflict
	}
	return nil
}

// iaRefreshSettleBudget applies the net charge (this turn's retained result minus
// the bytes actually freed) with one CAS. A negative net refund never exceeds the
// current charge.
func iaRefreshSettleBudget(ctx context.Context, turn *commandTurn, delta int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return basicStageError(err)
	}
	if used < 0 || cap < 0 || used > cap {
		return ErrConflict
	}
	next := used + delta
	if delta > 0 && delta > cap-used {
		return &Refusal{Code: RefusalCapacity}
	}
	if next < 0 || next > cap {
		return ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, next, int64(1), used, cap)
	if err != nil {
		return err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return basicStageError(err)
	}
	if got != next {
		return ErrConflict
	}
	return nil
}

func iaRefreshReclaimDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRefreshReclaimSlot, Kind: stmt.Read,
			SQL:    `SELECT reclaim_command_sequence,reclaim_operation_id,reclaim_request_digest,reclaim_result_digest,reclaim_applied_ms,reclaim_after_family,reclaim_after_key,reclaim_removed_rows,reclaim_removed_bytes,reclaim_terminal,state,revision,retained_rows,retained_bytes FROM catalog_info_refresh_undo WHERE refresh_id=?`,
			Params: []stmt.Param{i("refresh")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}},
			Cohorts: []stmt.Cohort{{Name: "reclaim_fixed_slot", Bindings: []any{int64(1)}}}},
		{ID: iaRefreshReclaimReceiptPage, Kind: stmt.Read,
			SQL:     `SELECT command_sequence,command_kind,result_state,after_key FROM catalog_info_refresh_receipts WHERE refresh_id=? AND command_incarnation=? AND command_sequence>? ORDER BY command_sequence LIMIT ?`,
			Params:  []stmt.Param{i("refresh"), b("incarnation"), i("after"), i("limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "acknowledged_receipt_cohort", Bindings: []any{int64(1), make([]byte, 16), int64(0), int64(65)}}}},
		{ID: iaRefreshReclaimReceiptErase, Kind: stmt.Write,
			SQL:     `DELETE FROM catalog_info_refresh_receipts WHERE refresh_id=? AND command_incarnation=? AND command_sequence=? RETURNING command_sequence`,
			Params:  []stmt.Param{i("refresh"), b("incarnation"), i("sequence")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "exact_acknowledged_receipt", Bindings: []any{int64(1), make([]byte, 16), int64(1)}}}},
		{ID: iaRefreshReclaimAdvance, Kind: stmt.Write,
			SQL: `UPDATE catalog_info_refresh_undo SET revision=?,state=?,last_command_sequence=?,retained_rows=?,retained_bytes=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_result_digest=?,reclaim_applied_ms=?,reclaim_after_family=?,reclaim_after_key=?,reclaim_removed_rows=?,reclaim_removed_bytes=?,reclaim_terminal=? WHERE refresh_id=? AND command_incarnation=? AND revision=? AND state=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retained_rows=? AND retained_bytes=? AND reclaim_command_sequence=? RETURNING revision`,
			Params: []stmt.Param{i("revision"), t("state"), i("sequence"), i("rows"), i("bytes"), i("reclaim_sequence"),
				b("operation"), b("request"), b("digest"), i("applied"), i("family"), t("key"), i("removed_rows"),
				i("removed_bytes"), i("terminal"), i("refresh"), b("incarnation"), i("old_revision"), t("old_state"),
				i("old_last"), i("old_ack"), i("old_rows"), i("old_bytes"), i("old_slot")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}},
			Cohorts: []stmt.Cohort{{Name: "bounded_retirement_turn", Bindings: []any{int64(2), "accepted", int64(9),
				int64(3), int64(400), int64(9), make([]byte, 16), make([]byte, 32), make([]byte, 32), int64(0),
				int64(0), "", int64(1), int64(100), int64(0), int64(1), make([]byte, 16), int64(1), "accepted",
				int64(8), int64(8), int64(3), int64(400), int64(8)}}}},
		{ID: iaRefreshReclaimAdvanceFirst, Kind: stmt.Write,
			SQL: `UPDATE catalog_info_refresh_undo SET revision=?,state=?,last_command_sequence=?,retained_rows=?,retained_bytes=?,reclaim_command_sequence=?,reclaim_operation_id=?,reclaim_request_digest=?,reclaim_result_digest=?,reclaim_applied_ms=?,reclaim_after_family=?,reclaim_after_key=?,reclaim_removed_rows=?,reclaim_removed_bytes=?,reclaim_terminal=? WHERE refresh_id=? AND command_incarnation=? AND revision=? AND state=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND retained_rows=? AND retained_bytes=? AND reclaim_command_sequence IS NULL RETURNING revision`,
			Params: []stmt.Param{i("revision"), t("state"), i("sequence"), i("rows"), i("bytes"), i("reclaim_sequence"),
				b("operation"), b("request"), b("digest"), i("applied"), i("family"), t("key"), i("removed_rows"),
				i("removed_bytes"), i("terminal"), i("refresh"), b("incarnation"), i("old_revision"), t("old_state"),
				i("old_last"), i("old_ack"), i("old_rows"), i("old_bytes")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}},
			Cohorts: []stmt.Cohort{{Name: "first_retirement_turn", Bindings: []any{int64(2), "accepted", int64(9),
				int64(3), int64(400), int64(9), make([]byte, 16), make([]byte, 32), make([]byte, 32), int64(0),
				int64(0), "", int64(1), int64(100), int64(0), int64(1), make([]byte, 16), int64(1), "accepted",
				int64(8), int64(8), int64(3), int64(400)}}}},
	}
	tables := map[uint8][]string{
		1: {"catalog_info_undo_asset_stream_facts", "asset_id"},
		2: {"catalog_info_undo_asset_streams", "asset_id", "stream_index"},
		3: {"catalog_info_undo_asset_chapter_facts", "asset_id"},
		4: {"catalog_info_undo_asset_chapters", "asset_id", "chapter_index"},
		5: {"catalog_info_undo_asset_subtitle_facts", "asset_id"},
		6: {"catalog_info_undo_subtitle_sidecars", "id"},
		7: {"catalog_info_undo_asset_subtitles", "id"},
	}
	for f := uint8(1); f <= 7; f++ {
		spec := tables[f]
		sqlText := "DELETE FROM " + spec[0] + " WHERE refresh_id=?"
		params := []stmt.Param{i("refresh")}
		bindings := []any{int64(1)}
		for _, name := range spec[1:] {
			sqlText += " AND " + name + "=?"
			if name == "asset_id" || name == "id" {
				params = append(params, t("key_"+name))
				bindings = append(bindings, "refresh-asset")
			} else {
				params = append(params, i("key_"+name))
				bindings = append(bindings, int64(1))
			}
		}
		sqlText += " RETURNING refresh_id"
		defs = append(defs, stmt.Definition{ID: iaRefreshReclaimDeleteID(f), Kind: stmt.Write, SQL: sqlText,
			Params: params, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{spec[0]}},
			Cohorts: []stmt.Cohort{{Name: "exact_journalled_row", Bindings: bindings}}})
	}
	for j := range defs {
		defs[j].Owner = "store/writer IA refresh reclaim"
		defs[j].CallSites = []string{"store/writer/ia_refresh_reclaim.go"}
	}
	return defs
}
