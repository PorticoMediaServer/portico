package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mgLCKind string

const (
	mgLCBeginKind      mgLCKind = "begin"
	mgLCCandidatesKind mgLCKind = "candidates" // includes immutable producer EOF
	mgLCEntriesKind    mgLCKind = "entries"
	mgLCVerifyKind     mgLCKind = "verify"
	mgLCSealKind       mgLCKind = "seal"
	mgLCPublishKind    mgLCKind = "publish"
)

func mgLCBirthKind(b mgLCBuild, c mgLCCommand, k mgLCKind) bool {
	if c.identity.Sequence == 1 {
		return k == mgLCBeginKind && c.identity.OperationID == b.create && c.request == b.birth
	}
	return k != mgLCBeginKind
}
func (k mgLCKind) valid() bool {
	switch k {
	case mgLCBeginKind, mgLCCandidatesKind, mgLCEntriesKind, mgLCVerifyKind, mgLCSealKind, mgLCPublishKind:
		return true
	}
	return false
}

// Exactly the draft's scalar native result. Deliberately no current DTO,
// unresolved_count or scan cursor reconstruction: those are not receipt fields.
// The closed MG result codec binds the complete saved scalar value.
type mgLCNativeResult struct {
	candidates, entries, cursor, bytes int64
	entryDigest                        [32]byte
	verified, verifyAfter, verifyEnd   int64
	verifyDigest                       [32]byte
	sealed                             bool
	phase, status, lease               string
	leaseUntil, published              int64
	reclaimFamily, reclaimKey          string
	reclaimSubkey                      int64
}

func mgLCResult(p mgLCProgress) mgLCNativeResult {
	return mgLCNativeResult{p.candidates, p.entries, p.cursor, p.bytes, p.entryDigest, p.verified, p.verifyAfter, p.verifyEnd, p.verifyDigest, p.sealed, p.phase, p.status, p.lease, p.leaseUntil, p.published, p.reclaimFamily, p.reclaimKey, p.reclaimSubkey}
}

type mgLCSaved struct {
	command                mgLCCommand
	kind                   mgLCKind
	digest                 ResultDigest
	applied, revision, ack int64
	result                 mgLCNativeResult
}

const mgLCReceiptResultColumns = `result_candidate_count,result_entry_count,result_cursor_ms,result_staged_bytes,result_entry_digest,result_verified_entry_count,result_verify_after_ms,result_verify_end_ms,result_verified_entry_digest,result_sealed,result_phase,result_status,result_lease_token,result_lease_until_ms,result_published_ms,result_reclaim_family,result_reclaim_key,result_reclaim_subkey`
const mgLCReceiptColumns = `operation_id,request_digest,result_digest,command_kind,applied_ms,accepted_revision,accepted_acknowledged_sequence,` + mgLCReceiptResultColumns

func mgLCReceiptDefinitions() []stmt.Definition {
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }
	t, i, x := stmt.Text, stmt.Integer, stmt.Blob
	owner := []stmt.Param{p("generation", t), p("incarnation", x), p("channel", t), p("config_revision", i), p("catalogue", t), p("history", i), p("create", x), p("birth", x)}
	predicate := `id=? AND command_incarnation=? AND channel_id=? AND config_revision=? AND catalog_fence=? AND play_history_fence=? AND create_operation_id=? AND birth_request_digest=?`
	identity := []stmt.Param{p("generation", t), p("incarnation", x), p("sequence", i)}
	columns := `generation_id,command_incarnation,command_sequence,operation_id,request_digest,result_digest,applied_ms,command_kind,channel_id,config_revision,catalog_fence,play_history_fence,accepted_revision,accepted_acknowledged_sequence,` + mgLCReceiptResultColumns
	names := strings.Split(columns, ",")
	var params []stmt.Param
	texts := map[string]bool{"generation_id": true, "command_kind": true, "channel_id": true, "catalog_fence": true, "result_phase": true, "result_status": true, "result_lease_token": true, "result_reclaim_family": true, "result_reclaim_key": true}
	blobs := map[string]bool{"command_incarnation": true, "operation_id": true, "request_digest": true, "result_digest": true, "result_entry_digest": true, "result_verified_entry_digest": true}
	for _, n := range names {
		typ := i
		if texts[n] {
			typ = t
		}
		if blobs[n] {
			typ = x
		}
		params = append(params, p(n, typ))
	}
	return []stmt.Definition{
		{ID: mgLCBirthOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation FROM lc_generations WHERE id=?`, Params: []stmt.Param{p("generation", t)}},
		{ID: mgLCReceiptOwner, Kind: stmt.Read, SQL: `SELECT ` + mgLCProgressColumns + ` FROM lc_generations WHERE ` + predicate, Params: owner},
		{ID: mgLCReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO lc_generation_command_receipts(` + columns + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(params)), ",") + `)`, Params: params},
		{ID: mgLCReceiptRead, Kind: stmt.Read, SQL: `SELECT channel_id,config_revision,catalog_fence,play_history_fence,` + mgLCReceiptColumns + ` FROM lc_generation_command_receipts WHERE generation_id=? AND command_incarnation=? AND command_sequence=?`, Params: identity},
		{ID: mgLCReceiptOperation, Kind: stmt.Read, SQL: `SELECT command_sequence FROM lc_generation_command_receipts WHERE generation_id=? AND command_incarnation=? AND operation_id=?`, Params: []stmt.Param{p("generation", t), p("incarnation", x), p("operation", x)}},
		{ID: mgLCReceiptAny, Kind: stmt.Read, SQL: `SELECT command_sequence FROM lc_generation_command_receipts WHERE generation_id=? LIMIT 1`, Params: []stmt.Param{p("generation", t)}},
		{ID: mgLCRetirement, Kind: stmt.Read, SQL: `SELECT command_incarnation,create_operation_id,birth_request_digest,channel_id,final_sequence,final_result_digest FROM lc_generation_retirements WHERE generation_id=?`, Params: []stmt.Param{p("generation", t)}},
		{ID: mgLCAckOwner, Kind: stmt.Write, SQL: `UPDATE lc_generations SET acknowledged_command_sequence=? WHERE ` + predicate + ` AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING acknowledged_command_sequence`, Params: append(append([]stmt.Param{p("ack", i)}, owner...), p("last", i), p("old_ack", i))},
	}
}
func (b mgLCBuild) ownerValues() []any {
	return []any{b.generation, b.incarnation[:], b.channel, b.revision, b.fence(), b.history, b.create[:], b.birth[:]}
}
func readMGLCOwner(ctx context.Context, turn *commandTurn, b mgLCBuild) (mgLCProgress, error) {
	row, err := mgLCRow(ctx, turn, mgLCReceiptOwner, b.ownerValues()...)
	if err != nil {
		return mgLCProgress{}, err
	}
	return scanMGLCProgress(row)
}
func readMGLCSaved(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand) (mgLCSaved, bool, error) {
	row, err := mgLCRow(ctx, turn, mgLCReceiptRead, b.generation, b.incarnation[:], c.identity.Sequence)
	if err != nil {
		return mgLCSaved{}, false, err
	}
	var s mgLCSaved
	var operation, request, digest, entries, verified []byte
	var kind, channel, catalogue string
	var revision, history int64
	var sealed int64
	r := &s.result
	err = row.Scan(&channel, &revision, &catalogue, &history, &operation, &request, &digest, &kind, &s.applied, &s.revision, &s.ack, &r.candidates, &r.entries, &r.cursor, &r.bytes, &entries, &r.verified, &r.verifyAfter, &r.verifyEnd, &verified, &sealed, &r.phase, &r.status, &r.lease, &r.leaseUntil, &r.published, &r.reclaimFamily, &r.reclaimKey, &r.reclaimSubkey)
	if errors.Is(err, sql.ErrNoRows) {
		return mgLCSaved{}, false, nil
	}
	if err != nil {
		return mgLCSaved{}, false, err
	}
	if channel != b.channel || revision != b.revision || catalogue != b.fence() || history != b.history {
		return mgLCSaved{}, false, ErrConflict
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 || len(entries) != 32 || len(verified) != 32 || (sealed != 0 && sealed != 1) {
		return mgLCSaved{}, false, ErrConflict
	}
	s.command.identity = c.identity
	copy(s.command.identity.OperationID[:], operation)
	copy(s.command.request[:], request)
	copy(s.digest[:], digest)
	copy(r.entryDigest[:], entries)
	copy(r.verifyDigest[:], verified)
	r.sealed = sealed == 1
	s.kind = mgLCKind(kind)
	if !s.kind.valid() || s.applied < 0 || s.revision <= 0 || s.ack < 0 || s.ack >= c.identity.Sequence {
		return mgLCSaved{}, false, ErrConflict
	}
	if s.digest != mgLCResultDigest(b, s) {
		return mgLCSaved{}, false, ErrConflict
	}
	return s, true, nil
}

// The retained floor is checked FIRST, before live-owner/receipt absence. It
// may deny re-birth; it never reconstructs or claims an original success.
func retiredMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild) (bool, error) {
	row, err := mgLCRow(ctx, turn, mgLCRetirement, b.generation)
	if err != nil {
		return false, err
	}
	var incarnation, create, birth, digest []byte
	var channel string
	var sequence int64
	err = row.Scan(&incarnation, &create, &birth, &channel, &sequence, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(incarnation, b.incarnation[:]) || !bytes.Equal(create, b.create[:]) || !bytes.Equal(birth, b.birth[:]) || channel != b.channel || sequence < 1 || len(digest) != 32 {
		return true, ErrConflict
	}
	return true, nil
}
func excludeMGLCBirth(ctx context.Context, turn *commandTurn, b mgLCBuild) error {
	retired, err := retiredMGLC(ctx, turn, b)
	if err != nil {
		return err
	}
	if retired {
		return ErrConflict
	}
	// An existing owner under ANY incarnation also prevents a new birth,
	// even after every ACKed receipt from that incarnation has been reclaimed.
	live, err := mgLCRow(ctx, turn, mgLCBirthOwner, b.generation)
	if err != nil {
		return err
	}
	var incarnation []byte
	if err := live.Scan(&incarnation); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	row, err := mgLCRow(ctx, turn, mgLCReceiptAny, b.generation)
	if err != nil {
		return err
	}
	var seq int64
	if err := row.Scan(&seq); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}

// Lookup has no current config/head/catalogue/lease CAS: an unknown COMMIT must
// resolve the ORIGINAL receipt. Fresh authority/disclosure and codec validation
// remain in the parent. No shared dispatcher registration or Applied is made.
func lookupMGLCReceipt(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, kind mgLCKind) (decision receiptDecision, saved mgLCSaved, err error) {
	defer func() {
		saved, err = finishCommand(ctx, commandLifetime(turn), saved, err)
		if err != nil {
			decision = receiptConflict
		}
	}()
	if !c.valid(b) || !kind.valid() {
		return receiptConflict, saved, ErrInvalid
	}
	retired, err := retiredMGLC(ctx, turn, b)
	if err != nil {
		return receiptConflict, saved, err
	}
	if !mgLCBirthKind(b, c, kind) {
		return receiptConflict, saved, ErrConflict
	}
	found, present, err := readMGLCSaved(ctx, turn, b, c)
	if err != nil {
		return receiptConflict, saved, err
	}
	if present && (found.command != c || found.kind != kind) {
		return receiptConflict, saved, ErrConflict
	}
	if retired {
		return receiptRetired, saved, nil
	}
	row, err := mgLCRow(ctx, turn, mgLCReceiptOperation, b.generation, b.incarnation[:], c.identity.OperationID[:])
	if err != nil {
		return receiptConflict, saved, err
	}
	var seq int64
	if err := row.Scan(&seq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return receiptConflict, saved, err
	} else if err == nil && seq != c.identity.Sequence {
		return receiptConflict, saved, ErrConflict
	}
	owner, err := readMGLCOwner(ctx, turn, b)
	if errors.Is(err, ErrConflict) {
		if !present && kind == mgLCBeginKind && c.identity.Sequence == 1 && c.identity.OperationID == b.create && c.request == b.birth {
			if err := excludeMGLCBirth(ctx, turn, b); err != nil {
				return receiptConflict, saved, err
			}
			return receiptFresh, saved, nil
		}
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	if err != nil {
		return receiptConflict, saved, err
	}
	if owner.last < 1 || owner.ack < 0 || owner.ack > owner.last || owner.last-owner.ack > 1 {
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	if c.identity.Sequence <= owner.ack {
		return receiptRetired, saved, nil
	}
	if c.identity.Sequence <= owner.last {
		if !present || found.revision != owner.revision || found.ack != owner.ack {
			return receiptResolveRequired, saved, ErrResolveRequired
		}
		return receiptReplay, found, nil
	}
	if present {
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	if c.identity.Sequence-owner.last != 1 {
		return receiptConflict, saved, ErrConflict
	}
	if owner.last != owner.ack {
		return receiptResolveRequired, saved, ErrResolveRequired
	}
	return receiptFresh, saved, nil
}
func saveMGLCReceipt(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, kind mgLCKind, p mgLCProgress) (out mgLCSaved, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !c.valid(b) || !kind.valid() || p.last != c.identity.Sequence || p.ack != p.last-1 {
		return out, ErrInvalid
	}
	if !mgLCBirthKind(b, c, kind) {
		return out, ErrConflict
	}
	actual, err := readMGLCOwner(ctx, turn, b)
	if err != nil {
		return out, err
	}
	if actual != p {
		return out, ErrConflict
	}
	now, err := mgLCTime(ctx, turn, b.leaseUntil)
	if err != nil {
		return out, err
	}
	r := mgLCResult(actual)
	savedResult := mgLCSaved{command: c, kind: kind, applied: now, revision: p.revision, ack: p.ack, result: r}
	digest := mgLCResultDigest(b, savedResult)
	sealed := int64(0)
	if r.sealed {
		sealed = 1
	}
	args := []any{b.generation, b.incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.request[:], digest[:], now, string(kind), b.channel, b.revision, b.fence(), b.history, p.revision, p.ack, r.candidates, r.entries, r.cursor, r.bytes, r.entryDigest[:], r.verified, r.verifyAfter, r.verifyEnd, r.verifyDigest[:], sealed, r.phase, r.status, r.lease, r.leaseUntil, r.published, r.reclaimFamily, r.reclaimKey, r.reclaimSubkey}
	if err := mgLCOne(ctx, turn, mgLCReceiptInsert, args...); err != nil {
		return out, err
	}
	saved, present, err := readMGLCSaved(ctx, turn, b, c)
	if err != nil {
		return out, err
	}
	out = mgLCSaved{c, kind, digest, now, p.revision, p.ack, r}
	if !present || saved != out {
		return mgLCSaved{}, ErrConflict
	}
	return out, nil
}

// An ACK has no fresh source/lease dependency and changes only the watermark.
// It binds the exact original request/operation/kind AND saved result digest.
func acknowledgeMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, kind mgLCKind, digest ResultDigest) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !c.valid(b) || !kind.valid() || digest == (ResultDigest{}) {
		return 0, ErrInvalid
	}
	if !mgLCBirthKind(b, c, kind) {
		return 0, ErrConflict
	}
	saved, present, err := readMGLCSaved(ctx, turn, b, c)
	if err != nil {
		return 0, err
	}
	if !present || saved.command != c || saved.kind != kind || saved.digest != digest {
		return 0, ErrConflict
	}
	owner, err := readMGLCOwner(ctx, turn, b)
	if err != nil {
		return 0, err
	}
	if owner.ack >= c.identity.Sequence {
		return owner.ack, nil
	}
	if owner.last != c.identity.Sequence || owner.ack != c.identity.Sequence-1 {
		return 0, ErrConflict
	}
	args := append([]any{c.identity.Sequence}, b.ownerValues()...)
	args = append(args, owner.last, owner.ack)
	row, err := mgLCRow(ctx, turn, mgLCAckOwner, args...)
	if err != nil {
		return 0, err
	}
	if err := row.Scan(&out); err != nil {
		return 0, mgLCNoRows(err)
	}
	if out != c.identity.Sequence {
		return 0, ErrConflict
	}
	return out, nil
}
