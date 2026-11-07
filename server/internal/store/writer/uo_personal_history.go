package writer

import (
	"context"
	"math"
	"strings"

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

// ApplyActivityBoundary(605) consumes MP's already-accepted occurrence witness.
// MP alone appends start/completion history and WC credits time. This adapter
// only joins that immutable row and fresh generation to current personal facts.
// Parent must compose it in MP's original fresh boundary turn, never call it as
// an independent replay of an old history row. No occurrence producer here.
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
	if q.subjectID <= 0 || q.libraryID <= 0 || q.eventID <= 0 || q.occurrence <= 0 || q.acceptedSequence < 0 || q.activityGeneration <= 0 || q.expectedRevision < 0 || q.expectedRevision == math.MaxInt64 || q.publicID == [16]byte{} || !uoPersonalText(q.library, 128) || !uoPersonalText(q.playback, 128) {
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
	// Compare all variable witness fields in SQL; no historical payload is copied.
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.history.boundary", q.eventID, kind, q.policy.account, q.policy.profileID, q.subjectID, q.publicID[:], q.library, q.playback, q.occurrence, q.acceptedSequence, h.activity, q.policy.memberBirth, q.policy.profileBirth); err != nil {
		return out, err
	}
	if q.expectedRevision == 0 {
		if _, err = wc008DraftInteger(ctx, turn, "uo.personal.intent.insert", q.policy.profile, q.publicID[:], q.subjectID, q.libraryID, h.activity, h.recommendation); err != nil {
			return out, err
		}
	}
	if q.completed {
		h, err = advanceUOPersonalHead(ctx, turn, q.policy, h)
		if err != nil {
			return out, err
		}
	}
	id := stmt.ID("uo.personal.history.start")
	if q.completed {
		id = "uo.personal.history.complete"
	}
	if _, err = wc008DraftInteger(ctx, turn, id, h.activity, h.activity, h.recommendation, q.playback, q.eventID, q.policy.profile, q.subjectID, q.publicID[:], max(int64(1), q.expectedRevision), q.playback); err != nil {
		return out, err
	}
	out.revision = max(int64(1), q.expectedRevision) + 1
	out.viewer, out.selection, err = bumpUOPersonalViewer(ctx, turn, q.policy.profile, q.library)
	return out, err
}
func uoPersonalHistoryDefinitions() []stmt.Definition {
	out := []stmt.Definition{
		uoPDef("uo.personal.history.highwater", stmt.Read, `SELECT COALESCE(max(id),0) FROM play_history`),
		uoPDef("uo.personal.history.clear", stmt.Write, `UPDATE personal_activity_revisions SET revision=?,activity_generation=?,recommendation_generation=?,history_visible_from_id=?,action_sequence=? WHERE profile_id=? AND revision=? AND action_sequence=? AND activity_generation=? AND recommendation_generation=? AND history_visible_from_id=? RETURNING revision`, uoPI("next_revision"), uoPI("activity"), uoPI("recommendation"), uoPI("cutoff"), uoPI("action"), uoPT("profile"), uoPI("revision"), uoPI("old_action"), uoPI("old_activity"), uoPI("old_recommendation"), uoPI("old_cutoff")),
		uoPDef("uo.personal.history.boundary", stmt.Read, `SELECT id FROM play_history WHERE id=? AND event_kind=? AND account_key=? AND profile_id=? AND subject_id=? AND item_public_id=? AND library_public_id=? AND playback_id=? AND occurrence_generation=? AND accepted_sequence=? AND activity_generation=? AND membership_incarnation=? AND profile_lifetime=?`, uoPI("event"), uoPT("kind"), uoPT("account"), uoPT("profile"), uoPI("subject"), uoPB("public"), uoPT("library"), uoPT("playback"), uoPI("occurrence"), uoPI("sequence"), uoPI("activity"), uoPT("member"), uoPT("birth")),
	}
	for _, completed := range []bool{false, true} {
		name, extra := "start", `started_count=CASE WHEN activity_generation=? THEN started_count+1 ELSE 1 END,last_started_at=h.occurred_at,last_played_at=h.occurred_at,`
		if completed {
			name = "complete"
			extra = `play_count=CASE WHEN activity_generation=? THEN play_count+1 ELSE 1 END,last_completed_at=h.occurred_at,watched=1,resume_completed=1,action_sequence=(SELECT action_sequence FROM personal_activity_revisions WHERE profile_id=p.profile_id),container_intent_sequence=(SELECT action_sequence FROM personal_activity_revisions WHERE profile_id=p.profile_id),`
		}
		owned := map[string]bool{"resume_playback_id": true, "activity_at": true, "activity_visible": true}
		if completed {
			owned["play_count"] = true
			owned["last_completed_at"] = true
			owned["watched"] = true
			owned["resume_completed"] = true
			owned["container_intent_sequence"] = true
		} else {
			owned["started_count"] = true
			owned["last_started_at"] = true
			owned["last_played_at"] = true
		}
		reset := strings.ReplaceAll(uoPersonalLazyReset(owned), "personal_items.profile_id", "p.profile_id")
		query := `UPDATE personal_items AS p INDEXED BY personal_current_live SET ` + reset + extra + `activity_generation=?,recommendation_generation=?,resume_playback_id=?,activity_at=h.occurred_at,activity_visible=1,revision=p.revision+1 FROM (SELECT occurred_at,playback_id FROM play_history WHERE id=?) h WHERE p.profile_id=? AND p.item_id=? AND p.item_public_id=? AND p.revision=? AND (p.evidence_fence_playback_id IS NULL OR p.evidence_fence_playback_id<>?) AND (p.dismissed_playback_id IS NULL OR p.dismissed_playback_id<>h.playback_id) AND p.play_count<9223372036854775807 AND p.started_count<9223372036854775807 RETURNING revision`
		// Event ID is used once in the query after the fixed SET witness arguments.
		out = append(out, uoPDef("uo.personal.history."+name, stmt.Write, query, uoPI("activity_old"), uoPI("activity"), uoPI("recommendation"), uoPT("playback"), uoPI("event"), uoPT("profile"), uoPI("subject"), uoPB("public"), uoPI("revision"), uoPT("fence")))
	}
	return out
}
