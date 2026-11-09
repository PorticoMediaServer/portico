package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type mgRuleRecordingRow struct {
	id, ruleID, state                       string
	owner                                   mgRecordingOwner
	programme                               string
	revision, ruleRevision, manual, updated int64
}

func mgRuleSeedRecording(t *testing.T, w *executor, v mgRuleCommandValues, r mgRuleRecordingRow) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,rule_id,rule_revision,manual,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation)
VALUES(?,?,'local',?,?,?,'rule-channel',?,?,'{}','{}',0,1000,0,?,?,?,?,?,0,?,x'00000000000000000000000000000000',x'00000000000000000000000000000000',x'0000000000000000000000000000000000000000000000000000000000000000',?,?,?)`,
		r.id, r.owner.key(), r.owner.account, r.owner.profile, v.source, r.programme, v.generation,
		r.revision, r.state, r.ruleID, r.ruleRevision, r.manual, r.updated,
		r.owner.memberIncarnation, r.owner.profileLifetime, v.generation); err != nil {
		t.Fatal(err)
	}
}

func mgRuleDeleteKeepFixture(t *testing.T) (*executor, mgRuleCommandValues, string) {
	t.Helper()
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id := strings.Repeat("e", 64)
	config := mgRuleValuesConfig(v, nil)
	save := mgRuleSaveRequestFor(v, id, 0, config, mgRulePreparedWire(config))
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), save), save); err != nil {
		t.Fatal(err)
	}
	mgRuleCommit(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	return w, v, id
}

func mgRuleRecordingValues(t *testing.T, w *executor, id string) (manual, ruleRevision, revision int64, ruleID string) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT manual,rule_revision,revision,rule_id FROM dvr_recordings WHERE id=?`, id).Scan(&manual, &ruleRevision, &revision, &ruleID); err != nil {
		t.Fatal(err)
	}
	return manual, ruleRevision, revision, ruleID
}

// Legacy DeleteRule(future=keep) detaches the rule's scheduled, conflicted and
// waiting intents in the delete transaction. The MG delete must do the same or
// the accepted receipt is a lie: the rule row is gone while its future intents
// stay attached to it.
func TestMGRuleCommandDeleteKeepDetachesRecordings(t *testing.T) {
	w, v, id := mgRuleDeleteKeepFixture(t)
	fake := mgPersonalFake(t, v.personal)
	otherRule, foreign := strings.Repeat("7", 64), mgRecordingOwner{
		account: "other-account", profile: "other-profile", memberIncarnation: strings.Repeat("3", 32),
		profileLifetime: strings.Repeat("4", 32), accountEpoch: 1, memberRevision: 1, profileRevision: 1,
	}
	attached := []mgRuleRecordingRow{
		{id: strings.Repeat("1", 64), ruleID: id, state: "scheduled", owner: v.owner, programme: "programme-1", revision: 3, ruleRevision: 4},
		{id: strings.Repeat("2", 64), ruleID: id, state: "conflicted", owner: v.owner, programme: "programme-2", revision: 4, ruleRevision: 4},
		{id: strings.Repeat("3", 64), ruleID: id, state: "waiting-source", owner: v.owner, programme: "programme-3", revision: 5, ruleRevision: 4},
		{id: strings.Repeat("4", 64), ruleID: id, state: "waiting-guide", owner: v.owner, programme: "programme-4", revision: 6, ruleRevision: 4},
	}
	untouched := []mgRuleRecordingRow{
		{id: strings.Repeat("5", 64), ruleID: id, state: "completed", owner: v.owner, programme: "programme-5", revision: 7, ruleRevision: 4},
		{id: strings.Repeat("6", 64), ruleID: otherRule, state: "scheduled", owner: v.owner, programme: "programme-6", revision: 8, ruleRevision: 9},
		{id: strings.Repeat("8", 64), ruleID: id, state: "scheduled", owner: foreign, programme: "programme-8", revision: 9, ruleRevision: 4},
	}
	for _, r := range append(append([]mgRuleRecordingRow{}, attached...), untouched...) {
		mgRuleSeedRecording(t, w, v, r)
	}

	deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
	turn := mgRuleCommandTurn(t, w, MGDeleteRule)
	deleted, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest), deleteRequest)
	if err != nil || deleted.decision != receiptFresh || deleted.rule.detached != int64(len(attached)) {
		t.Fatal(deleted, err)
	}
	mgRuleCommit(t, turn)

	for _, r := range attached {
		manual, ruleRevision, revision, ruleID := mgRuleRecordingValues(t, w, r.id)
		if manual != 1 || ruleID != "" || ruleRevision != 0 || revision != r.revision+1 {
			t.Fatalf("%s not detached: manual=%d rule=%q rule_revision=%d revision=%d", r.state, manual, ruleID, ruleRevision, revision)
		}
	}
	for _, r := range untouched {
		manual, ruleRevision, revision, ruleID := mgRuleRecordingValues(t, w, r.id)
		if manual != r.manual || ruleID != r.ruleID || ruleRevision != r.ruleRevision || revision != r.revision {
			t.Fatalf("%s touched: manual=%d rule=%q rule_revision=%d revision=%d", r.id, manual, ruleID, ruleRevision, revision)
		}
	}
}

func TestMGRuleCommandDeleteKeepReplayHasNoEffects(t *testing.T) {
	w, v, id := mgRuleDeleteKeepFixture(t)
	fake := mgPersonalFake(t, v.personal)
	row := mgRuleRecordingRow{id: strings.Repeat("1", 64), ruleID: id, state: "scheduled", owner: v.owner, programme: "programme-1", revision: 3, ruleRevision: 4}
	mgRuleSeedRecording(t, w, v, row)

	deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
	reference := mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest)
	turn := mgRuleCommandTurn(t, w, MGDeleteRule)
	first, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest)
	if err != nil || first.decision != receiptFresh || first.rule.detached != 1 {
		t.Fatal(first, err)
	}
	mgRuleCommit(t, turn)

	// A later unrelated advance of a detached intent is not a new delete effect:
	// replay returns the original saved count and digest and writes nothing.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET revision=revision+10 WHERE id=?`, row.id); err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGDeleteRule)
	replay, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest)
	if err != nil || replay.decision != receiptReplay || replay.rule != first.rule || replay.digest != first.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	manual, ruleRevision, revision, ruleID := mgRuleRecordingValues(t, w, row.id)
	if manual != 1 || ruleID != "" || ruleRevision != 0 || revision != row.revision+11 {
		t.Fatal("replay wrote a detached intent", manual, ruleID, ruleRevision, revision)
	}
}

func TestMGRuleCommandDeleteCancelDoesNotDetach(t *testing.T) {
	w, v, id := mgRuleDeleteKeepFixture(t)
	fake := mgPersonalFake(t, v.personal)
	row := mgRuleRecordingRow{id: strings.Repeat("1", 64), ruleID: id, state: "scheduled", owner: v.owner, programme: "programme-1", revision: 3, ruleRevision: 4}
	mgRuleSeedRecording(t, w, v, row)

	deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
	deleteRequest.input.future = "cancel"
	turn := mgRuleCommandTurn(t, w, MGDeleteRule)
	deleted, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest), deleteRequest)
	if err != nil || deleted.decision != receiptFresh || deleted.rule.detached != 0 {
		t.Fatal(deleted, err)
	}
	mgRuleCommit(t, turn)
	if manual, ruleRevision, revision, ruleID := mgRuleRecordingValues(t, w, row.id); manual != 0 || ruleID != id || ruleRevision != 4 || revision != 3 {
		t.Fatal("cancel delete detached an intent", manual, ruleID, ruleRevision, revision)
	}
}

// The detach is bounded. One intent beyond the ceiling is a typed refusal that
// rolls the whole delete frame back: no rule row change, no detach, no receipt.
func TestMGRuleCommandDeleteKeepRefusesBeyondBound(t *testing.T) {
	w, v, id := mgRuleDeleteKeepFixture(t)
	fake := mgPersonalFake(t, v.personal)
	// One row beyond the ceiling, seeded in a single recursive statement.
	if _, err := w.db.ExecContext(t.Context(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,options_json,start_ms,end_ms,priority,revision,state,rule_id,rule_revision,manual,created_ms,updated_ms,command_incarnation,create_operation_id,birth_request_digest,command_member_incarnation,command_profile_lifetime,guide_pin_generation)
SELECT printf('%064d',x),?,'local',?,?,?,'rule-channel',printf('programme-%05d',x),?,'{}','{}',0,1000,0,3,'scheduled',?,4,0,0,0,x'00000000000000000000000000000000',x'00000000000000000000000000000000',x'0000000000000000000000000000000000000000000000000000000000000000',?,?,? FROM n`,
		int64(mgRuleDetachMaxRecordings+1), v.owner.key(), v.owner.account, v.owner.profile, v.source,
		v.generation, id, v.owner.memberIncarnation, v.owner.profileLifetime, v.generation); err != nil {
		t.Fatal(err)
	}
	deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
	turn := mgRuleCommandTurn(t, w, MGDeleteRule)
	got, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest), deleteRequest)
	var refusal *PolicyError
	if !errors.Is(err, ErrCap) || !errors.As(err, &refusal) || refusal.Field != "mg_rule_delete_recordings" || refusal.Limit != mgRuleDetachMaxRecordings || got != (mgRuleAcceptance{}) {
		t.Fatal("unbounded detach did not refuse", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var deleted, enabled, attached, receipts int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT deleted FROM dvr_rules WHERE id=?),(SELECT enabled FROM dvr_rules WHERE id=?),(SELECT count(*) FROM dvr_recordings WHERE rule_id=? AND manual=0),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?)`, id, id, id, v.root).Scan(&deleted, &enabled, &attached, &receipts); err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || enabled != 1 || attached != int64(mgRuleDetachMaxRecordings+1) || receipts != 1 {
		t.Fatal("refused frame leaked an effect", deleted, enabled, attached, receipts)
	}
}

// A denial after the native rule/detach effects but before Persist rolls the
// whole frame back; a refusal must never leave detached intents behind.
func TestMGRuleCommandDeleteKeepRollsBackOnLateDenial(t *testing.T) {
	w, v, id := mgRuleDeleteKeepFixture(t)
	fake := mgPersonalFake(t, v.personal)
	row := mgRuleRecordingRow{id: strings.Repeat("1", 64), ruleID: id, state: "scheduled", owner: v.owner, programme: "programme-1", revision: 3, ruleRevision: 4}
	mgRuleSeedRecording(t, w, v, row)
	seam := &mgRuleDenyAfterRechecks{inner: fake, after: 2}
	deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
	turn := mgRuleCommandTurn(t, w, MGDeleteRule)
	got, err := acceptMGRuleDelete(t.Context(), turn, seam, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest), deleteRequest)
	if !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
		t.Fatal("late denial was not reported", got, err)
	}
	if seam.count <= seam.after {
		t.Fatal("denial preceded the delete effects", seam.count)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var deleted, enabled, receipts, sequence int64
	var manual, ruleRevision, revision int64
	var ruleID string
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT deleted FROM dvr_rules WHERE id=?),(SELECT enabled FROM dvr_rules WHERE id=?),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, id, id, v.root, v.root).Scan(&deleted, &enabled, &receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT manual,rule_revision,revision,rule_id FROM dvr_recordings WHERE id=?`, row.id).Scan(&manual, &ruleRevision, &revision, &ruleID); err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || enabled != 1 || receipts != 1 || sequence != 1 || manual != 0 || ruleRevision != 4 || revision != 3 || ruleID != id {
		t.Fatal("late denial leaked an effect", deleted, enabled, receipts, sequence, manual, ruleRevision, revision, ruleID)
	}
}

// The seam stands in only for the unminted current-session authorization. This
// wrapper denies the nth+1 current-authority recheck, which is the Persist
// recheck that follows the native effects.
type mgRuleDenyAfterRechecks struct {
	inner mgPersonalRootSeam
	after int
	count int
}

func (s *mgRuleDenyAfterRechecks) authorizeUSCurrentSession(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgPersonalRootAuthorization, error) {
	session, err := s.inner.authorizeUSCurrentSession(ctx, turn, witness, lifetime)
	if err != nil || session == nil {
		return session, err
	}
	return &mgRuleDenySession{inner: session, seam: s}, nil
}

type mgRuleDenySession struct {
	inner mgPersonalRootAuthorization
	seam  *mgRuleDenyAfterRechecks
}

func (s *mgRuleDenySession) mgPersonalRootID() string { return s.inner.mgPersonalRootID() }
func (s *mgRuleDenySession) mgPersonalRootIdentity() uoRootIdentity {
	return s.inner.mgPersonalRootIdentity()
}
func (s *mgRuleDenySession) state(ctx context.Context, turn *commandTurn) (string, error) {
	return s.inner.state(ctx, turn)
}
func (s *mgRuleDenySession) admitTarget(ctx context.Context, turn *commandTurn, target mgPersonalAdmissionTarget) error {
	return s.inner.admitTarget(ctx, turn, target)
}
func (s *mgRuleDenySession) recheck(ctx context.Context, turn *commandTurn) (err error) {
	s.seam.count++
	if s.seam.count > s.seam.after {
		return ErrDisclosureDenied
	}
	return s.inner.recheck(ctx, turn)
}
