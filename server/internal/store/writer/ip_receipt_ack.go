package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native source-ACK component only, matching W's floor-only protocol. The
// outer closed dispatcher must first validate the COMPLETE typed original
// result and current disclosure in this SAME admitted turn. Passing a digest
// grants no authority. No debt/receipt/history is deleted or refunded here;
// physical cleanup is a separately receipted maintenance command.
type ipReceiptOwnerKind uint8

const (
	ipReceiptPolicy  ipReceiptOwnerKind = 1
	ipReceiptRefresh ipReceiptOwnerKind = 2
	ipReceiptText    ipReceiptOwnerKind = 3
	ipReceiptArtwork ipReceiptOwnerKind = 4
	ipReceiptPerson  ipReceiptOwnerKind = 5
)

type ipReceiptACKRequest struct {
	family                             ipReceiptOwnerKind
	ownerID, libraryID                 int64
	provider, purpose, personOperation string
	reference                          ReceiptReference
	sourceKind                         CommandID
	resultDigest                       ResultDigest
	appliedMS                          int64 // outer writer's trusted clock
}

func (q ipReceiptACKRequest) coordinates() (owner, receipt, update stmt.ID, args []any, err error) {
	switch q.family {
	case ipReceiptPolicy:
		if q.libraryID <= 0 || !ipPolicyValidText(q.provider, 128) || q.sourceKind != IPPolicySet {
			err = ErrInvalid
			return
		}
		owner = ipACKPolicyOwner
		receipt = ipACKPolicyReceipt
		update = ipACKPolicyAdvance
		args = []any{q.libraryID, q.provider}
	case ipReceiptRefresh:
		if !ipPolicyValidText(q.provider, 128) || (q.purpose != "metadata" && q.purpose != "artwork") || (q.sourceKind != IPRefreshRequest && q.sourceKind != IPRefreshPage) {
			err = ErrInvalid
			return
		}
		owner = ipACKRefreshOwner
		receipt = ipACKRefreshReceipt
		update = ipACKRefreshAdvance
		args = []any{q.provider, q.purpose}
	case ipReceiptText:
		if q.ownerID <= 0 || (q.sourceKind != IPFieldEdit && q.sourceKind != IPFieldLock && q.sourceKind != IPFieldRestore) {
			err = ErrInvalid
			return
		}
		owner = ipACKTextOwner
		receipt = ipACKTextReceipt
		update = ipACKTextAdvance
		args = []any{q.ownerID}
	case ipReceiptArtwork:
		if q.ownerID <= 0 || q.sourceKind < IPArtworkDiscoveryClaim || q.sourceKind > IPArtworkReencode {
			err = ErrInvalid
			return
		}
		owner = ipACKArtworkOwner
		receipt = ipACKArtworkReceipt
		update = ipACKArtworkAdvance
		args = []any{q.ownerID}
	case ipReceiptPerson:
		if !ipPolicyValidText(q.personOperation, 512) || (q.sourceKind != IPEvidenceBegin && q.sourceKind != IPEvidenceAppend && q.sourceKind != IPEvidenceSeal && q.sourceKind != IPProviderAccept && q.sourceKind != IPProviderRenew && q.sourceKind != IPProviderFinish && q.sourceKind != IPProviderReclaim) {
			err = ErrInvalid
			return
		}
		owner = ipACKPersonOwner
		receipt = ipACKPersonReceipt
		update = ipACKPersonAdvance
		args = []any{q.personOperation}
	default:
		err = ErrInvalid
	}
	return
}
func acknowledgeIPReceipt(ctx context.Context, t *commandTurn, q ipReceiptACKRequest) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(t), floor, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != IPReceiptACK || !ipPolicyValidIdentity(q.reference.Identity) || q.reference.RequestDigest == (RequestDigest{}) || q.resultDigest == (ResultDigest{}) || q.appliedMS < 0 {
		return 0, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	oid, rid, uid, keys, err := q.coordinates()
	if err != nil {
		return 0, err
	}
	var incarnation []byte
	var last, ack, after int64
	var due sql.NullInt64
	rows, err := ipTextRows(ctx, t, oid, keys...)
	if err != nil {
		return 0, err
	}
	err = ipTextOne(rows, func() error { return rows.Scan(&incarnation, &last, &ack, &after, &due) })
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrOwnerUnavailable
	}
	if err != nil {
		return 0, err
	}
	i := q.reference.Identity
	if len(incarnation) != 16 || !bytes.Equal(incarnation, i.Incarnation[:]) || after < 0 || after > ack || ack > last || last-ack > 1 || due.Valid && due.Int64 < 0 {
		return 0, ErrResolveRequired
	}
	// The floor precedes receipt decoding. ACK retry stays retired after native
	// cleanup or source replacement; it must not redue/refund anything.
	if i.Sequence <= ack {
		return ack, nil
	}
	if i.Sequence != last {
		return 0, ErrConflict
	}
	args := append(append([]any(nil), keys...), i.Incarnation[:], i.Sequence, i.OperationID[:], q.reference.RequestDigest[:], q.resultDigest[:], int64(q.sourceKind))
	if err = ipTextExpectOne(ctx, t, rid, args...); err != nil {
		return 0, err
	}
	nextDue := q.appliedMS
	if due.Valid && due.Int64 < nextDue {
		nextDue = due.Int64
	}
	var oldDue any
	if due.Valid {
		oldDue = due.Int64
	}
	args = []any{i.Sequence, nextDue}
	args = append(args, keys...)
	args = append(args, i.Incarnation[:], last, ack, after, oldDue)
	if err = ipTextExpectOne(ctx, t, uid, args...); err != nil {
		return 0, err
	}
	return i.Sequence, t.checkLifetimes(ctx)
}

const (
	ipACKPolicyOwner    stmt.ID = "ip.receipt_ack.policy_owner"
	ipACKPolicyReceipt  stmt.ID = "ip.receipt_ack.policy_receipt"
	ipACKPolicyAdvance  stmt.ID = "ip.receipt_ack.policy_advance"
	ipACKRefreshOwner   stmt.ID = "ip.receipt_ack.refresh_owner"
	ipACKRefreshReceipt stmt.ID = "ip.receipt_ack.refresh_receipt"
	ipACKRefreshAdvance stmt.ID = "ip.receipt_ack.refresh_advance"
	ipACKTextOwner      stmt.ID = "ip.receipt_ack.text_owner"
	ipACKTextReceipt    stmt.ID = "ip.receipt_ack.text_receipt"
	ipACKTextAdvance    stmt.ID = "ip.receipt_ack.text_advance"
	ipACKArtworkOwner   stmt.ID = "ip.receipt_ack.artwork_owner"
	ipACKArtworkReceipt stmt.ID = "ip.receipt_ack.artwork_receipt"
	ipACKArtworkAdvance stmt.ID = "ip.receipt_ack.artwork_advance"
	ipACKPersonOwner    stmt.ID = "ip.receipt_ack.person_owner"
	ipACKPersonReceipt  stmt.ID = "ip.receipt_ack.person_receipt"
	ipACKPersonAdvance  stmt.ID = "ip.receipt_ack.person_advance"
)

func ipReceiptACKDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(k string) stmt.Param { return stmt.Param{Name: k, Type: stmt.Integer, Nullable: true} }
	defs := []stmt.Definition{
		{ID: ipACKPolicyOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM metadata_provider_policy WHERE library_id=? AND provider=?`, Params: []stmt.Param{i("library_id"), s("provider")}},
		{ID: ipACKPolicyReceipt, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_policy_receipts WHERE library_id=? AND provider=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND request_digest=? AND result_digest=? AND (200)=?`, Params: []stmt.Param{i("library_id"), s("provider"), b("incarnation"), i("sequence"), b("operation"), b("request"), b("result"), i("kind")}},
		{ID: ipACKPolicyAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_provider_policy SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=? WHERE library_id=? AND provider=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING 1`, Params: []stmt.Param{i("new_ack"), i("due"), i("library_id"), s("provider"), b("incarnation"), i("last"), i("ack"), i("after"), n("old_due")}},
		{ID: ipACKRefreshOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM metadata_refresh_sweeps WHERE provider=? AND purpose=?`, Params: []stmt.Param{s("provider"), s("purpose")}},
		{ID: ipACKRefreshReceipt, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_refresh_receipts WHERE provider=? AND purpose=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND request_digest=? AND result_digest=? AND (CASE command_kind WHEN 'request' THEN 201 WHEN 'page' THEN 202 WHEN 'finish' THEN 202 ELSE 0 END)=?`, Params: []stmt.Param{s("provider"), s("purpose"), b("incarnation"), i("sequence"), b("operation"), b("request"), b("result"), i("kind")}},
		{ID: ipACKRefreshAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_refresh_sweeps SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=? WHERE provider=? AND purpose=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING 1`, Params: []stmt.Param{i("new_ack"), i("due"), s("provider"), s("purpose"), b("incarnation"), i("last"), i("ack"), i("after"), n("old_due")}},
		{ID: ipACKTextOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM metadata_edit_owners WHERE id=?`, Params: []stmt.Param{i("id")}},
		{ID: ipACKTextReceipt, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND request_digest=? AND result_digest=? AND (CASE command_kind WHEN 'field_edit' THEN 240 WHEN 'field_lock' THEN 241 WHEN 'field_restore' THEN 242 ELSE 0 END)=?`, Params: []stmt.Param{i("id"), b("incarnation"), i("sequence"), b("operation"), b("request"), b("result"), i("kind")}},
		{ID: ipACKTextAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_edit_owners SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=? WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING 1`, Params: []stmt.Param{i("new_ack"), i("due"), i("id"), b("incarnation"), i("last"), i("ack"), i("after"), n("old_due")}},
		{ID: ipACKArtworkOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM artwork_subject_jobs WHERE id=?`, Params: []stmt.Param{i("id")}},
		{ID: ipACKArtworkReceipt, Kind: stmt.Read, SQL: `SELECT 1 FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND request_digest=? AND result_digest=? AND (CASE command_kind WHEN 'discovery_claim' THEN 220 WHEN 'discovery_page' THEN 221 WHEN 'discovery_finish' THEN 222 WHEN 'job_claim' THEN 223 WHEN 'job_renew' THEN 224 WHEN 'material_publish' THEN 225 WHEN 'select' THEN 226 WHEN 'clear' THEN 227 WHEN 'upload_accept' THEN 228 WHEN 'gc_mark' THEN 229 WHEN 'gc_finish' THEN 230 WHEN 'reencode' THEN 231 ELSE 0 END)=?`, Params: []stmt.Param{i("id"), b("incarnation"), i("sequence"), b("operation"), b("request"), b("result"), i("kind")}},
		{ID: ipACKArtworkAdvance, Kind: stmt.Write, SQL: `UPDATE artwork_subject_jobs SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=? WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING 1`, Params: []stmt.Param{i("new_ack"), i("due"), i("id"), b("incarnation"), i("last"), i("ack"), i("after"), n("old_due")}},
		{ID: ipACKPersonOwner, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM metadata_person_operations WHERE id=?`, Params: []stmt.Param{s("id")}},
		{ID: ipACKPersonReceipt, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_person_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND request_digest=? AND result_digest=? AND (CASE command_kind WHEN 'begin' THEN 210 WHEN 'append' THEN 211 WHEN 'evidence_seal' THEN 212 WHEN 'accept' THEN 216 WHEN 'renew' THEN 204 WHEN 'retry' THEN 205 WHEN 'reclaim' THEN 218 ELSE 0 END)=?`, Params: []stmt.Param{s("id"), b("incarnation"), i("sequence"), b("operation"), b("request"), b("result"), i("kind")}},
		{ID: ipACKPersonAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_person_operations SET acknowledged_command_sequence=?,receipt_reclaim_due_ms=? WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS ? RETURNING 1`, Params: []stmt.Param{i("new_ack"), i("due"), s("id"), b("incarnation"), i("last"), i("ack"), i("after"), n("old_due")}},
	}
	for j := range defs {
		defs[j].Owner = "writer.ip.receipt_ack"
		defs[j].CallSites = []string{"writer/ip_receipt_ack.go:acknowledgeIPReceipt"}
		var bindings []any
		for _, p := range defs[j].Params {
			if p.Nullable {
				bindings = append(bindings, nil)
			} else {
				switch p.Type {
				case stmt.Integer:
					bindings = append(bindings, int64(0))
				case stmt.Text:
					bindings = append(bindings, "")
				case stmt.Blob:
					bindings = append(bindings, make([]byte, 16))
				}
			}
		}
		defs[j].Cohorts = []stmt.Cohort{{Name: "missing_native_owner", Bindings: bindings}}
	}
	return defs
}
