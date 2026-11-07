package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
)

const (
	MPTranscodeCreate    CommandID = 740
	MPTranscodeClaim     CommandID = 741
	MPTranscodeRenew     CommandID = 742
	MPTranscodeCancel    CommandID = 743
	MPTranscodePublish   CommandID = 744
	MPTranscodeRetire    CommandID = 745
	MPTranscodeReconcile CommandID = 746
)

type mpTranscodeRecipe struct {
	profile, target, selection, sourceRevision, transformation string
	part                                                       int64
}

func (r mpTranscodeRecipe) valid() bool {
	return mpPrepTexts(r.profile, r.target, r.selection) && lowerHex(r.sourceRevision, 64) && lowerHex(r.transformation, 64) && r.part >= 0
}

type mpTranscodeCreate struct {
	rootIncarnation [16]byte
	id              string
	item, nowMS     int64
	actor           mpPrepActor
	source          mpPrepSource
	recipe          mpTranscodeRecipe
}
type mpTranscodeFence struct {
	id, execution                                            string
	revision, generation, leaseOwner, leaseGeneration, nowMS int64
	actor                                                    mpPrepActor
	source                                                   mpPrepSource
	recipe                                                   mpTranscodeRecipe
}
type mpTranscodeEffect struct {
	phase, errorCode, sourceRevision                                                                     string
	objectSize                                                                                           int64
	versionIncarnation                                                                                   [16]byte
	versionState                                                                                         string
	versionRevision                                                                                      int64
	rootIncarnation                                                                                      [16]byte
	job, state, execution, version, digest                                                               string
	revision, generation, cleanedGeneration, leaseOwner, leaseGeneration, leaseExpiresMS, attempts, size int64
	nextAttemptMS                                                                                        int64 // -1 means SQL NULL (not due)
	stopExecution                                                                                        string
	cleanupPending                                                                                       bool
	cleanupCursor                                                                                        int64
}
type mpTranscodeNative struct {
	effect          mpTranscodeEffect
	actor           mpPrepActor
	source          mpPrepSource
	recipe          mpTranscodeRecipe
	exited, cleaned int64
}

func applyMPTranscodeCreate(ctx context.Context, t *commandTurn, q mpTranscodeCreate) (out mpTranscodeEffect, err error) {
	defer func() {
		if err == nil {
			out, err = completeMPTranscodeResult(ctx, t, out)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodeCreate); err != nil {
		return
	}
	if q.rootIncarnation == ([16]byte{}) || !mpPrepTexts(q.id) || q.item <= 0 || q.nowMS <= 0 || !q.actor.valid(q.nowMS) || !q.recipe.valid() {
		return out, ErrInvalid
	}
	if err = checkMPPrepActor(ctx, t, q.actor); err != nil {
		return
	}
	if err = checkMPPrepSource(ctx, t, q.source); err != nil {
		return
	}
	if err = checkMPPrepItem(ctx, t, q.item, q.recipe.part, q.source); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.create", q.id, q.item, q.source.asset, q.source.library, q.recipe.profile, q.recipe.target, q.recipe.selection, q.recipe.sourceRevision, q.nowMS, q.nowMS, q.nowMS, q.rootIncarnation[:]); err != nil {
		return
	}
	args := append([]any{q.id}, q.actor.args()...)
	args = append(args, q.source.args()...)
	args = append(args, q.recipe.transformation, q.recipe.part)
	if err = mpPrepOne(ctx, t, "mp.transcode.input_insert", args...); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.custody_insert", q.id); err != nil {
		return
	}
	return mpTranscodeEffect{rootIncarnation: q.rootIncarnation, job: q.id, state: "queued", revision: 1, nextAttemptMS: q.nowMS}, nil
}

func captureMPTranscode(ctx context.Context, t *commandTurn, f mpTranscodeFence, fresh bool) (out mpTranscodeNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mpPrepTexts(f.id) || !mpPrepText(f.execution, true) || !mpPrepRevision(f.revision) || f.generation < 0 || f.generation == math.MaxInt64 || f.leaseOwner < 0 || f.leaseGeneration < 0 || f.leaseGeneration == math.MaxInt64 || f.nowMS <= 0 || !f.actor.valid(0) || (fresh && !f.actor.valid(f.nowMS)) {
		return out, ErrInvalid
	}
	r, err := mpPrepRow(ctx, t, "mp.transcode.current", f.id)
	if err != nil {
		return
	}
	e, a, s, p := &out.effect, &out.actor, &out.source, &out.recipe
	e.job = f.id
	var root, versionRoot []byte
	var item int64
	err = r.Scan(&e.state, &e.revision, &e.generation, &e.cleanedGeneration, &e.leaseOwner, &e.leaseGeneration, &e.leaseExpiresMS, &e.attempts, &e.nextAttemptMS, &e.version, &e.size,
		&a.server, &a.authority, &a.account, &a.profile, &a.device, &a.family, &a.grant, &a.sessionHash, &a.epoch, &a.tokenGeneration, &a.horizonMS,
		&s.library, &s.source, &s.asset, &s.object, &s.revision, &s.root, &s.incarnation, &s.remote, &s.configuration, &s.policy, &s.authority, &s.libraryGeneration, &s.configurationGeneration, &s.profileRevision, &s.size, &s.modifiedNS,
		&p.profile, &p.target, &p.selection, &p.sourceRevision, &p.transformation, &p.part, &e.execution, &out.exited, &out.cleaned, &e.cleanupCursor, &root, &item, &e.digest, &e.versionState, &e.versionRevision, &versionRoot, &e.phase, &e.errorCode, &e.sourceRevision, &e.objectSize)
	if err != nil {
		return
	}
	if fresh {
		if err = checkMPPrepActor(ctx, t, f.actor); err != nil {
			return
		}
	}
	if len(root) != 16 {
		return out, ErrResolveRequired
	}
	copy(e.rootIncarnation[:], root)
	if e.version != "" {
		if len(versionRoot) != 16 {
			return out, ErrResolveRequired
		}
		copy(e.versionIncarnation[:], versionRoot)
	} else if len(versionRoot) != 0 {
		return out, ErrResolveRequired
	}
	if *a != f.actor {
		return out, ErrDisclosureDenied
	}
	if e.revision != f.revision || e.generation != f.generation || e.leaseOwner != f.leaseOwner || e.leaseGeneration != f.leaseGeneration || e.execution != f.execution {
		return out, ErrConflict
	}
	if e.cleanedGeneration > e.generation || e.attempts < 0 || e.attempts > math.MaxUint32 || (out.exited != 0 && out.exited != 1) || (out.cleaned != 0 && out.cleaned != 1) || out.cleaned > out.exited {
		return out, ErrResolveRequired
	}
	e.cleanupPending = out.cleaned == 0
	if fresh {
		if *s != f.source || *p != f.recipe || !p.valid() {
			return out, ErrConflict
		}
		if err = checkMPPrepItem(ctx, t, item, p.part, *s); err != nil {
			return
		}
		if err = checkMPPrepSource(ctx, t, *s); err != nil {
			return
		}
	}
	return
}

func applyMPTranscodeClaim(ctx context.Context, t *commandTurn, f mpTranscodeFence, owner, untilMS int64, execution string) (out mpTranscodeEffect, err error) {
	defer func() {
		if err == nil {
			out, err = completeMPTranscodeResult(ctx, t, out)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodeClaim); err != nil {
		return
	}
	if owner <= 0 || untilMS <= f.nowMS || untilMS > f.actor.horizonMS || !mpPrepTexts(execution) {
		return out, ErrInvalid
	}
	n, err := captureMPTranscode(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = n.effect
	if out.state != "queued" || n.exited != 1 || n.cleaned != 1 || out.cleanedGeneration != out.generation || out.nextAttemptMS < 0 || out.nextAttemptMS > f.nowMS || out.leaseExpiresMS > f.nowMS || out.attempts == math.MaxUint32 {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.claim", owner, untilMS, f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration, f.nowMS, f.nowMS); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.execution", execution, f.id, f.generation); err != nil {
		return
	}
	out.state = "running"
	out.revision++
	out.generation++
	out.leaseOwner = owner
	out.leaseGeneration++
	out.leaseExpiresMS = untilMS
	out.attempts++
	out.execution = execution
	out.nextAttemptMS = -1
	out.cleanupPending = true
	out.cleanupCursor = 0
	out.size = 0
	return
}
func applyMPTranscodeRenew(ctx context.Context, t *commandTurn, f mpTranscodeFence, untilMS int64) (out mpTranscodeEffect, err error) {
	defer func() {
		if err == nil {
			out, err = completeMPTranscodeResult(ctx, t, out)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodeRenew); err != nil {
		return
	}
	n, err := captureMPTranscode(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = n.effect
	if out.state != "running" || out.leaseOwner <= 0 || out.leaseExpiresMS <= f.nowMS || untilMS <= out.leaseExpiresMS || untilMS > f.actor.horizonMS || n.exited != 0 {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.renew", untilMS, f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration, f.nowMS); err != nil {
		return
	}
	out.leaseExpiresMS = untilMS
	out.revision++
	return
}
func applyMPTranscodeCancel(ctx context.Context, t *commandTurn, f mpTranscodeFence) (out mpTranscodeEffect, err error) {
	defer func() {
		if err == nil {
			out, err = completeMPTranscodeResult(ctx, t, out)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodeCancel); err != nil {
		return
	}
	n, err := captureMPTranscode(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = n.effect
	if out.state != "queued" && out.state != "running" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.cancel", f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration); err != nil {
		return
	}
	if out.state == "queued" {
		out.state = "cancelled"
	} else {
		out.state = "cancelling"
		out.stopExecution = out.execution
	}
	out.revision++
	out.nextAttemptMS = -1
	return
}

// Inert observation data, NOT proof. The sole external-work adapter must verify
// its opaque supervisor custody and the source/recipe-bound probe, encode,
// validation, immutable checkpoint and digest/size BEFORE invoking this component.
// A string digest, SQL row or boolean cannot replace that verification.
type mpTranscodeVerifiedArtifact struct {
	versionIncarnation                                                    [16]byte // allocated by W, distinct from the job owner
	job, execution, version, digest, inputEvidence, transformation, facts string
	generation, leaseOwner, leaseGeneration, size                         int64
	source                                                                mpPrepSource
	recipe                                                                mpTranscodeRecipe
}

func (a mpTranscodeVerifiedArtifact) valid(f mpTranscodeFence) bool {
	return a.versionIncarnation != ([16]byte{}) && a.job == f.id && a.execution == f.execution && a.execution != "" && a.generation == f.generation && a.leaseOwner == f.leaseOwner && a.leaseGeneration == f.leaseGeneration && a.source == f.source && a.recipe == f.recipe && a.transformation == f.recipe.transformation && mpPrepTexts(a.version, a.inputEvidence, a.facts) && lowerHex(a.digest, 64) && lowerHex(a.transformation, 64) && a.size > 0
}
func applyMPTranscodePublish(ctx context.Context, t *commandTurn, f mpTranscodeFence, a mpTranscodeVerifiedArtifact) (out mpTranscodeEffect, err error) {
	defer func() {
		if err == nil {
			out, err = completeMPTranscodeResult(ctx, t, out)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodePublish); err != nil {
		return
	}
	if !a.valid(f) {
		return out, ErrInvalid
	}
	n, err := captureMPTranscode(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = n.effect
	if a.versionIncarnation == out.rootIncarnation {
		return out, ErrInvalid
	}
	if out.state != "running" || out.leaseExpiresMS <= f.nowMS || out.leaseOwner <= 0 || out.version != "" || n.cleaned != 0 {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.publish_version", a.version, f.recipe.part, a.inputEvidence, a.transformation, a.digest, a.size, a.facts, f.nowMS, a.versionIncarnation[:], f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration, f.nowMS); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.publish_job", a.size, a.version, f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration, f.nowMS); err != nil {
		return
	}
	out.state = "succeeded"
	out.revision++
	out.size = a.size
	out.version = a.version
	out.versionIncarnation = a.versionIncarnation
	out.digest = a.digest
	out.versionState = "published"
	out.versionRevision = 1
	out.nextAttemptMS = -1
	// Lease/custody remain held until real physical exit and cleanup, even after
	// durable publication. Retiring a version is a separate pin-fenced command.
	return
}

type mpTranscodeVersionFence struct {
	version, digest, cleanupRequestID string
	size, revision                    int64
}

// A retirement returns job observation plus actual NEW version-child debt. It
// never uses the job physical-pending field to represent artifact cleanup.
// W captures/stores the parent's version-owned result under745 in this turn.
type mpTranscodeRetirementEffect struct {
	job                  mpTranscodeEffect
	cleanupRequestID     string
	custodyVariableBytes int64
}

func applyMPTranscodeRetire(ctx context.Context, t *commandTurn, f mpTranscodeFence, v mpTranscodeVersionFence) (out mpTranscodeRetirementEffect, err error) {
	defer func() {
		if err == nil {
			out.job, err = completeMPTranscodeResult(ctx, t, out.job)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodeRetire); err != nil {
		return
	}
	if !mpPrepTexts(v.version, v.cleanupRequestID) || !lowerHex(v.digest, 64) || v.size <= 0 || !mpPrepRevision(v.revision) {
		return out, ErrInvalid
	}
	n, err := captureMPTranscode(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out.job = n.effect
	job := &out.job
	if n.exited != 1 || n.cleaned != 1 || job.state != "succeeded" || job.version != v.version || job.versionState != "published" || job.versionRevision != v.revision || job.digest != v.digest || job.size != v.size {
		return out, ErrConflict
	}
	// Pin lookup is a single indexed NOT EXISTS, not an unbounded reader walk.
	if err = mpPrepOne(ctx, t, "mp.transcode.retire", v.version, f.id, job.versionIncarnation[:], v.revision, v.digest, v.size); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, "mp.transcode.retire_custody", v.version, v.cleanupRequestID)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.custodyVariableBytes, &out.cleanupRequestID); e != nil {
		return out, e
	}
	if out.custodyVariableBytes <= 0 || out.cleanupRequestID != v.cleanupRequestID {
		return out, ErrResolveRequired
	}
	job.version = v.version
	job.versionState = "deleting"
	job.versionRevision = v.revision + 1
	job.digest = v.digest
	job.size = v.size
	// Encode custody stays exited and cleaned. The version-owned request is
	// pending until 749 claims a distinct execution and observes its real exit.
	return
}

// This data must come from the exact supervisor execution's joined exit and a
// completed bounded external deletion slice. It is not constructed from lease
// expiry or cancellation. No helper/file IO takes place inside the writer.
type mpTranscodeCleanup struct {
	execution                                                   string
	generation, leaseOwner, leaseGeneration, cursor, nextCursor int64
	exitDigest                                                  string
	finished                                                    bool
	retryAtMS                                                   int64                   // -1 means terminal failure/cancellation, otherwise explicit retry
	version                                                     mpTranscodeVersionFence // rejected: deletion belongs to version749
}

func applyMPTranscodeReconcile(ctx context.Context, t *commandTurn, f mpTranscodeFence, c mpTranscodeCleanup) (out mpTranscodeEffect, err error) {
	defer func() {
		if err == nil {
			out, err = completeMPTranscodeResult(ctx, t, out)
		}
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
	}()
	if err = mpPrepTurn(t, MPTranscodeReconcile); err != nil {
		return
	}
	if c.version != (mpTranscodeVersionFence{}) || c.execution == "" || c.execution != f.execution || c.generation != f.generation || c.leaseOwner != f.leaseOwner || c.leaseGeneration != f.leaseGeneration || !lowerHex(c.exitDigest, 64) || c.cursor < 0 || c.nextCursor < c.cursor || c.nextCursor-c.cursor > 64 || c.retryAtMS < -1 || (c.retryAtMS >= 0 && c.retryAtMS < f.nowMS) {
		return out, ErrInvalid
	}
	n, err := captureMPTranscode(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = n.effect
	if c.cursor != out.cleanupCursor || n.cleaned != 0 || (out.state != "running" && out.state != "cancelling" && out.state != "succeeded") {
		return out, ErrConflict
	}
	if c.retryAtMS >= 0 && (out.state != "running" || !c.finished) {
		return out, ErrInvalid
	}
	done := int64(0)
	if c.finished {
		done = 1
	}
	if err = mpPrepOne(ctx, t, "mp.transcode.cleanup", c.exitDigest, c.nextCursor, done, f.id, f.generation, f.execution, c.cursor, c.exitDigest); err != nil {
		return
	}
	if c.finished {
		if out.state == "succeeded" {
			if err = mpPrepOne(ctx, t, "mp.transcode.version_settled", out.version, f.id); err != nil {
				return
			}
		}
		var retry any
		if c.retryAtMS >= 0 {
			retry = c.retryAtMS
		}
		if err = mpPrepOne(ctx, t, "mp.transcode.reconcile_job", retry, retry, f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration); err != nil {
			return
		}
		switch out.state {
		case "cancelling":
			out.state = "cancelled"
		case "running":
			if c.retryAtMS >= 0 {
				out.state = "queued"
			} else {
				out.state = "failed"
			}
		}
		out.cleanedGeneration = out.generation
		out.leaseOwner = 0
		out.leaseExpiresMS = 0
		out.nextAttemptMS = c.retryAtMS
	} else {
		if err = mpPrepOne(ctx, t, "mp.transcode.cleanup_revision", f.nowMS, f.id, f.revision, f.generation, f.leaseOwner, f.leaseGeneration); err != nil {
			return
		}
	}
	out.revision++
	out.cleanupCursor = c.nextCursor
	out.cleanupPending = !c.finished
	return
}

// Draft definitions use canonical jobs/versions/pins plus the typed input and
// custody children specified in mp_start_prep_transcode_coverage.md. No DDL,
// registration, grant minting or accepting facade is performed here.
func mpTranscodeDefinitions() []stmt.Definition {
	d := mpPrepCommonDefinitions()
	specs := []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.transcode.create", `INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms,job_next_attempt_ms,mp_command_incarnation) VALUES(?,?,?,?,?,?,?,?,'','queued','waiting',?,?,?,?) RETURNING 1`, "sissssssiiib", stmt.Write},
		{"mp.transcode.input_insert", `INSERT INTO mp_job_inputs(job_id,server_id,authority,account_id,actor_profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms,library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns,transformation_digest,part_index) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "sssssssssiiisssssssssssiiiiisi", stmt.Write},
		{"mp.transcode.custody_insert", `INSERT INTO mp_job_custody(job_id,generation,execution_id,physical_exited,cleaned,exit_digest,cleanup_cursor) VALUES(?,0,'',1,1,'',0) RETURNING 1`, "s", stmt.Write},
		{"mp.transcode.current", `SELECT j.state,j.revision,j.generation,j.cleaned_generation,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,j.job_attempts,coalesce(j.job_next_attempt_ms,-1),j.version_id,j.bytes,i.server_id,i.authority,i.account_id,i.actor_profile_id,i.device_id,i.family_id,i.grant_id,i.session_hash,i.epoch,i.token_generation,i.original_horizon_ms,i.library_id,i.library_source_id,i.asset_id,i.inventory_object_id,i.inventory_revision,i.root_identity,i.incarnation,i.remote_source_id,i.effective_configuration,i.read_policy_witness,i.read_authority_witness,i.library_generation,i.raw_configuration_generation,i.profile_revision,i.size,i.modified_ns,j.profile_id,j.target_id,j.selection_json,j.source_revision,i.transformation_digest,i.part_index,c.execution_id,c.physical_exited,c.cleaned,c.cleanup_cursor,j.mp_command_incarnation,j.item_id,coalesce(v.digest,''),coalesce(v.state,''),coalesce(v.revision,0),v.mp_command_incarnation,j.phase,j.error_code,j.source_revision,coalesce(v.size,0) FROM prepared_media_jobs AS j CROSS JOIN mp_job_inputs AS i ON i.job_id=j.id CROSS JOIN mp_job_custody AS c ON c.job_id=j.id AND c.generation=j.generation LEFT JOIN prepared_media_versions AS v ON v.id=j.version_id AND v.job_id=j.id WHERE j.id=?`, "s", stmt.Read},
		{"mp.transcode.claim", `UPDATE prepared_media_jobs SET state='running',phase='probing source',generation=generation+1,job_lease_owner=?,job_lease_generation=job_lease_generation+1,job_lease_expires_ms=?,job_attempts=job_attempts+1,job_next_attempt_ms=NULL,revision=revision+1,updated_ms=?,bytes=0,error_code='' WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms<=? AND job_next_attempt_ms<=? AND state='queued' AND cleaned_generation=generation AND job_attempts<4294967295 RETURNING 1`, "iiisiiiiii", stmt.Write},
		{"mp.transcode.execution", `UPDATE mp_job_custody SET generation=generation+1,execution_id=?,physical_exited=0,cleaned=0,exit_digest='',cleanup_cursor=0 WHERE job_id=? AND generation=? AND physical_exited=1 AND cleaned=1 RETURNING 1`, "ssi", stmt.Write},
		{"mp.transcode.renew", `UPDATE prepared_media_jobs SET job_lease_expires_ms=?,revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? AND state='running' AND job_lease_expires_ms>? RETURNING 1`, "iisiiiii", stmt.Write},
		{"mp.transcode.cancel", `UPDATE prepared_media_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'cancelling' END,phase='cancelled',job_next_attempt_ms=NULL,revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? AND state IN('queued','running') RETURNING 1`, "isiiii", stmt.Write},
		{"mp.transcode.publish_version", `INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,edition_id,input_evidence,transformation_digest,digest,size,facts_json,state,revision,created_ms,mp_command_incarnation,job_id) SELECT ?,j.item_id,j.asset_id,j.library_id,j.profile_id,j.target_id,j.source_revision,j.selection_json,?,NULL,?,?,?,?,?,'published',1,?,?,j.id FROM prepared_media_jobs AS j WHERE j.id=? AND j.revision=? AND j.generation=? AND j.job_lease_owner=? AND j.job_lease_generation=? AND j.job_lease_expires_ms>? AND j.state='running' AND j.version_id='' RETURNING 1`, "sisssisibsiiiii", stmt.Write},
		{"mp.transcode.publish_job", `UPDATE prepared_media_jobs SET state='succeeded',phase='published',bytes=?,version_id=?,revision=revision+1,updated_ms=?,job_next_attempt_ms=NULL WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms>? AND state='running' RETURNING 1`, "isisiiiii", stmt.Write},
		{"mp.transcode.retire", `UPDATE prepared_media_versions SET state='deleting',mp_retirement_state='draining',revision=revision+1 WHERE id=? AND job_id=? AND mp_command_incarnation=? AND revision=? AND digest=? AND size=? AND state='published' AND NOT EXISTS(SELECT 1 FROM prepared_media_session_pins AS p INDEXED BY prepared_media_readers WHERE p.version_id=prepared_media_versions.id) RETURNING 1`, "ssbisi", stmt.Write},
		{"mp.transcode.retire_custody", `INSERT INTO mp_version_custody(version_id,request_id,generation,execution_id,physical_exited,cleaned,exit_digest,cleanup_cursor,finished) VALUES(?,?,0,'',1,1,'',0,0) RETURNING variable_bytes,request_id`, "ss", stmt.Write},
		{"mp.transcode.cleanup", `UPDATE mp_job_custody SET physical_exited=1,exit_digest=?,cleanup_cursor=?,cleaned=? WHERE job_id=? AND generation=? AND execution_id=? AND cleanup_cursor=? AND cleaned=0 AND (physical_exited=0 OR exit_digest=?) RETURNING 1`, "siisisis", stmt.Write},
		{"mp.transcode.version_settled", `SELECT 1 FROM prepared_media_versions WHERE id=? AND job_id=? AND state IN('published','deleted')`, "ss", stmt.Read},
		{"mp.transcode.reconcile_job", `UPDATE prepared_media_jobs SET state=CASE WHEN state='cancelling' THEN 'cancelled' WHEN state='running' THEN CASE WHEN ? IS NULL THEN 'failed' ELSE 'queued' END ELSE state END,phase='settled',cleaned_generation=generation,job_lease_owner=0,job_lease_expires_ms=0,job_next_attempt_ms=?,revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? AND state IN('running','cancelling','succeeded') RETURNING 1`, "iiisiiii", stmt.Write},
		{"mp.transcode.cleanup_revision", `UPDATE prepared_media_jobs SET revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND generation=? AND job_lease_owner=? AND job_lease_generation=? RETURNING 1`, "isiiii", stmt.Write},
	}
	for _, s := range specs {
		v := mpPrepDefinition(s.id, s.sql, s.types, s.kind)
		if s.id == "mp.transcode.reconcile_job" {
			v.Params[0].Nullable = true
			v.Params[1].Nullable = true
		}
		d = append(d, v)
	}
	return d
}
