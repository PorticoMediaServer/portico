package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// DRAFT native facts from current authority in the SAME fresh commandTurn.
// Parent owns command IDs, lifetime/authority, receipts, quota, events and the
// physical terminal. Replay never enters these effects; every error rolls back
// the entire turn. A returned effect is never an Applied protocol result.
type uoPersonalPolicy struct {
	profile, authority, account, profileID, memberBirth, profileBirth string
	historyPolicy                                                     int64
}

func uoPersonalText(s string, maxBytes int) bool {
	return s != "" && len(s) <= maxBytes && utf8.ValidString(s)
}
func (p uoPersonalPolicy) valid() bool {
	return uoPersonalText(p.profile, 512) && uoPersonalText(p.authority, 128) && uoPersonalText(p.account, 128) && uoPersonalText(p.profileID, 128) && len(p.memberBirth) == 32 && len(p.profileBirth) == 32 && p.historyPolicy > 0
}

type uoPersonalHead struct{ revision, activity, recommendation, cutoff, action int64 }

func readUOPersonalHead(ctx context.Context, turn *commandTurn, p uoPersonalPolicy) (out uoPersonalHead, err error) {
	if !p.valid() {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	// Insert only the acting profile. Never replace a superseded policy witness.
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.head.ensure", p.profile, p.authority, p.account, p.profileID, p.memberBirth, p.profileBirth, p.historyPolicy); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, "uo.personal.head.read", p.profile, p.authority, p.account, p.profileID, p.memberBirth, p.profileBirth, p.historyPolicy)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.revision, &out.activity, &out.recommendation, &out.cutoff, &out.action); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.revision < 0 || out.revision == math.MaxInt64 || out.action < 0 || out.action == math.MaxInt64 || out.activity <= 0 || out.recommendation <= 0 || out.cutoff < 0 {
		return out, ErrConflict
	}
	return out, nil
}
func advanceUOPersonalHead(ctx context.Context, turn *commandTurn, p uoPersonalPolicy, h uoPersonalHead) (uoPersonalHead, error) {
	n, err := wc008DraftInteger(ctx, turn, "uo.personal.head.advance", p.profile, h.revision, h.action, h.activity, h.recommendation, h.cutoff)
	if err != nil {
		return uoPersonalHead{}, err
	}
	h.revision++
	h.action++
	if n != h.action {
		return uoPersonalHead{}, ErrResolveRequired
	}
	return h, nil
}
func bumpUOPersonalViewer(ctx context.Context, turn *commandTurn, profile, library string) (viewer, selection int64, err error) {
	row, err := wc008DraftQuery(ctx, turn, uoPersonalFlagsRevisionRead, profile, library)
	if err != nil {
		return 0, 0, err
	}
	err = row.Scan(&viewer, &selection)
	if errors.Is(err, sql.ErrNoRows) {
		row, err = wc008DraftQuery(ctx, turn, uoPersonalFlagsRevisionInsert, profile, library, int64(1), int64(1))
	} else if err == nil {
		if viewer < 0 || selection < 0 || viewer == math.MaxInt64 || selection == math.MaxInt64 {
			return 0, 0, ErrConflict
		}
		row, err = wc008DraftQuery(ctx, turn, uoPersonalFlagsRevisionUpdate, viewer+1, selection+1, profile, library, viewer, selection)
	}
	if err != nil {
		return 0, 0, err
	}
	err = row.Scan(&viewer, &selection)
	return
}

type uoPersonalIntentInput struct {
	policy                                                                    uoPersonalPolicy
	subjectID, libraryID, entityRevision, admissionRevision, expectedRevision int64
	publicID                                                                  [16]byte
	library                                                                   string
	watched, container, dismiss                                               bool
	// Trusted clock and captured generation, never a client timestamp/generation.
	activityGeneration int64
	at                 string
}
type uoPersonalIntentEffect struct {
	revision, action, membership, viewer, selection int64
	watched, dismissed                              bool
}

// SetWatchedIntent(601) and DismissContinue(602). Container undo is a NEW
// action restoring its previous watched value, with the current watermark;
// expectedRevision prevents undo from erasing a successor intent. No child row,
// occurrence, play time or immutable history is manufactured or enumerated.
func applyUOPersonalIntent(ctx context.Context, turn *commandTurn, q uoPersonalIntentInput) (out uoPersonalIntentEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.policy.valid() || !uoPersonalText(q.library, 128) || !uoPersonalText(q.at, 64) || q.subjectID <= 0 || q.libraryID <= 0 || q.entityRevision <= 0 || q.admissionRevision <= 0 || q.expectedRevision < 0 || q.expectedRevision == math.MaxInt64 || q.publicID == [16]byte{} || q.activityGeneration <= 0 || q.dismiss && (q.watched || q.container) {
		return out, ErrInvalid
	}
	h, err := readUOPersonalHead(ctx, turn, q.policy)
	if err != nil {
		return out, err
	}
	if h.activity != q.activityGeneration {
		return out, ErrConflict
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.intent.subject", q.subjectID, q.publicID[:], q.libraryID, q.entityRevision, q.admissionRevision, q.library, uoFlagInteger(q.container)); err != nil {
		return out, err
	}
	var revision int64
	row, err := wc008DraftQuery(ctx, turn, "uo.personal.intent.current", q.policy.profile, q.subjectID, q.publicID[:])
	if err != nil {
		return out, err
	}
	err = row.Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		revision = 0
	} else if err != nil {
		return out, err
	}
	if revision != q.expectedRevision {
		return out, ErrConflict
	}
	if revision == 0 {
		if _, err = wc008DraftInteger(ctx, turn, "uo.personal.intent.insert", q.policy.profile, q.publicID[:], q.subjectID, q.libraryID, h.activity, h.recommendation); err != nil {
			return out, err
		}
	}
	h, err = advanceUOPersonalHead(ctx, turn, q.policy, h)
	if err != nil {
		return out, err
	}
	if q.container {
		out.membership, err = wc008DraftInteger(ctx, turn, "uo.personal.intent.clock")
		if err != nil {
			return out, err
		}
	}
	next := q.expectedRevision + 1
	if q.expectedRevision == 0 {
		next = 1
	}
	id := stmt.ID("uo.personal.intent.watched")
	args := []any{next, h.action, h.activity, h.recommendation, uoFlagInteger(q.watched), q.at, uoFlagInteger(q.container), out.membership, h.action, q.policy.profile, q.subjectID, q.publicID[:], max(int64(1), q.expectedRevision)}
	if q.dismiss {
		id = "uo.personal.intent.dismiss"
		args = []any{next, h.action, h.activity, h.recommendation, q.at, q.policy.profile, q.subjectID, q.publicID[:], max(int64(1), q.expectedRevision)}
	}
	if _, err = wc008DraftInteger(ctx, turn, id, args...); err != nil {
		return out, err
	}
	out.revision, out.action, out.watched, out.dismissed = next, h.action, q.watched, q.dismiss
	out.viewer, out.selection, err = bumpUOPersonalViewer(ctx, turn, q.policy.profile, q.library)
	return out, err
}

// Finite definition builders shared ONLY by these personal draft families.
func uoPI(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
func uoPT(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
func uoPB(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
func uoPDef(id string, kind stmt.Kind, query string, params ...stmt.Param) stmt.Definition {
	return stmt.Definition{ID: stmt.ID(id), Owner: "store/writer UO personal draft", Kind: kind, SQL: query, Params: params, CallSites: []string{"store/writer/uo_personal_intent.go", "store/writer/uo_personal_history.go", "store/writer/uo_personal_offline.go", "store/writer/uo_personal_bulk.go"}, Plan: stmt.Plan{Hot: true}}
}
func uoPersonalIntentDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		uoPDef("uo.personal.head.ensure", stmt.Write, `INSERT INTO personal_activity_revisions(profile_id,policy_authority,policy_account_id,policy_profile_id,policy_member_incarnation,policy_profile_lifetime,history_policy_generation) VALUES(?,?,?,?,?,?,?) ON CONFLICT(profile_id) DO UPDATE SET revision=revision RETURNING revision`, uoPT("profile"), uoPT("authority"), uoPT("account"), uoPT("profile_id"), uoPT("member"), uoPT("birth"), uoPI("policy")),
		uoPDef("uo.personal.head.read", stmt.Read, `SELECT revision,activity_generation,recommendation_generation,history_visible_from_id,action_sequence FROM personal_activity_revisions WHERE profile_id=? AND policy_authority=? AND policy_account_id=? AND policy_profile_id=? AND policy_member_incarnation=? AND policy_profile_lifetime=? AND history_policy_generation=?`, uoPT("profile"), uoPT("authority"), uoPT("account"), uoPT("profile_id"), uoPT("member"), uoPT("birth"), uoPI("policy")),
		uoPDef("uo.personal.head.advance", stmt.Write, `UPDATE personal_activity_revisions SET revision=revision+1,action_sequence=action_sequence+1 WHERE profile_id=? AND revision=? AND action_sequence=? AND activity_generation=? AND recommendation_generation=? AND history_visible_from_id=? RETURNING action_sequence`, uoPT("profile"), uoPI("revision"), uoPI("action"), uoPI("activity"), uoPI("recommendation"), uoPI("cutoff")),
		uoPDef("uo.personal.intent.subject", stmt.Read, `SELECT e.id FROM catalog_entities e CROSS JOIN catalog_subjects s ON s.id=e.id CROSS JOIN catalog_kinds k ON k.id=s.kind CROSS JOIN catalog_libraries l ON l.id=s.library_id WHERE e.id=? AND s.public_id=? AND s.library_id=? AND e.revision=? AND e.admission_revision=? AND l.library_id=? AND k.container=? AND e.public_id=s.public_id AND e.retired=0 AND l.retired=0`, uoPI("subject"), uoPB("public"), uoPI("library"), uoPI("revision"), uoPI("admission"), uoPT("library_key"), uoPI("container")),
		uoPDef("uo.personal.intent.current", stmt.Read, `SELECT revision FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=? AND item_public_id=?`, uoPT("profile"), uoPI("subject"), uoPB("public")),
		uoPDef("uo.personal.intent.insert", stmt.Write, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,activity_generation,recommendation_generation) VALUES(?,?,?,?,?,?) RETURNING revision`, uoPT("profile"), uoPB("public"), uoPI("subject"), uoPI("library"), uoPI("activity"), uoPI("recommendation")),
		uoPDef("uo.personal.intent.clock", stmt.Read, `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`),
	}
	defs = append(defs, uoPDef("uo.personal.intent.watched", stmt.Write, `UPDATE personal_items INDEXED BY personal_current_live SET revision=?,action_sequence=?,activity_generation=?,recommendation_generation=?,watched=?,watched_at=?,container_membership_sequence=CASE WHEN ?=1 THEN ? ELSE NULL END,container_intent_sequence=?,evidence_fence_playback_id=resume_playback_id,resume_position=0,resume_completed=0,activity_visible=0 WHERE profile_id=? AND item_id=? AND item_public_id=? AND revision=? RETURNING revision`, uoPI("revision"), uoPI("action"), uoPI("activity"), uoPI("recommendation"), uoPI("watched"), uoPT("at"), uoPI("container"), uoPI("membership"), uoPI("intent"), uoPT("profile"), uoPI("subject"), uoPB("public"), uoPI("old")))
	defs = append(defs, uoPDef("uo.personal.intent.dismiss", stmt.Write, `UPDATE personal_items INDEXED BY personal_current_live SET revision=?,action_sequence=?,activity_generation=?,recommendation_generation=?,dismissed_playback_id=resume_playback_id,dismissed_child_public_id=resume_item_public_id,dismissed_activity_at=?,activity_visible=0 WHERE profile_id=? AND item_id=? AND item_public_id=? AND revision=? RETURNING revision`, uoPI("revision"), uoPI("action"), uoPI("activity"), uoPI("recommendation"), uoPT("at"), uoPT("profile"), uoPI("subject"), uoPB("public"), uoPI("old")))
	// Every generation-stale field is cleared before stamping this one row.
	for n := range defs {
		if defs[n].ID == "uo.personal.intent.watched" {
			defs[n].SQL = strings.Replace(defs[n].SQL, "SET revision=", "SET "+uoPersonalLazyReset(map[string]bool{"watched": true, "watched_at": true, "resume_position": true, "resume_completed": true, "activity_visible": true, "container_membership_sequence": true, "container_intent_sequence": true, "evidence_fence_playback_id": true})+"revision=", 1)
		}
		if defs[n].ID == "uo.personal.intent.dismiss" {
			defs[n].SQL = strings.Replace(defs[n].SQL, "SET revision=", "SET "+uoPersonalLazyReset(map[string]bool{"dismissed_playback_id": true, "dismissed_child_public_id": true, "dismissed_activity_at": true, "activity_visible": true})+"revision=", 1)
		}
	}
	for _, d := range uoPersonalFlagsDefinitions() {
		if d.ID == uoPersonalFlagsRevisionRead || d.ID == uoPersonalFlagsRevisionInsert || d.ID == uoPersonalFlagsRevisionUpdate {
			defs = append(defs, d)
		}
	}
	return defs
}

// Closed schema column set, never a callback, client field list or JSON model.
// All RHS expressions see the OLD generation, as SQLite specifies. Reused by
// MP-boundary composition so generation changes cannot revive stale activity.
func uoPersonalLazyReset(owned map[string]bool) string {
	var out string
	groups := []struct {
		columns []string
		zero    string
	}{
		{[]string{"play_count", "started_count", "played_ms", "shared_play_count", "watched", "resume_position", "resume_unit", "resume_completed"}, "0"},
		{[]string{"watched_at", "shared_last_played_at", "last_played_at", "last_started_at", "last_completed_at", "resume_playback_id", "resume_source_revision", "resume_asset_id", "resume_item_public_id", "activity_at", "activity_state", "evidence_fence_playback_id", "dismissed_playback_id", "dismissed_child_public_id", "dismissed_activity_at", "container_watermark", "container_cleared_through", "container_membership_sequence", "container_intent_sequence", "reading_format", "reading_cfi", "reading_section", "reading_page_index", "reading_progression", "reading_revision", "reading_observed_ms", "chapter_manifest_digest", "chapter_asset_id", "chapter_asset_revision", "chapter_asset_size", "chapter_asset_modified_ns", "chapter_completed_bits"}, "NULL"},
	}
	for _, g := range groups {
		for _, c := range g.columns {
			if !owned[c] {
				out += c + "=CASE WHEN activity_generation=(SELECT activity_generation FROM personal_activity_revisions WHERE profile_id=personal_items.profile_id) THEN " + c + " ELSE " + g.zero + " END,"
			}
		}
	}
	for _, c := range []string{"radio_last_started_at", "radio_last_session_id"} {
		if !owned[c] {
			out += c + "=CASE WHEN recommendation_generation=(SELECT recommendation_generation FROM personal_activity_revisions WHERE profile_id=personal_items.profile_id) THEN " + c + " ELSE NULL END,"
		}
	}
	return out
}
