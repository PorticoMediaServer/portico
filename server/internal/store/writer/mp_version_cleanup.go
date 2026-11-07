package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
)

// Distinct artifact-cleanup physical lifetime on the ACTUAL version owner.
// A joined encoding execution is never substituted for this execution. W's
// external adapter starts a returned claim only after confirmed/resolved COMMIT
// and retains its IO/helper permit until actual exit. Expiry cannot take over.
type mpVersionCleanupFence struct {
	version, request, execution, digest string
	incarnation                         [16]byte
	revision, generation, cursor, size  int64
}
type mpVersionCleanupClaim struct {
	fence     mpVersionCleanupFence
	execution string
}

// Inert observation, not proof. Only the closed external adapter may supply a
// verified exact joined execution and artifact/reference-gate completion. No
// caller boolean, SQL digest or version size proves physical byte release.
type mpVersionCleanupConfirmation struct {
	fence      mpVersionCleanupFence
	exitDigest string
	nextCursor int64
	finished   bool
}
type mpVersionCleanupEffect struct {
	oldCustodyBytes, newCustodyBytes  int64
	version, request, execution       string
	revision, generation, cursor      int64
	physicalExited, cleaned, finished bool
}

func (f mpVersionCleanupFence) valid() bool {
	return mpPrepTexts(f.version, f.request) && mpPrepText(f.execution, true) && lowerHex(f.digest, 64) && f.incarnation != ([16]byte{}) && mpPrepRevision(f.revision) && f.generation >= 0 && f.generation < math.MaxInt64 && f.cursor >= 0 && f.size > 0 && (f.generation == 0) == (f.execution == "")
}
func applyMPVersionCleanupClaim(ctx context.Context, t *commandTurn, q mpVersionCleanupClaim) (out mpVersionCleanupEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 749); err != nil {
		return out, err
	}
	f := q.fence
	if !f.valid() || !mpPrepTexts(q.execution) || q.execution == f.execution {
		return out, ErrInvalid
	}
	oldBytes, e := mpVersionCustodyBytes(ctx, t, f)
	if e != nil {
		return out, e
	}
	// The new execution can replace only fully joined/cleaned previous work.
	row, e := mpPrepRow(ctx, t, "mp.version.cleanup_claim", q.execution, f.version, f.request, f.generation, f.execution, f.cursor, f.incarnation[:], f.revision, f.digest, f.size)
	if e != nil {
		return out, e
	}
	var newBytes int64
	if e = row.Scan(&newBytes); e != nil {
		return out, e
	}
	if newBytes <= 0 {
		return out, ErrResolveRequired
	}
	if err = mpPrepOne(ctx, t, "mp.version.cleanup_claim_revision", f.version, f.incarnation[:], f.revision, f.digest, f.size, q.execution, f.request, f.generation+1); err != nil {
		return out, err
	}
	out = mpVersionCleanupEffect{oldCustodyBytes: oldBytes, newCustodyBytes: newBytes, version: f.version, request: f.request, execution: q.execution, revision: f.revision + 1, generation: f.generation + 1, cursor: f.cursor}
	return out, t.checkLifetimes(ctx)
}
func applyMPVersionCleanupConfirmation(ctx context.Context, t *commandTurn, q mpVersionCleanupConfirmation) (out mpVersionCleanupEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 749); err != nil {
		return out, err
	}
	f := q.fence
	if !f.valid() || f.generation <= 0 || f.execution == "" || !lowerHex(q.exitDigest, 64) || q.nextCursor < f.cursor || q.nextCursor-f.cursor > 64 {
		return out, ErrInvalid
	}
	oldBytes, e := mpVersionCustodyBytes(ctx, t, f)
	if e != nil {
		return out, e
	}
	done := int64(0)
	if q.finished {
		done = 1
	}
	row, e := mpPrepRow(ctx, t, "mp.version.cleanup_confirm", q.exitDigest, q.nextCursor, done, f.version, f.request, f.generation, f.execution, f.cursor, f.incarnation[:], f.revision, f.digest, f.size)
	if e != nil {
		return out, e
	}
	var newBytes int64
	if e = row.Scan(&newBytes); e != nil {
		return out, e
	}
	if newBytes <= 0 {
		return out, ErrResolveRequired
	}
	if err = mpPrepOne(ctx, t, "mp.version.cleanup_confirm_revision", done, f.version, f.incarnation[:], f.revision, f.digest, f.size, f.request, f.generation, f.execution, q.nextCursor, done, q.exitDigest); err != nil {
		return out, err
	}
	out = mpVersionCleanupEffect{oldCustodyBytes: oldBytes, newCustodyBytes: newBytes, version: f.version, request: f.request, execution: f.execution, revision: f.revision + 1, generation: f.generation, cursor: q.nextCursor, physicalExited: true, cleaned: true, finished: q.finished}
	return out, t.checkLifetimes(ctx)
}

// W captures the COMPLETE original version result here in the same fresh turn
// after745/749 effects and before receipt storage. Replay uses immutable saved
// fields instead. The original result includes actual cleanup execution identity.
func captureMPVersionReceiptResult(ctx context.Context, t *commandTurn, id string, inc [16]byte) (out mpVersionResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != 745 && t.transaction.descriptor.ID != 749 || inc == ([16]byte{}) || !mpPrepTexts(id) {
		return out, ErrInvalid
	}
	row, e := mpPrepRow(ctx, t, "mp.version.result_current", id, inc[:])
	if e != nil {
		return out, e
	}
	var finished, exited, cleaned int64
	if e = row.Scan(&out.version, &out.job, &out.sourceRevision, &out.transformation, &out.digest, &out.state, &out.retirement, &out.revision, &out.size, &out.cleanupRequest, &out.cleanupExecution, &out.cleanupGeneration, &out.cleanupCursor, &finished, &exited, &cleaned); e != nil {
		return out, e
	}
	if finished < 0 || finished > 1 || exited < 0 || exited > 1 || cleaned < 0 || cleaned > exited {
		return out, ErrResolveRequired
	}
	out.cleanupFinished = finished == 1
	out.physicalExited = exited == 1
	out.cleaned = cleaned == 1
	out.physicalPending = !out.cleanupFinished
	return out, t.checkLifetimes(ctx)
}
func mpVersionCustodyBytes(ctx context.Context, t *commandTurn, f mpVersionCleanupFence) (int64, error) {
	row, e := mpPrepRow(ctx, t, "mp.version.custody_bytes", f.version, f.request, f.generation, f.execution, f.cursor, f.incarnation[:], f.revision, f.digest, f.size)
	if e != nil {
		return 0, e
	}
	var n int64
	if e = row.Scan(&n); e != nil {
		return 0, e
	}
	if n <= 0 {
		return 0, ErrResolveRequired
	}
	return n, nil
}
func mpVersionCleanupDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpVersionCleanupDefinition("mp.version.custody_bytes", `SELECT c.variable_bytes FROM mp_version_custody c JOIN prepared_media_versions v ON v.id=c.version_id WHERE c.version_id=? AND c.request_id=? AND c.generation=? AND c.execution_id=? AND c.cleanup_cursor=? AND v.mp_command_incarnation=? AND v.revision=? AND v.digest=? AND v.size=? AND v.state='deleting'`, "ssisibisi", stmt.Read),
		mpVersionCleanupDefinition("mp.version.cleanup_claim", `UPDATE mp_version_custody SET generation=generation+1,execution_id=?,physical_exited=0,cleaned=0,exit_digest='' WHERE version_id=? AND request_id=? AND generation=? AND execution_id=? AND cleanup_cursor=? AND finished=0 AND physical_exited=1 AND cleaned=1 AND EXISTS(SELECT 1 FROM prepared_media_versions v WHERE v.id=mp_version_custody.version_id AND v.mp_command_incarnation=? AND v.revision=? AND v.digest=? AND v.size=? AND v.state='deleting') AND NOT EXISTS(SELECT 1 FROM prepared_media_session_pins p WHERE p.version_id=mp_version_custody.version_id) RETURNING variable_bytes`, "sssisibisi", stmt.Write),
		mpVersionCleanupDefinition("mp.version.cleanup_claim_revision", `UPDATE prepared_media_versions SET revision=revision+1,mp_retirement_state='draining' WHERE id=? AND mp_command_incarnation=? AND revision=? AND digest=? AND size=? AND state='deleting' AND EXISTS(SELECT 1 FROM mp_version_custody c WHERE c.version_id=prepared_media_versions.id AND c.execution_id=? AND c.request_id=? AND c.generation=? AND c.physical_exited=0 AND c.cleaned=0 AND c.finished=0) RETURNING 1`, "sbisissi", stmt.Write),
		mpVersionCleanupDefinition("mp.version.cleanup_confirm", `UPDATE mp_version_custody SET physical_exited=1,cleaned=1,exit_digest=?,cleanup_cursor=?,finished=? WHERE version_id=? AND request_id=? AND generation=? AND execution_id=? AND cleanup_cursor=? AND physical_exited=0 AND cleaned=0 AND finished=0 AND EXISTS(SELECT 1 FROM prepared_media_versions v WHERE v.id=mp_version_custody.version_id AND v.mp_command_incarnation=? AND v.revision=? AND v.digest=? AND v.size=? AND v.state='deleting') AND NOT EXISTS(SELECT 1 FROM prepared_media_session_pins p WHERE p.version_id=mp_version_custody.version_id) RETURNING variable_bytes`, "siissisibisi", stmt.Write),
		mpVersionCleanupDefinition("mp.version.cleanup_confirm_revision", `UPDATE prepared_media_versions SET revision=revision+1,state=CASE WHEN ?=1 THEN 'deleted' ELSE 'deleting' END,mp_retirement_state='draining' WHERE id=? AND mp_command_incarnation=? AND revision=? AND digest=? AND size=? AND state='deleting' AND EXISTS(SELECT 1 FROM mp_version_custody c WHERE c.version_id=prepared_media_versions.id AND c.request_id=? AND c.generation=? AND c.execution_id=? AND c.cleanup_cursor=? AND c.finished=? AND c.exit_digest=? AND c.physical_exited=1 AND c.cleaned=1) RETURNING 1`, "isbisisisiis", stmt.Write),
		mpVersionCleanupDefinition("mp.version.result_current", `SELECT v.id,v.job_id,v.source_revision,v.transformation_digest,v.digest,v.state,v.mp_retirement_state,v.revision,v.size,c.request_id,c.execution_id,c.generation,c.cleanup_cursor,c.finished,c.physical_exited,c.cleaned FROM prepared_media_versions v JOIN mp_version_custody c ON c.version_id=v.id WHERE v.id=? AND v.mp_command_incarnation=?`, "sb", stmt.Read),
	}
}

func mpVersionCleanupDefinition(id, sql, types string, kind stmt.Kind) stmt.Definition {
	d := mpPrepDefinition(id, sql, types, kind)
	d.CallSites = []string{"store/writer/mp_version_cleanup.go"}
	return d
}
