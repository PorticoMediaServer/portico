package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	scanRunHistoryOwner stmt.ID = "scan.run_lifecycle.receipt_owner"
	scanRunHistoryBody  stmt.ID = "scan.run_lifecycle.receipt_body"
)

// These two points compose with the existing scan header, ACK and exact-key
// cleanup definitions. There is no alternate registry, callback or SQL handle.
// Current opaque/native library disclosure precedes this protocol. Source,
// root, retirement, due time and lease freshness never filter saved history.
func scanRunLifecycleHistoryDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	blob := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	inc := make([]byte, 16)
	return []stmt.Definition{
		{ID: scanRunHistoryOwner, Owner: "store/writer lifecycle protocol", CallSites: []string{"store/writer/scan_lifecycle_history.go"}, Kind: stmt.Read,
			SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,
original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,
create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode
FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{text("run_id")}, Cohorts: scanReceiptReadCohorts(scanReceiptOwner, inc),
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}}},
		{ID: scanRunHistoryBody, Owner: "store/writer lifecycle protocol", CallSites: []string{"store/writer/scan_lifecycle_history.go"}, Kind: stmt.Read,
			SQL: `SELECT result_lease_owner,result_lease_generation,result_lease_expires_ms,
result_acknowledged_sequence,result_discovered,result_processed,result_root_identity,result_root_revision,result_root_cursor,result_root_pages,result_root_skipped,
change_id,next_family,next_copy_phase,next_copy_id,next_copy_aux,next_copy_key,next_copy_key2,next_copy_key3,after_entity,expected_epoch,
refused_relative_path,refusal_code,cursor_kind,cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence")},
			Cohorts: append(scanReceiptReadCohorts(scanReceiptLookup, inc), stmt.Cohort{Name: "claim_second_sequence", Bindings: []any{"scan-replay", inc, int64(2)}}),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}}},
	}
}

type scanLifecycleReceiptLookup struct {
	owner    receiptOwnerFacts
	decision receiptDecision
	result   scanRunLifecycleResult
	digest   ResultDigest
}

// This is private same-turn protocol data. Neither a complete Capture receipt
// nor successful decoding supplies authority, byte budgets or a fresh profile.
// Native row/copy and complete original/result memory must be admitted by the
// sole composition before calling; these point predicates are not byte bounds.
func lookupScanRunLifecycle(ctx context.Context, turn *commandTurn, q ScanReceiptRequest, kind CommandID) (value scanLifecycleReceiptLookup, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || (kind != InventoryCreate && kind != InventoryClaim) ||
		turn.transaction.descriptor.ID != kind || q.RunID == "" || q.LibraryID <= 0 || q.LibraryKey == "" || q.Receipt.Identity.Sequence <= 0 ||
		len(q.RunID) > maxCanonicalRequestBytes || len(q.LibraryKey) > maxCanonicalRequestBytes-len(q.RunID) ||
		!utf8.ValidString(q.RunID) || !utf8.ValidString(q.LibraryKey) {
		return value, ErrInvalid
	}
	for _, id := range []stmt.ID{scanRunHistoryOwner, scanReceiptLookup, scanRunHistoryBody, scanReceiptReclaimKey} {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("scan_lifecycle_history_steps")
		}
	}
	proof, err := readScanLifecycleByteProof(ctx, turn, q, kind)
	if err != nil {
		return value, err
	}
	if proof.decision != receiptReplay {
		if proof.decision == receiptFresh && kind == InventoryCreate {
			return value, ErrResolveRequired
		}
		return scanLifecycleReceiptLookup{owner: proof.prefix.owner, decision: proof.decision}, nil
	}
	// Every compiled point must yield exactly zero or one row, including
	// driver/projection fault controls; never silently discard a second row.
	scanPoint := func(rows *commandRows, dest ...any) error {
		if rows == nil {
			return ErrInvalid
		}
		var err error
		if !rows.Next() {
			err = rows.Err()
			if err == nil {
				err = sql.ErrNoRows
			}
		} else {
			err = rows.Scan(dest...)
			if err == nil && rows.Next() {
				err = ErrResolveRequired
			}
		}
		return errors.Join(err, rows.Err(), rows.Close())
	}
	var owner receiptOwnerFacts
	var original scanRunOriginalScope
	var inc, createOperation, createDigest []byte
	var follow int64
	original.RunID = q.RunID
	row, err := turn.query(ctx, turn.steps[scanRunHistoryOwner].statementStep, q.RunID)
	if err != nil {
		return value, err
	}
	err = scanPoint(row, &inc, &owner.lastSequence, &owner.acknowledgedSequence,
		&original.LibraryID, &original.LibraryKey, &original.ResolvedRoot, &follow,
		&createOperation, &createDigest, &original.SourceID, &original.SourceGeneration, &original.SourceIncarnation,
		&original.RootIdentity, &original.PolicyRevision, &original.ScanMode)
	expected := receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = classifyReceipt(expected, kind, owner, receiptFacts{})
		return value, err // a missing old owner can NEVER acquire birth authority
	}
	if err != nil {
		return value, err
	}
	if len(inc) != 16 || len(createOperation) != 16 || len(createDigest) != 32 || (follow != 0 && follow != 1) {
		return value, ErrResolveRequired
	}
	if original.LibraryID != q.LibraryID || original.LibraryKey != q.LibraryKey {
		return value, ErrDisclosureDenied
	}
	owner.exists = true
	copy(owner.incarnation[:], inc)
	copy(original.CreateOperationID[:], createOperation)
	copy(original.CreateRequestDigest[:], createDigest)
	original.FollowSymlinks = follow == 1
	if owner != proof.prefix.owner || original.CreateOperationID != proof.prefix.createOperation || original.CreateRequestDigest != proof.prefix.createDigest ||
		!proof.run.matchesHistoryOriginal(original, inc) {
		return value, ErrConflict
	}

	if owner.incarnation != q.Receipt.Identity.Incarnation {
		_, err = classifyReceipt(expected, kind, owner, receiptFacts{})
		return value, err
	}
	// Creation's retained anchor detects conflicts even after its ACK deleted
	// the scalar header. It does not reconstruct an Applied result.
	if kind == InventoryCreate && (q.Receipt.Identity.Sequence != 1 || original.CreateOperationID != q.Receipt.Identity.OperationID || original.CreateRequestDigest != q.Receipt.RequestDigest) {
		return value, ErrConflict
	}
	row, err = turn.query(ctx, turn.steps[scanReceiptLookup].statementStep, q.RunID, inc, q.Receipt.Identity.Sequence)
	if err != nil {
		return value, err
	}
	var operation, request, savedDigest []byte
	var savedKind, sourceID, sourceIncarnation, nextCursor string
	var sourceRevision sql.NullString
	var count int64
	v := scanRunLifecycleResult{Original: original}
	err = scanPoint(row, &operation, &request, &savedKind, &v.AcceptedRunRevision, &v.AppliedMS,
		&sourceID, &sourceIncarnation, &sourceRevision, &nextCursor, &count, &savedDigest)
	var facts receiptFacts
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(savedDigest) != 32 {
			return value, ErrResolveRequired
		}
		facts.exists, facts.resultCount = true, count
		facts.identity.command = q.Receipt.Identity
		copy(facts.identity.command.OperationID[:], operation)
		copy(facts.identity.digest[:], request)
		copy(facts.resultDigest[:], savedDigest)
		switch savedKind {
		case "inventory_create":
			facts.kind = InventoryCreate
		case "inventory_claim":
			facts.kind = InventoryClaim
		case scanInventoryReceiptKind:
			facts.kind = InventoryAccept
		}
	}
	if err != nil {
		return value, err
	}
	decision, err := classifyReceipt(expected, kind, owner, facts)
	if err != nil {
		return value, err
	}
	if decision == receiptFresh && kind == InventoryCreate {
		return value, ErrResolveRequired // partial EXISTING owner is not a birth miss
	}
	out := scanLifecycleReceiptLookup{owner: owner, decision: decision}
	if decision != receiptReplay {
		return out, nil // floor FIRST, including a physically drained scalar body
	}
	if !scanReceiptIdentityMatches(proof.facts, operation, request, savedDigest, savedKind, count) ||
		!proof.header.matchesInventoryHeader(operation, request, savedKind, sourceID, sourceIncarnation, sourceRevision, nextCursor, savedDigest) {
		return value, ErrResolveRequired
	}
	if sourceRevision.Valid || nextCursor != "" || count != 0 || sourceID != original.SourceID || sourceIncarnation != original.SourceIncarnation {
		return value, ErrResolveRequired
	}
	row, err = turn.query(ctx, turn.steps[scanRunHistoryBody].statementStep, q.RunID, inc, q.Receipt.Identity.Sequence)
	if err != nil {
		return value, err
	}
	var numbers [8]sql.NullInt64
	var texts [3]sql.NullString
	var change sql.NullInt64
	var family, phase, id, aux, afterEntity, epoch int64
	var key, key2, key3 string
	var refusal [8]any
	err = scanPoint(row, &numbers[0], &numbers[1], &numbers[2], &numbers[3], &numbers[4], &numbers[5], &texts[0], &texts[1], &texts[2], &numbers[6], &numbers[7],
		&change, &family, &phase, &id, &aux, &key, &key2, &key3, &afterEntity, &epoch,
		&refusal[0], &refusal[1], &refusal[2], &refusal[3], &refusal[4], &refusal[5], &refusal[6], &refusal[7])
	if err != nil {
		return value, errors.Join(ErrResolveRequired, err)
	}
	if !proof.header.matches(operation, request, savedKind, sourceID, sourceIncarnation, sourceRevision, nextCursor, savedDigest,
		texts[0], texts[1], texts[2], key, key2, key3,
		refusal[0], refusal[1], refusal[2], refusal[3], refusal[4], refusal[5], refusal[6], refusal[7]) {
		return value, ErrResolveRequired
	}
	for _, n := range numbers {
		if !n.Valid {
			return value, ErrResolveRequired
		}
	}
	for _, s := range texts {
		if !s.Valid {
			return value, ErrResolveRequired
		}
	}
	for _, x := range refusal {
		if x != nil {
			return value, ErrResolveRequired
		}
	}
	if change.Valid || family != 0 || phase != 0 || id != 0 || aux != 0 || key != "" || key2 != "" || key3 != "" || afterEntity != 0 || epoch != 0 {
		return value, ErrResolveRequired
	}
	v.LeaseOwner, v.LeaseGeneration, v.LeaseExpiresMS = numbers[0].Int64, numbers[1].Int64, numbers[2].Int64
	v.AcknowledgedSequence, v.Discovered, v.Processed = numbers[3].Int64, numbers[4].Int64, numbers[5].Int64
	v.RootPages, v.RootSkipped = numbers[6].Int64, numbers[7].Int64
	v.RootIdentity, v.RootRevision, v.RootCursor = texts[0].String, texts[1].String, texts[2].String
	var digest ResultDigest
	if kind == InventoryCreate {
		digest, err = inventoryCreateResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, inventoryCreateResult{v})
	} else {
		digest, err = inventoryClaimResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, inventoryClaimResult{v})
	}
	if err != nil || digest != ResultDigest(facts.resultDigest) {
		return value, errors.Join(ErrResolveRequired, err)
	}
	children, err := turn.query(ctx, turn.steps[scanReceiptReclaimKey].statementStep, q.RunID, inc, q.Receipt.Identity.Sequence)
	if err != nil {
		return value, err
	}
	hasChild := children.Next()
	err = errors.Join(children.Err(), children.Close())
	if err != nil || hasChild {
		return value, errors.Join(ErrResolveRequired, err)
	}
	out.result, out.digest = v, digest
	return out, nil
}

// Lifecycle ACK changes only the floor and deletes the fully validated exact
// scalar header. The caller commits/rolls back BOTH; no ACK receipt or refund is
// minted. Ordinary N1 ACK and its independently charged cohort are unchanged.
func acknowledgeScanRunLifecycle(ctx context.Context, turn *commandTurn, q ScanReceiptRequest, kind CommandID) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	lookup, err := lookupScanRunLifecycle(ctx, turn, q, kind)
	if err != nil {
		return 0, err
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.Receipt.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrResolveRequired
	}
	for _, id := range []stmt.ID{scanReceiptAck, scanReceiptReclaimKey, scanReceiptReclaimHeader} {
		if _, ok := turn.steps[id]; !ok {
			return 0, invalid("scan_lifecycle_ack_steps")
		}
	}
	// The same-invocation lookup proved the exact cohort empty in this SAME
	// transaction. Nothing between it and this CAS can append a child or pin.
	changed, err := turn.exec(ctx, turn.steps[scanReceiptAck].statementStep, q.Receipt.Identity.Sequence,
		q.RunID, q.Receipt.Identity.Incarnation[:], lookup.owner.acknowledgedSequence, lookup.owner.lastSequence)
	if err != nil {
		return 0, err
	}
	n, err := changed.RowsAffected()
	if err != nil || n != 1 {
		return 0, errors.Join(ErrConflict, err)
	}
	changed, err = turn.exec(ctx, turn.steps[scanReceiptReclaimHeader].statementStep, q.RunID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return 0, err
	}
	n, err = changed.RowsAffected()
	if err != nil || n != 1 {
		return 0, errors.Join(ErrConflict, err)
	}
	return q.Receipt.Identity.Sequence, nil
}
