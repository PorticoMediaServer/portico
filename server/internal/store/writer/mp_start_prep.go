package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mpStartPrepBegin struct {
	rootIncarnation                         [16]byte
	id, item, keyDigest, bodyDigest         string
	part, nowMS, deadlineMS, tokenExpiresMS int64
	replaces                                string
	replacesGeneration                      int64
	actor                                   mpPrepActor
	source                                  mpPrepSource
	queue                                   mpPrepQueue
	results                                 mpStartPrepStableIDs
}
type mpStartPrepStableIDs struct {
	session, legacySession, snapshot string
	legacyGeneration                 int64
}

func (v mpStartPrepStableIDs) valid() bool {
	return mpPrepTexts(v.session, v.legacySession, v.snapshot) && v.legacyGeneration > 0
}

type mpStartPrepFence struct {
	id, execution   string
	revision, nowMS int64
	actor           mpPrepActor
	source          mpPrepSource
	queue           mpPrepQueue
}

// Effects deliberately carry no ResultState. Stop obligations survive cancel,
// expiry and logical acceptance until the external supervisor records exit.
type mpStartPrepEffect struct {
	cleanupCursor                 int64
	rootIncarnation               [16]byte
	preparation, state, execution string
	revision, deadlineMS          int64
	ids                           mpStartPrepStableIDs
	stopExecution                 string
	cleanupPending                bool
}

func applyMPStartPrepBegin(ctx context.Context, t *commandTurn, q mpStartPrepBegin) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepBegin); err != nil {
		return
	}
	if q.rootIncarnation == ([16]byte{}) || !mpPrepTexts(q.id, q.item) || !lowerHex(q.keyDigest, 64) || !lowerHex(q.bodyDigest, 64) || q.part < 0 || q.nowMS <= 0 || q.deadlineMS <= q.nowMS || q.deadlineMS > q.tokenExpiresMS || q.tokenExpiresMS > q.actor.horizonMS || !q.actor.valid(q.nowMS) || !q.results.valid() || !q.queue.valid() || !mpPrepText(q.replaces, true) || (q.replaces == "") != (q.replacesGeneration == 0) || q.replacesGeneration < 0 {
		return out, ErrInvalid
	}
	if err = checkMPPrepActor(ctx, t, q.actor); err != nil {
		return
	}
	if err = checkMPPrepSource(ctx, t, q.source); err != nil {
		return
	}
	if err = checkMPPrepPublicItem(ctx, t, q.item, q.part, q.source); err != nil {
		return
	}
	if err = checkMPPrepQueue(ctx, t, q.queue, q.actor); err != nil {
		return
	}
	if q.replaces != "" {
		if err = mpPrepOne(ctx, t, "mp.start.replacement", q.replaces, q.actor.authority, q.actor.account, q.actor.profile, q.actor.device, q.replacesGeneration); err != nil {
			return
		}
	}
	// A duplicate device/key is a conflict here. Receipt resolution and legacy
	// start-key/body replay lookup MUST precede fresh effects in the dispatcher.
	if err = mpPrepOne(ctx, t, "mp.start.begin", q.id, q.actor.device, q.keyDigest, q.bodyDigest, q.item, q.source.asset, q.part, q.replaces, q.replacesGeneration, q.tokenExpiresMS, q.nowMS, q.deadlineMS, q.rootIncarnation[:]); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.actor_insert", append([]any{q.id}, q.actor.args()...)...); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.source_insert", append([]any{q.id}, q.source.args()...)...); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.queue_insert", append([]any{q.id, q.id, q.keyDigest, q.bodyDigest}, q.queue.args()...)...); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.results_insert", q.id, q.results.session, q.results.legacySession, q.results.legacyGeneration, q.results.snapshot); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.lifecycle_insert", q.id); err != nil {
		return
	}
	return mpStartPrepEffect{rootIncarnation: q.rootIncarnation, preparation: q.id, state: "pending", revision: 1, deadlineMS: q.deadlineMS, ids: q.results}, nil
}

func captureMPStartPrep(ctx context.Context, t *commandTurn, f mpStartPrepFence, fresh bool) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mpPrepTexts(f.id) || !mpPrepText(f.execution, true) || !mpPrepRevision(f.revision) || f.nowMS <= 0 || !f.actor.valid(0) || (fresh && !f.actor.valid(f.nowMS)) {
		return out, ErrInvalid
	}
	r, err := mpPrepRow(ctx, t, "mp.start.current", f.id)
	if err != nil {
		return
	}
	var actor mpPrepActor
	var source mpPrepSource
	var queue mpPrepQueue
	var sealed int64
	var root []byte
	var item string
	var part int64
	out.preparation = f.id
	err = r.Scan(&out.state, &out.revision, &out.execution, &sealed, &out.deadlineMS,
		&actor.server, &actor.authority, &actor.account, &actor.profile, &actor.device, &actor.family, &actor.grant, &actor.sessionHash, &actor.epoch, &actor.tokenGeneration, &actor.horizonMS,
		&source.library, &source.source, &source.asset, &source.object, &source.revision, &source.root, &source.incarnation, &source.remote, &source.configuration, &source.policy, &source.authority, &source.libraryGeneration, &source.configurationGeneration, &source.profileRevision, &source.size, &source.modifiedNS,
		&queue.id, &queue.entry, &queue.ownerKind, &queue.ownerID, &queue.revision, &queue.custody,
		&out.ids.session, &out.ids.legacySession, &out.ids.legacyGeneration, &out.ids.snapshot, &root, &item, &part)
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
	copy(out.rootIncarnation[:], root)
	if actor != f.actor {
		return out, ErrDisclosureDenied
	}
	if out.revision != f.revision || out.execution != f.execution || (out.execution != "" && sealed != 1) {
		return out, ErrConflict
	}
	if fresh {
		if out.deadlineMS <= f.nowMS || source != f.source || queue != f.queue {
			return out, ErrConflict
		}
		if err = checkMPPrepPublicItem(ctx, t, item, part, source); err != nil {
			return
		}
		if err = checkMPPrepSource(ctx, t, source); err != nil {
			return
		}
		if err = checkMPPrepQueue(ctx, t, queue, actor); err != nil {
			return
		}
	}
	return
}

// Claim assigns one immutable execution, sealing its input before helper work.
// There is no implicit takeover of an execution whose physical exit is unknown.
func applyMPStartPrepClaim(ctx context.Context, t *commandTurn, f mpStartPrepFence, execution string) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepClaim); err != nil {
		return
	}
	if !mpPrepTexts(execution) || f.execution != "" {
		return out, ErrInvalid
	}
	if out, err = captureMPStartPrep(ctx, t, f, true); err != nil {
		return
	}
	if out.state != "pending" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.claim", execution, f.id, f.revision); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.execution_insert", f.id, execution); err != nil {
		return
	}
	out.state = "preparing"
	out.execution = execution
	out.revision++
	out.cleanupPending = true
	return
}

// Sealing publishes only the verified bounded result commitment selected by
// the external preparer. No external work or manifest traversal occurs here.
type mpStartPrepCompletion struct {
	execution, resultDigest string
	stagedRows, stagedBytes int64
}

func applyMPStartPrepSeal(ctx context.Context, t *commandTurn, f mpStartPrepFence, c mpStartPrepCompletion) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepSeal); err != nil {
		return
	}
	if c.execution != f.execution || c.execution == "" || !lowerHex(c.resultDigest, 64) || c.stagedRows < 0 || c.stagedRows > 256 || c.stagedBytes < 0 || c.stagedBytes > 1<<20 {
		return out, ErrInvalid
	}
	if out, err = captureMPStartPrep(ctx, t, f, true); err != nil {
		return
	}
	if out.state != "preparing" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.completion", c.resultDigest, c.stagedRows, c.stagedBytes, f.id, f.execution); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.seal", f.id, f.revision, f.execution); err != nil {
		return
	}
	out.state = "prepared"
	out.revision++
	out.cleanupPending = true
	return
}

func applyMPStartPrepAccept(ctx context.Context, t *commandTurn, f mpStartPrepFence) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepAccept); err != nil {
		return
	}
	if out, err = captureMPStartPrep(ctx, t, f, true); err != nil {
		return
	}
	if out.state != "prepared" || f.execution == "" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.replacement_current", f.id); err != nil {
		return
	}
	// W composes the parent's session/source-pin effects in this SAME turn using
	// these original IDs. Acceptance alone cannot mint a session, grant or ACK.
	if err = mpPrepOne(ctx, t, "mp.start.accept", f.id, f.revision, f.execution, f.nowMS); err != nil {
		return
	}
	out.state = "accepted"
	out.revision++
	out.cleanupPending = true
	return
}

func applyMPStartPrepRetire(ctx context.Context, t *commandTurn, f mpStartPrepFence) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepRetire); err != nil {
		return
	}
	// Retiring a stale source/queue never validates or adopts today's source.
	if out, err = captureMPStartPrep(ctx, t, f, false); err != nil {
		return
	}
	if out.state == "accepted" || out.state == "retired" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.retire", f.id, f.revision, f.execution); err != nil {
		return
	}
	out.state = "retired"
	out.revision++
	out.stopExecution = f.execution
	out.cleanupPending = f.execution != ""
	return
}

func mpStartPrepDefinitions() []stmt.Definition {
	d := mpPrepCommonDefinitions()
	specs := []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.start.begin", `INSERT INTO playback_start_preparations(id,device_id,start_key_digest,start_body_digest,item_id,asset_id,part_index,replaces_session_id,replaces_generation,token_expires_ms,created_ms,deadline_ms,mp_command_incarnation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "ssssssisiiiib", stmt.Write},
		{"mp.start.actor_insert", `INSERT INTO playback_start_preparation_actor(operation_id,server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "sssssssssiii", stmt.Write},
		{"mp.start.source_insert", `INSERT INTO playback_start_preparation_source(operation_id,library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "ssssssssssssiiiii", stmt.Write},
		{"mp.start.queue_insert", `INSERT INTO playback_start_preparation_queue(operation_id,legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "ssssssssii", stmt.Write},
		{"mp.start.results_insert", `INSERT INTO playback_start_preparation_results(operation_id,session_id,legacy_session_id,legacy_generation,snapshot_id) VALUES(?,?,?,?,?) RETURNING 1`, "sssis", stmt.Write},
		{"mp.start.lifecycle_insert", `INSERT INTO playback_start_preparation_lifecycle(operation_id,state,revision,execution_id,sealed) VALUES(?,'pending',1,'',0) RETURNING 1`, "s", stmt.Write},
		{"mp.start.replacement_current", `SELECT 1 FROM playback_start_preparations AS p CROSS JOIN playback_start_preparation_actor AS a ON a.operation_id=p.id WHERE p.id=? AND (p.replaces_session_id='' OR EXISTS(SELECT 1 FROM playback_v1_sessions AS v WHERE v.id=p.replaces_session_id AND v.generation=p.replaces_generation AND v.ended_ms=0 AND v.authority=a.authority AND v.account_id=a.account_id AND v.profile_id=a.profile_id AND v.device_id=a.device_id))`, "s", stmt.Read},
		{"mp.start.replacement", `SELECT 1 FROM playback_v1_sessions WHERE id=? AND authority=? AND account_id=? AND profile_id=? AND device_id=? AND generation=? AND ended_ms=0`, "sssssi", stmt.Read},
		{"mp.start.current", `SELECT l.state,l.revision,l.execution_id,l.sealed,p.deadline_ms,a.server_id,a.authority,a.account_id,a.profile_id,a.device_id,a.family_id,a.grant_id,a.session_hash,a.epoch,a.token_generation,a.original_horizon_ms,s.library_id,s.library_source_id,s.asset_id,s.inventory_object_id,s.inventory_revision,s.root_identity,s.incarnation,s.remote_source_id,s.effective_configuration,s.read_policy_witness,s.read_authority_witness,s.library_generation,s.raw_configuration_generation,s.profile_revision,s.size,s.modified_ns,q.queue_id,q.queue_entry_id,q.queue_owner_kind,q.queue_owner_id,q.queue_revision,q.queue_custody_generation,r.session_id,r.legacy_session_id,r.legacy_generation,r.snapshot_id,p.mp_command_incarnation,p.item_id,p.part_index FROM playback_start_preparations AS p CROSS JOIN playback_start_preparation_lifecycle AS l ON l.operation_id=p.id CROSS JOIN playback_start_preparation_actor AS a ON a.operation_id=p.id CROSS JOIN playback_start_preparation_source AS s ON s.operation_id=p.id CROSS JOIN playback_start_preparation_queue AS q ON q.operation_id=p.id CROSS JOIN playback_start_preparation_results AS r ON r.operation_id=p.id WHERE p.id=?`, "s", stmt.Read},
		{"mp.start.claim", `UPDATE playback_start_preparation_lifecycle SET state='preparing',execution_id=?,sealed=1,revision=revision+1 WHERE operation_id=? AND revision=? AND state='pending' AND execution_id='' AND sealed=0 RETURNING 1`, "ssi", stmt.Write},
		{"mp.start.execution_insert", `INSERT INTO mp_start_execution(operation_id,execution_id,physical_exited,cleaned,exit_digest,result_digest,staged_rows,staged_bytes,cleanup_cursor) VALUES(?,?,0,0,'','',0,0,0) RETURNING 1`, "ss", stmt.Write},
		{"mp.start.completion", `UPDATE mp_start_execution SET result_digest=?,staged_rows=?,staged_bytes=? WHERE operation_id=? AND execution_id=? AND result_digest='' AND cleaned=0 RETURNING 1`, "siiss", stmt.Write},
		{"mp.start.seal", `UPDATE playback_start_preparation_lifecycle SET state='prepared',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state='preparing' AND sealed=1 RETURNING 1`, "sis", stmt.Write},
		{"mp.start.accept", `UPDATE playback_start_preparation_lifecycle SET state='accepted',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state='prepared' AND sealed=1 AND EXISTS(SELECT 1 FROM mp_start_execution AS x WHERE x.operation_id=playback_start_preparation_lifecycle.operation_id AND x.execution_id=playback_start_preparation_lifecycle.execution_id AND length(x.result_digest)=64 AND x.cleaned=0) AND EXISTS(SELECT 1 FROM playback_start_preparations AS p WHERE p.id=playback_start_preparation_lifecycle.operation_id AND p.deadline_ms>?) RETURNING 1`, "sisi", stmt.Write},
		{"mp.start.cleanup", `UPDATE mp_start_execution SET physical_exited=1,exit_digest=?,cleanup_cursor=?,cleaned=? WHERE operation_id=? AND execution_id=? AND cleanup_cursor=? AND cleaned=0 AND (physical_exited=0 OR exit_digest=?) RETURNING 1`, "siissis", stmt.Write},
		{"mp.start.cleanup_revision", `UPDATE playback_start_preparation_lifecycle SET revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state='retired' RETURNING 1`, "sis", stmt.Write},
		{"mp.start.retire", `UPDATE playback_start_preparation_lifecycle SET state='retired',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state IN('pending','preparing','prepared','failed') RETURNING 1`, "sis", stmt.Write},
	}
	for _, s := range specs {
		d = append(d, mpPrepDefinition(s.id, s.sql, s.types, s.kind))
	}
	return d
}

// Retained lifetime identity and original stable IDs are never deleted here.
// The supervisor must prove exact physical exit and completed external cleanup
// slice before supplying this inert observation through W's closed adapter.
type mpStartPrepCleanup struct {
	execution, exitDigest string
	cursor, nextCursor    int64
	finished              bool
}

func applyMPStartPrepCleanup(ctx context.Context, t *commandTurn, f mpStartPrepFence, c mpStartPrepCleanup) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepRetire); err != nil {
		return
	}
	if c.execution == "" || c.execution != f.execution || !lowerHex(c.exitDigest, 64) || c.cursor < 0 || c.nextCursor < c.cursor || c.nextCursor-c.cursor > 64 {
		return out, ErrInvalid
	}
	if out, err = captureMPStartPrep(ctx, t, f, false); err != nil {
		return
	}
	if out.state != "retired" {
		return out, ErrConflict
	}
	done := int64(0)
	if c.finished {
		done = 1
	}
	if err = mpPrepOne(ctx, t, "mp.start.cleanup", c.exitDigest, c.nextCursor, done, f.id, f.execution, c.cursor, c.exitDigest); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.start.cleanup_revision", f.id, f.revision, f.execution); err != nil {
		return
	}
	out.revision++
	out.cleanupCursor = c.nextCursor
	out.cleanupPending = !c.finished
	return
}
