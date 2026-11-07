package writer

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const uoPersonalBulkMax = 32

type uoPersonalBulkBeginInput struct {
	policy                      uoPersonalPolicy
	job, operation, requestHash string
	field                       uoPersonalField
	value                       string
	subjects                    []int64 // exact accepted explicit selection; no caller SQL/JSON selector
	nowMS                       int64
}
type uoPersonalBulkEffect struct {
	revision, cursor, done, failed, total int64
	terminal                              bool
}

// BeginPersonalJob(607) persists one bounded exact explicit selection. Larger
// server-selected catalog jobs need the parent's separate fenced selection
// builder; this draft refuses them rather than silently truncating membership.
func applyUOPersonalBulkBegin(ctx context.Context, turn *commandTurn, q uoPersonalBulkBeginInput) (out uoPersonalBulkEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.policy.valid() || !uoPersonalText(q.job, 128) || !uoPersonalText(q.operation, 128) || len(q.requestHash) != 64 || !q.field.validValue(q.value) || len(q.subjects) == 0 || len(q.subjects) > uoPersonalBulkMax || q.nowMS < 0 {
		return out, ErrInvalid
	}
	if _, err = readUOPersonalHead(ctx, turn, q.policy); err != nil {
		return out, err
	}
	seen := map[int64]bool{}
	for _, id := range q.subjects {
		if id <= 0 || seen[id] {
			return out, ErrInvalid
		}
		seen[id] = true
	}
	principal := q.policy.account + ":" + q.policy.memberBirth + ":" + q.policy.profileBirth
	args := q.field.name() + "=" + q.value
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.bulk.begin", q.job, q.policy.profile, q.operation, q.requestHash, principal, args, int64(len(q.subjects)), q.nowMS, q.nowMS, q.nowMS); err != nil {
		return out, err
	}
	for n, id := range q.subjects {
		if _, err = wc008DraftInteger(ctx, turn, "uo.personal.bulk.member", q.job, int64(n+1), id); err != nil {
			return out, err
		}
	}
	out = uoPersonalBulkEffect{revision: 1, total: int64(len(q.subjects))}
	return out, turn.checkLifetimes(ctx)
}

type uoPersonalBulkStepInput struct {
	target                                     uoPersonalIntentInput
	job                                        string
	field                                      uoPersonalField
	value                                      string
	revision, cursor, owner, generation, nowMS int64
	// Current permission refusal supplied by parent's same-turn authority check.
	// Source retirement is verified natively; authorization stays parent-owned.
	denied bool
}

// StepPersonalJob(608) processes ONE stored ordinal with its outcome and cursor
// atomically. Parent composes original receipts and final lease release in this
// same turn. A stale worker/cursor cannot rescan or repeat a member's effects.
func applyUOPersonalBulkStep(ctx context.Context, turn *commandTurn, q uoPersonalBulkStepInput) (out uoPersonalBulkEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.target.policy.valid() || !uoPersonalText(q.job, 128) || !q.field.validValue(q.value) || q.revision <= 0 || q.revision == math.MaxInt64 || q.cursor < 0 || q.cursor == math.MaxInt64 || q.owner <= 0 || q.generation <= 0 || q.nowMS < 0 {
		return out, ErrInvalid
	}
	if _, err = readUOPersonalHead(ctx, turn, q.target.policy); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, "uo.personal.bulk.current", q.job, q.target.policy.profile, q.target.policy.account+":"+q.target.policy.memberBirth+":"+q.target.policy.profileBirth, q.field.name()+"="+q.value, q.revision, q.cursor, q.owner, q.generation, q.nowMS)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.total, &out.done, &out.failed); err != nil {
		return out, err
	}
	if out.total <= 0 || out.done < 0 || out.failed < 0 || out.done > out.total || out.failed > out.total-out.done || out.done+out.failed != q.cursor || q.cursor >= out.total {
		return out, ErrResolveRequired
	}
	ordinal := q.cursor + 1
	var subject int64
	var public []byte
	row, err = wc008DraftQuery(ctx, turn, "uo.personal.bulk.next", q.job, ordinal)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&subject, &public); err != nil {
		return out, err
	}
	if subject != q.target.subjectID || len(public) != 16 || string(public) != string(q.target.publicID[:]) {
		return out, ErrConflict
	}
	code := ""
	changed := false
	live, err := wc008DraftInteger(ctx, turn, "uo.personal.bulk.live", subject)
	if err != nil {
		return out, err
	}
	if q.denied {
		code = "not-permitted"
	} else if live == 0 {
		code = "source-unavailable"
	} else {
		_, changed, err = applyUOPersonalScalar(ctx, turn, q.target, q.field, q.value)
		if err != nil {
			return out, err
		}
	}
	outcome := "already_present"
	if changed {
		outcome = "added"
	}
	if code != "" {
		outcome = "failed"
		out.failed++
		if _, err = wc008DraftInteger(ctx, turn, "uo.personal.bulk.failure", q.job, ordinal, subject, code); err != nil {
			return out, err
		}
	} else {
		out.done++
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.bulk.outcome", q.job, public, outcome, code); err != nil {
		return out, err
	}
	out.cursor = ordinal
	out.revision = q.revision + 1
	out.terminal = out.done+out.failed == out.total
	state := "running"
	if out.terminal {
		state = "complete"
		if out.failed > 0 {
			state = "partial"
		}
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.bulk.advance", out.done, out.failed, out.cursor, out.revision, q.nowMS, state, q.job, q.target.policy.profile, q.revision, q.cursor, q.owner, q.generation, q.nowMS); err != nil {
		return out, err
	}
	return out, turn.checkLifetimes(ctx)
}
func uoPersonalBulkDefinitions() []stmt.Definition {
	return []stmt.Definition{
		uoPDef("uo.personal.bulk.begin", stmt.Write, `INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,total,created_ms,updated_ms,selection_complete,job_next_attempt_ms) VALUES(?,?,?,?,?,'explicit',?,'queued',?,?,?,1,?) RETURNING revision`, uoPT("job"), uoPT("profile"), uoPT("operation"), uoPT("hash"), uoPT("principal"), uoPT("args"), uoPI("total"), uoPI("created"), uoPI("updated"), uoPI("next")),
		uoPDef("uo.personal.bulk.member", stmt.Write, `INSERT INTO personal_job_items(job_id,ordinal,item_id) SELECT ?,?,id FROM catalog_subjects WHERE id=? RETURNING ordinal`, uoPT("job"), uoPI("ordinal"), uoPI("subject")),
		uoPDef("uo.personal.bulk.current", stmt.Read, `SELECT total,done,failed FROM personal_jobs WHERE id=? AND profile_id=? AND principal=? AND args=? AND revision=? AND cursor=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms>? AND selection_complete=1 AND state IN('queued','running') AND selector='explicit'`, uoPT("job"), uoPT("profile"), uoPT("principal"), uoPT("args"), uoPI("revision"), uoPI("cursor"), uoPI("owner"), uoPI("generation"), uoPI("now")),
		uoPDef("uo.personal.bulk.next", stmt.Read, `SELECT m.item_id,s.public_id FROM personal_job_items m CROSS JOIN catalog_subjects s ON s.id=m.item_id WHERE m.job_id=? AND m.ordinal=?`, uoPT("job"), uoPI("ordinal")),
		uoPDef("uo.personal.bulk.live", stmt.Read, `SELECT EXISTS(SELECT 1 FROM catalog_entities e CROSS JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=? AND e.retired=0 AND l.retired=0)`, uoPI("subject")),
		uoPDef("uo.personal.bulk.failure", stmt.Write, `INSERT INTO personal_job_failures(job_id,ordinal,item_id,code) VALUES(?,?,?,?) RETURNING ordinal`, uoPT("job"), uoPI("ordinal"), uoPI("subject"), uoPT("code")),
		uoPDef("uo.personal.bulk.outcome", stmt.Write, `INSERT INTO personal_job_membership_outcomes(job_id,item_public_id,outcome,code) VALUES(?,?,?,?) RETURNING 1`, uoPT("job"), uoPB("public"), uoPT("outcome"), uoPT("code")),
		uoPDef("uo.personal.bulk.advance", stmt.Write, `UPDATE personal_jobs SET done=?,failed=?,cursor=?,revision=?,updated_ms=?,state=? WHERE id=? AND profile_id=? AND revision=? AND cursor=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms>? AND state IN('queued','running') RETURNING revision`, uoPI("done"), uoPI("failed"), uoPI("cursor"), uoPI("next_revision"), uoPI("updated"), uoPT("state"), uoPT("job"), uoPT("profile"), uoPI("revision"), uoPI("old_cursor"), uoPI("owner"), uoPI("generation"), uoPI("now")),
	}
}
