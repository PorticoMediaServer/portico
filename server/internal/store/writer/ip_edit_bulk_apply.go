package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strconv"
)

// Bounded native bulk apply and no-next finish. Each target's finite field
// bundle runs in the SAME protected turn. Its exact receipt is pinned by the
// captured target before the bulk page result commits. Current authority and
// required search/event/resource effects remain outer-driver composition.
func applyIPEditBulkPage(ctx context.Context, t *commandTurn, c ipEditBulkCommand, appliedMS int64) (out ipEditBulkNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	q := c.request
	r := q.Root
	i := q.Identity
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != q.Action.command() || appliedMS < 0 || (q.Action != ipEditBulkApply && q.Action != ipEditBulkFinish) {
		return out, ErrInvalid
	}
	checked, e := newIPEditBulkCommand(q)
	if e != nil || checked.digest != c.digest {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	f, out, e := resolveIPEditBulk(ctx, t, c)
	if e != nil || out.decision != receiptFresh {
		return out, e
	}
	if q.LeaseUntilMS <= appliedMS || f.leaseOwner != q.LeaseOwner || f.leaseGeneration != q.LeaseGeneration || f.leaseUntilMS != q.LeaseUntilMS || f.revision != q.Revision || (f.state != 2 && f.state != 3) || !f.complete.Valid || f.complete.Int64 != 1 || !f.expected.Valid || f.expected.Int64 != r.ExpectedCount || !f.captured.Valid || f.captured.Int64 != r.ExpectedCount || !f.expectedBytes.Valid || f.expectedBytes.Int64 != r.ExpectedBytes || !f.capturedBytes.Valid || f.capturedBytes.Int64 != r.ExpectedBytes || !bytes.Equal(f.manifest, r.Manifest[:]) || !bytes.Equal(f.chain, r.Manifest[:]) || f.processed < 0 || f.failed < 0 || f.processed > r.ExpectedCount || f.failed > r.ExpectedCount-f.processed || f.processed+f.failed != q.AfterOrdinal {
		return ipEditBulkNative{}, ErrConflict
	}
	if err = ipTextExpectOne(ctx, t, ipBulkRoot, r.Operation, r.Kind, r.RootEntity, r.Library, r.Actor, r.BaseRevision); err != nil {
		return ipEditBulkNative{}, err
	}
	for _, field := range r.Fields {
		if err = ipTextExpectOne(ctx, t, ipBulkFieldMatch, ipEditBulkFieldArgs(r.Operation, field)...); err != nil {
			return ipEditBulkNative{}, err
		}
	}
	raw, err := ipEditBulkRaw(ctx, t, r.Operation, q.AfterOrdinal)
	if err != nil {
		return ipEditBulkNative{}, err
	}
	next := ipEditBulkResult{Revision: q.Revision + 1, AfterOrdinal: q.AfterOrdinal, Captured: r.ExpectedCount, Processed: f.processed, Failed: f.failed, AppliedMS: appliedMS, Complete: true, Manifest: r.Manifest}
	oldCursor := ""
	if q.AfterOrdinal > 0 {
		oldCursor = strconv.FormatInt(q.AfterOrdinal, 10)
	}
	if q.Action == ipEditBulkFinish {
		if len(raw) != 0 || q.AfterOrdinal != r.ExpectedCount {
			return ipEditBulkNative{}, ErrConflict
		}
		if err = ipTextExpectOne(ctx, t, ipBulkFinishApply, i.Sequence, r.Operation, i.Incarnation[:], q.Revision, f.last, f.ack, oldCursor, q.AfterOrdinal); err != nil {
			return ipEditBulkNative{}, err
		}
	} else {
		if len(raw) < len(q.Targets) {
			return ipEditBulkNative{}, ErrConflict
		}
		for j, target := range q.Targets {
			if raw[j] != target {
				return ipEditBulkNative{}, ErrConflict
			}
			edit := q.Edits[j]
			src := edit.Source
			rows, e := ipTextRows(ctx, t, ipBulkEligible, target.EntityID, r.Library, src.LibraryID, target.Incarnation[:], target.Revision, src.MetadataRevision)
			if e != nil {
				return ipEditBulkNative{}, e
			}
			var n int64
			e = ipTextOne(rows, func() error { return rows.Scan(&n) })
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return ipEditBulkNative{}, e
			}
			if errors.Is(e, sql.ErrNoRows) {
				if err = ipTextExpectOne(ctx, t, ipBulkTargetStale, r.Operation, target.Ordinal, target.EntityID, target.Incarnation[:], target.Revision); err != nil {
					return ipEditBulkNative{}, err
				}
				next.Failed++
			} else {
				child, e := newIPTextBundleCommand(edit)
				if e != nil {
					return ipEditBulkNative{}, e
				}
				applied, e := applyIPTextBundleBody(ctx, t, child, appliedMS)
				if e != nil {
					return ipEditBulkNative{}, e
				}
				if applied.decision != receiptFresh {
					return ipEditBulkNative{}, ErrResolveRequired
				}
				if err = ipTextExpectOne(ctx, t, ipBulkTargetApplied, applied.value.SubjectRevision, src.OwnerID, edit.Identity.Incarnation[:], edit.Identity.Sequence, r.Operation, target.Ordinal, target.EntityID, target.Incarnation[:], target.Revision); err != nil {
					return ipEditBulkNative{}, err
				}
				next.Processed++
			}
			next.AfterOrdinal = target.Ordinal + 1
		}
		if err = ipTextExpectOne(ctx, t, ipBulkApplyAdvance, next.Processed, next.Failed, strconv.FormatInt(next.AfterOrdinal, 10), i.Sequence, r.Operation, i.Incarnation[:], q.Revision, f.last, f.ack, oldCursor, f.processed, f.failed); err != nil {
			return ipEditBulkNative{}, err
		}
	}
	out.value = next
	out.digest, err = canonicalResultDigest(i, c.digest, ipEditBulkSavedResult{q.Action, next})
	if err != nil {
		return ipEditBulkNative{}, err
	}
	if err = ipTextExecOne(ctx, t, ipBulkSave, r.Operation, i.Incarnation[:], i.Sequence, i.OperationID[:], c.digest[:], appliedMS, out.digest[:], q.Action.text(), next.Revision, next.AfterOrdinal, next.Captured, next.Processed, next.Failed, ipTextBool(next.Complete), r.Manifest[:]); err != nil {
		return ipEditBulkNative{}, err
	}
	return out, t.checkLifetimes(ctx)
}
func ipEditBulkRaw(ctx context.Context, t *commandTurn, owner string, after int64) (out []ipEditBulkTarget, err error) {
	rows, err := ipTextRows(ctx, t, ipBulkRawTargets, owner, after)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, rows.Close())
		if err != nil {
			out = nil
		}
	}()
	for rows.Next() {
		var v ipEditBulkTarget
		var inc []byte
		var state int64
		if err = rows.Scan(&v.Ordinal, &v.EntityID, &inc, &v.Revision, &state); err != nil {
			return nil, err
		}
		if len(inc) != 16 || state != 1 || v.Ordinal != after+int64(len(out)) {
			return nil, ErrResolveRequired
		}
		copy(v.Incarnation[:], inc)
		out = append(out, v)
	}
	return out, rows.Err()
}

const (
	ipBulkRawTargets    stmt.ID = "ip.edit_bulk.raw_targets"
	ipBulkEligible      stmt.ID = "ip.edit_bulk.eligible"
	ipBulkTargetStale   stmt.ID = "ip.edit_bulk.target_stale"
	ipBulkTargetApplied stmt.ID = "ip.edit_bulk.target_applied"
	ipBulkApplyAdvance  stmt.ID = "ip.edit_bulk.apply_advance"
	ipBulkFinishApply   stmt.ID = "ip.edit_bulk.finish_apply"
)

func ipEditBulkApplyDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	return []stmt.Definition{
		{ID: ipBulkRawTargets, Kind: stmt.Read, SQL: `SELECT ordinal,entity_id,expected_entity_incarnation,expected_revision,CASE state WHEN 'captured' THEN 1 ELSE 0 END FROM metadata_edit_batch_targets WHERE operation_id=? AND ordinal>=? ORDER BY ordinal LIMIT 17`, Params: []stmt.Param{s("owner"), i("after")}},
		{ID: ipBulkEligible, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=? AND l.library_id=? AND e.library_id=? AND e.metadata_incarnation=? AND e.revision=? AND e.metadata_revision=? AND e.retired=0`, Params: []stmt.Param{i("entity"), s("library"), i("library_id"), b("incarnation"), i("revision"), i("metadata_revision")}},
		{ID: ipBulkTargetStale, Kind: stmt.Write, SQL: `UPDATE metadata_edit_batch_targets SET state='stale' WHERE operation_id=? AND ordinal=? AND entity_id=? AND expected_entity_incarnation=? AND expected_revision=? AND state='captured' AND result_revision IS NULL AND edit_owner_id IS NULL RETURNING 1`, Params: []stmt.Param{s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkTargetApplied, Kind: stmt.Write, SQL: `UPDATE metadata_edit_batch_targets SET state='applied',result_revision=?,edit_owner_id=?,edit_command_incarnation=?,edit_command_sequence=? WHERE operation_id=? AND ordinal=? AND entity_id=? AND expected_entity_incarnation=? AND expected_revision=? AND state='captured' AND result_revision IS NULL AND edit_owner_id IS NULL RETURNING 1`, Params: []stmt.Param{i("result_revision"), i("edit_owner"), b("edit_incarnation"), i("edit_sequence"), s("owner"), i("ordinal"), i("entity"), b("incarnation"), i("revision")}},
		{ID: ipBulkApplyAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET processed=?,failed=?,after_item=?,last_command_sequence=?,job_revision=job_revision+1,status='running' WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND after_item=? AND processed=? AND failed=? AND status IN('ready','running') AND descriptive_input_complete=1 RETURNING 1`, Params: []stmt.Param{i("processed_next"), i("failed_next"), s("after_next"), i("sequence"), s("owner"), b("incarnation"), i("revision"), i("last"), i("ack"), s("after"), i("processed"), i("failed")}},
		{ID: ipBulkFinishApply, Kind: stmt.Write, SQL: `UPDATE metadata_repair_cascades SET status='complete',job_revision=job_revision+1,last_command_sequence=? WHERE id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND after_item=? AND processed+failed=? AND status IN('ready','running') AND descriptive_input_complete=1 AND NOT EXISTS(SELECT 1 FROM metadata_edit_batch_targets t WHERE t.operation_id=metadata_repair_cascades.id AND t.ordinal>=processed+failed) RETURNING 1`, Params: []stmt.Param{i("sequence"), s("owner"), b("incarnation"), i("revision"), i("last"), i("ack"), s("after"), i("processed_and_failed")}},
	}
}
