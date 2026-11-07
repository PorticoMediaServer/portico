package writer

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoPersonalClearScope uint8

const (
	uoClearActivity           uoPersonalClearScope = 1
	uoHideHistory             uoPersonalClearScope = 2
	uoClearActivityAndHistory uoPersonalClearScope = 3
)

type uoPersonalClearInput struct {
	policy           uoPersonalPolicy
	scope            uoPersonalClearScope
	expectedRevision int64
}
type uoPersonalClearEffect struct{ head uoPersonalHead }

// ClearHistory(606) is a constant-size privacy/generation boundary. play_history
// is append-only forever. Physical current-row compaction belongs to bounded
// maintenance, never this action or a source-driven per-user fanout.
func applyUOPersonalClear(ctx context.Context, turn *commandTurn, q uoPersonalClearInput) (out uoPersonalClearEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.scope < uoClearActivity || q.scope > uoClearActivityAndHistory {
		return out, ErrInvalid
	}
	h, err := readUOPersonalHead(ctx, turn, q.policy)
	if err != nil {
		return out, err
	}
	if h.revision != q.expectedRevision {
		return out, ErrConflict
	}
	next := h
	next.revision++
	next.action++
	if q.scope != uoHideHistory {
		if h.activity == math.MaxInt64 || h.recommendation == math.MaxInt64 {
			return out, ErrConflict
		}
		next.activity++
		next.recommendation++
	}
	if q.scope != uoClearActivity {
		high, e := wc008DraftInteger(ctx, turn, "uo.personal.history.highwater")
		if e != nil {
			return out, e
		}
		if high < 0 || high == math.MaxInt64 {
			return out, ErrConflict
		}
		next.cutoff = max(h.cutoff, high+1)
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.history.clear", next.revision, next.activity, next.recommendation, next.cutoff, next.action, q.policy.profile, h.revision, h.action, h.activity, h.recommendation, h.cutoff); err != nil {
		return out, err
	}
	out.head = next
	return out, turn.checkLifetimes(ctx)
}

// ApplyActivityBoundary(605) consumes MP/WC's ALREADY accepted occurrence and
// personal-row witness in the SAME fresh turn. WC owns history, resume,
// occurrence counts and all listening/time summaries. UO only supplies watched
// action ordering at a real completion, never a second occurrence/count engine.
// Parent must compose this behind WC's original fresh receipt classification.
type uoPersonalBoundaryInput struct {
	policy                                                                                            uoPersonalPolicy
	subjectID, libraryID, eventID, occurrence, acceptedSequence, activityGeneration, expectedRevision int64
	publicID                                                                                          [16]byte
	library, playback                                                                                 string
	completed                                                                                         bool
}
type uoPersonalBoundaryEffect struct{ revision, viewer, selection int64 }

func applyUOPersonalBoundary(ctx context.Context, turn *commandTurn, q uoPersonalBoundaryInput) (out uoPersonalBoundaryEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.subjectID <= 0 || q.libraryID <= 0 || q.eventID <= 0 || q.occurrence <= 0 || q.acceptedSequence < 0 || q.activityGeneration <= 0 || q.expectedRevision <= 0 || q.expectedRevision == math.MaxInt64 || q.publicID == [16]byte{} || !uoPersonalText(q.library, 128) || !uoPersonalText(q.playback, 128) {
		return out, ErrInvalid
	}
	h, err := readUOPersonalHead(ctx, turn, q.policy)
	if err != nil {
		return out, err
	}
	if h.activity != q.activityGeneration {
		return out, ErrConflict
	}
	kind := "started"
	if q.completed {
		kind = "completed"
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.history.boundary", q.eventID, kind, q.policy.account, q.policy.profileID, q.subjectID, q.publicID[:], q.library, q.playback, q.occurrence, q.acceptedSequence, h.activity, q.policy.memberBirth, q.policy.profileBirth); err != nil {
		return out, err
	}
	// Compare timestamp/playback witnesses inside SQLite; no text is copied into
	// Go or rewritten. The source must already have committed its native prefix
	// inside this transaction. A history row alone does not mint occurrence credit.
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.history.accepted", q.eventID, q.policy.profile, q.subjectID, q.publicID[:], q.libraryID, q.expectedRevision, h.activity, q.playback, uoFlagInteger(q.completed), uoFlagInteger(q.completed)); err != nil {
		return out, err
	}
	out.revision = q.expectedRevision
	if !q.completed {
		return out, turn.checkLifetimes(ctx)
	}
	h, err = advanceUOPersonalHead(ctx, turn, q.policy, h)
	if err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.history.complete", h.action, h.action, q.policy.profile, q.subjectID, q.publicID[:], q.expectedRevision, h.activity); err != nil {
		return out, err
	}
	out.revision++
	out.viewer, out.selection, err = bumpUOPersonalViewer(ctx, turn, q.policy.profile, q.library)
	return out, err
}
func uoPersonalHistoryDefinitions() []stmt.Definition {
	return []stmt.Definition{
		uoPersonalDefinition("uo.personal.history.highwater", stmt.Read, `SELECT COALESCE(max(id),0) FROM play_history`),
		uoPersonalDefinition("uo.personal.history.clear", stmt.Write, `UPDATE personal_activity_revisions SET revision=?,activity_generation=?,recommendation_generation=?,history_visible_from_id=?,action_sequence=? WHERE profile_id=? AND revision=? AND action_sequence=? AND activity_generation=? AND recommendation_generation=? AND history_visible_from_id=? RETURNING revision`, uoPersonalIntegerParam("next_revision"), uoPersonalIntegerParam("activity"), uoPersonalIntegerParam("recommendation"), uoPersonalIntegerParam("cutoff"), uoPersonalIntegerParam("action"), uoPersonalTextParam("profile"), uoPersonalIntegerParam("revision"), uoPersonalIntegerParam("old_action"), uoPersonalIntegerParam("old_activity"), uoPersonalIntegerParam("old_recommendation"), uoPersonalIntegerParam("old_cutoff")),
		uoPersonalDefinition("uo.personal.history.boundary", stmt.Read, `SELECT id FROM play_history WHERE id=? AND event_kind=? AND account_key=? AND profile_id=? AND subject_id=? AND item_public_id=? AND library_public_id=? AND playback_id=? AND occurrence_generation=? AND accepted_sequence=? AND activity_generation=? AND membership_incarnation=? AND profile_lifetime=?`, uoPersonalIntegerParam("event"), uoPersonalTextParam("kind"), uoPersonalTextParam("account"), uoPersonalTextParam("profile"), uoPersonalIntegerParam("subject"), uoPersonalBlobParam("public"), uoPersonalTextParam("library"), uoPersonalTextParam("playback"), uoPersonalIntegerParam("occurrence"), uoPersonalIntegerParam("sequence"), uoPersonalIntegerParam("activity"), uoPersonalTextParam("member"), uoPersonalTextParam("birth")),
		uoPersonalDefinition("uo.personal.history.accepted", stmt.Read, `SELECT p.revision FROM personal_items AS p INDEXED BY personal_current_live CROSS JOIN play_history AS h WHERE h.id=? AND p.profile_id=? AND p.item_id=? AND p.item_public_id=? AND p.library_id=? AND p.revision=? AND p.activity_generation=? AND p.resume_playback_id=? AND (p.evidence_fence_playback_id IS NULL OR p.evidence_fence_playback_id<>h.playback_id) AND (p.dismissed_playback_id IS NULL OR p.dismissed_playback_id<>h.playback_id) AND ((?=0 AND p.last_started_at=h.occurred_at AND p.started_count>0) OR (?=1 AND p.last_completed_at=h.occurred_at AND p.play_count>0 AND p.watched=1))`, uoPersonalIntegerParam("event"), uoPersonalTextParam("profile"), uoPersonalIntegerParam("subject"), uoPersonalBlobParam("public"), uoPersonalIntegerParam("library"), uoPersonalIntegerParam("revision"), uoPersonalIntegerParam("activity"), uoPersonalTextParam("playback"), uoPersonalIntegerParam("start"), uoPersonalIntegerParam("complete")),
		uoPersonalDefinition("uo.personal.history.complete", stmt.Write, `UPDATE personal_items INDEXED BY personal_current_live SET revision=revision+1,action_sequence=?,container_intent_sequence=? WHERE profile_id=? AND item_id=? AND item_public_id=? AND revision=? AND activity_generation=? RETURNING revision`, uoPersonalIntegerParam("action"), uoPersonalIntegerParam("intent"), uoPersonalTextParam("profile"), uoPersonalIntegerParam("subject"), uoPersonalBlobParam("public"), uoPersonalIntegerParam("revision"), uoPersonalIntegerParam("activity")),
	}
}
