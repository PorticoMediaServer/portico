package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
)

const (
	MPStartPrepTimeout  CommandID = 705
	MPTranscodeDeadline CommandID = 747
	MPTranscodeRetry    CommandID = 748
	MPVersionFinalize   CommandID = 749
)

// A raw (deadline,id) page selects candidates before lifecycle filtering. This
// exact point retires one expired original operation and retains its execution
// stop obligation. Current maintenance disclosure is supplied by W; the saved
// original actor is historical data, not cleanup authority.
func applyMPStartPrepTimeout(ctx context.Context, t *commandTurn, f mpStartPrepFence) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepTimeout); err != nil {
		return out, err
	}
	if out, err = captureMPStartPrep(ctx, t, f, false); err != nil {
		return out, err
	}
	if out.deadlineMS > f.nowMS || out.state == "accepted" || out.state == "retired" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.timeout", f.id, f.revision, f.execution, out.deadlineMS, f.nowMS); err != nil {
		return out, err
	}
	out.state = "retired"
	out.revision++
	out.stopExecution = f.execution
	out.cleanupPending = f.execution != ""
	return out, t.checkLifetimes(ctx)
}

// Expired execution ownership requests stop; it never licenses takeover or
// releases the execution/pins/lease. Reconcile746 still requires actual exit.
func applyMPTranscodeDeadline(ctx context.Context, t *commandTurn, f mpTranscodeFence) (out mpTranscodeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPTranscodeDeadline); err != nil {
		return out, err
	}
	n, e := captureMPTranscode(ctx, t, f, false)
	if e != nil {
		return out, e
	}
	out = n.effect
	if out.state != "running" || out.leaseOwner <= 0 || out.leaseExpiresMS <= 0 || out.leaseExpiresMS > f.nowMS || n.cleaned != 0 {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.deadline", f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration, out.leaseExpiresMS, f.nowMS); err != nil {
		return out, err
	}
	out.state = "cancelling"
	out.revision++
	out.stopExecution = f.execution
	out.cleanupPending = true
	out.nextAttemptMS = -1
	return out, t.checkLifetimes(ctx)
}

type mpTranscodeRetryInput struct {
	fence     mpTranscodeFence
	retryAtMS int64
}

// Explicit retry of failed fully cleaned work revalidates the original current
// source/actor/recipe. Canceled work is never resurrected. A new execution is
// subsequently claimed by741, not minted by this scheduling effect.
func applyMPTranscodeRetry(ctx context.Context, t *commandTurn, q mpTranscodeRetryInput) (out mpTranscodeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPTranscodeRetry); err != nil {
		return out, err
	}
	f := q.fence
	if q.retryAtMS < f.nowMS || q.retryAtMS == math.MaxInt64 {
		return out, ErrInvalid
	}
	n, e := captureMPTranscode(ctx, t, f, true)
	if e != nil {
		return out, e
	}
	out = n.effect
	if out.state != "failed" || n.exited != 1 || n.cleaned != 1 || out.cleanedGeneration != out.generation || out.leaseOwner != 0 || out.leaseExpiresMS != 0 || out.attempts >= math.MaxUint32 || out.version != "" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.retry", q.retryAtMS, f.nowMS, f.id, f.revision, f.generation, f.leaseGeneration); err != nil {
		return out, err
	}
	out.state = "queued"
	out.revision++
	out.nextAttemptMS = q.retryAtMS
	out.cleanupPending = false
	return out, t.checkLifetimes(ctx)
}

type mpVersionFinalizeInput struct {
	version, digest string
	incarnation     [16]byte
	revision, size  int64
}
type mpVersionFinalizeEffect struct {
	version, digest, job, sourceRevision, transformation string
	revision, size                                       int64
	retired                                              bool
}

// A logically deleted, physically settled version becomes retirement-ready.
// Its native incarnation/floor remains until its own749 result is ACKed and
// reclaimed. This is not object deletion and does not return governor permits.
func applyMPVersionFinalize(ctx context.Context, t *commandTurn, q mpVersionFinalizeInput) (out mpVersionFinalizeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPVersionFinalize); err != nil {
		return out, err
	}
	if !mpPrepTexts(q.version) || !lowerHex(q.digest, 64) || q.incarnation == ([16]byte{}) || !mpPrepRevision(q.revision) || q.size <= 0 {
		return out, ErrInvalid
	}
	row, e := mpPrepRow(ctx, t, "mp.version.finalize", q.version, q.incarnation[:], q.revision, q.digest, q.size)
	if e != nil {
		return out, e
	}
	out.version = q.version
	out.digest = q.digest
	out.size = q.size
	if e = row.Scan(&out.revision, &out.job, &out.sourceRevision, &out.transformation); e != nil {
		return out, e
	}
	if out.revision != q.revision+1 {
		return out, ErrResolveRequired
	}
	out.retired = true
	return out, t.checkLifetimes(ctx)
}
func mpExpiryDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpExpiryDefinition("mp.start.timeout", `UPDATE playback_start_preparation_lifecycle SET state='retired',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state IN('pending','preparing','prepared','failed') AND EXISTS(SELECT 1 FROM playback_start_preparations p WHERE p.id=playback_start_preparation_lifecycle.operation_id AND p.deadline_ms=? AND p.deadline_ms<=?) RETURNING 1`, "sisii", stmt.Write),
		mpExpiryDefinition("mp.transcode.deadline", `UPDATE prepared_media_jobs SET state='cancelling',phase='expired',job_next_attempt_ms=NULL,revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_lease_expires_ms<=? AND state='running' RETURNING 1`, "isiiiiii", stmt.Write),
		mpExpiryDefinition("mp.transcode.retry", `UPDATE prepared_media_jobs SET state='queued',phase='waiting',error_code='',job_next_attempt_ms=?,revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND generation=? AND job_lease_generation=? AND state='failed' AND cleaned_generation=generation AND job_lease_owner=0 AND job_lease_expires_ms=0 AND version_id='' AND job_attempts<4294967295 AND EXISTS(SELECT 1 FROM mp_job_custody c WHERE c.job_id=prepared_media_jobs.id AND c.generation=prepared_media_jobs.generation AND c.physical_exited=1 AND c.cleaned=1) RETURNING 1`, "iisiii", stmt.Write),
		mpExpiryDefinition("mp.version.finalize", `UPDATE prepared_media_versions SET mp_retirement_state='retired',revision=revision+1 WHERE id=? AND mp_command_incarnation=? AND revision=? AND digest=? AND size=? AND state='deleted' AND mp_retirement_state IN('live','draining') AND mp_command_last_sequence=mp_command_acknowledged_sequence AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0 AND NOT EXISTS(SELECT 1 FROM prepared_media_session_pins p WHERE p.version_id=prepared_media_versions.id) AND EXISTS(SELECT 1 FROM mp_version_custody c WHERE c.version_id=prepared_media_versions.id AND c.physical_exited=1 AND c.cleaned=1 AND c.finished=1) RETURNING revision,job_id,source_revision,transformation_digest`, "sbisi", stmt.Write),
	}
}

func mpExpiryDefinition(id, sql, types string, kind stmt.Kind) stmt.Definition {
	d := mpPrepDefinition(id, sql, types, kind)
	d.CallSites = []string{"store/writer/mp_expiry.go"}
	return d
}
