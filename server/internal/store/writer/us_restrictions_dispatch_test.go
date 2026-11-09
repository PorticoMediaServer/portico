package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	usRestrictionsOwnerID     = "security-owner"
	usRestrictionsProfileID   = "restricted-profile"
	usRestrictionsProfileLife = "dddddddddddddddddddddddddddddddd"
)

// Synthetic harness only. US-01 still mints the authority stamp after its own
// current-source checks; nothing here constructs a production issuer, registers
// a product command or qualifies a hold. The executed SQL is the real closed
// statement set on the canonical schema.
func usRestrictionsRequestFor(proof *usOwnerAuthorityProof) usRestrictionsRequest {
	i := proof.witness.issuer
	return usRestrictionsRequest{
		ownerID: usRestrictionsOwnerID, issuer: i, account: i.account, memberIncarnation: i.memberIncarnation,
		profile: usRestrictionsProfileID, profileLifetime: usRestrictionsProfileLife, expectedRevision: 1,
		ratingSystem: "mpaa", maximumAgeRating: "PG-13", allowUnrated: false,
		blockedLabels: []string{"violence", "language"}, allowDownloads: true, allowLiveTV: false, allowDVR: false, allowWatchTogether: true,
	}
}

func usRestrictionsIdentity() CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
}

func usRestrictionsFixture(t *testing.T, seed bool, edit func(*usRestrictionsRequest)) (w *executor, path string, r *stmt.Registry, proof *usOwnerAuthorityProof, command *usRestrictionsCommand) {
	t.Helper()
	// The issuer session and its proof come from the reviewed US-01 fixture, so
	// this task adds no new producer of its own for the ordinary family/device.
	w, _, proof = usOwnerAuthorityFixtureIssuer(t, nil)
	if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	identity, request := usRestrictionsIdentity(), usRestrictionsRequestFor(proof)
	if edit != nil {
		edit(&request)
	}
	var err error
	command, err = prepareUSRestrictions(USProfileRestrictions, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	if request.profile != request.issuer.profile && request.account == request.issuer.account {
		if _, err = w.db.ExecContext(t.Context(), `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,position) VALUES(?,?,?,'Restricted',0,2)`,
			request.profile, request.account, request.profileLifetime); err != nil {
			t.Fatal(err)
		}
	}
	if seed && request.account == request.issuer.account {
		if _, err = w.db.ExecContext(t.Context(), `INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES(?,'mpaa','R',17,1,'["old"]',1,1,0,1,1)`,
			request.profile); err != nil {
			t.Fatal(err)
		}
	}
	i := request.issuer
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		request.ownerID, identity.Incarnation[:], i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family)
	defs := append([]stmt.Definition(nil), usAuthorityDefinitions()...)
	defs = append(defs, usRestrictionsDefinitions()...)
	defs = append(defs, usRestrictionsReceiptDefinitions()...)
	r, err = stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, path, r, proof, command
}

func usRestrictionsTurn(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID, memory uint64) (*commandTurn, *usOwnerAuthorization, context.CancelFunc) {
	t.Helper()
	if memory == 0 {
		memory = 4 << 20
	}
	recipe := testRecipe()
	recipe.limit = Work{100000, 10_000_000, 100000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{8, 5000, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	lifetime, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	budget := usOwnerAuthorityBudget{memory: memory, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, lifetime, budget)
	if err != nil {
		t.Fatal(err)
	}
	return turn, a, cancel
}

func usRestrictionsInput(t *testing.T, c *usRestrictionsCommand) *usRestrictionsInvocation {
	t.Helper()
	q, err := usRestrictionsReceiptForCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	return &usRestrictionsInvocation{request: q, input: c}
}

func usRestrictionsQuery(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

func usRestrictionsSource(t *testing.T, w *executor, profile string) (revision int64, system, code, labels string, dvr int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,rating_system,maximum_age_rating,blocked_labels,allow_dvr FROM profile_restrictions WHERE profile_id=?`, profile).
		Scan(&revision, &system, &code, &labels, &dvr); err != nil {
		t.Fatal(err)
	}
	return revision, system, code, labels, dvr
}

func usRestrictionsReplay(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID, inv *usRestrictionsInvocation) (usRestrictionsDispatchResult, AckResult) {
	t.Helper()
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, kind, 0)
	out, floor, err := runUSRestrictionsDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	return out, floor
}

// Original, then replay/Resolve of the same identity, then the floor-only ACK.
func TestUSRestrictionsDispatchPhysicalOriginalReplayAndACK(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	inv := usRestrictionsInput(t, input)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.revision != 2 || out.Digest == (ResultDigest{}) {
		t.Fatal("original was not the closed accepted result", out)
	}
	if out.Value.ratingSystem != "mpaa" || out.Value.maximumAgeRating != "PG-13" || out.Value.maximumAge != 13 ||
		out.Value.allowUnrated || !out.Value.allowDownloads || out.Value.allowLiveTV || out.Value.allowDVR || !out.Value.allowWatchTogether {
		t.Fatal("complete DTO fields", out.Value)
	}
	if !reflect.DeepEqual(out.Value.labels, []string{"language", "violence"}) || out.ChargedBytes <= 0 || out.OwnerCount != 1 || out.OwnerBytes != out.ChargedBytes {
		t.Fatal("original accounting", out)
	}
	revision, system, code, labels, _ := usRestrictionsSource(t, w, usRestrictionsProfileID)
	if revision != 2 || system != "mpaa" || code != "PG-13" || labels != `["language","violence"]` {
		t.Fatal("original source effects", revision, system, code, labels)
	}
	// The product applies restrictions live and appends no security event.
	if n := usRestrictionsQuery(t, w, `SELECT count(*) FROM api_events`); n != 0 {
		t.Fatal("restrictions replacement appended an event", n)
	}
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_results`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_result_labels`) != 2 {
		t.Fatal("receipt cohort", out)
	}

	// Replay: the SAME identity again, now without its preparation. Zero new
	// effects and no live-source read.
	before := usRestrictionsQuery(t, w, `SELECT total_changes()`)
	replay, _ := usRestrictionsReplay(t, w, r, proof, input.kind, &usRestrictionsInvocation{request: inv.request})
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Digest != out.Digest || !reflect.DeepEqual(replay.Value, out.Value) {
		t.Fatal("replay did not return the saved original", replay)
	}
	if replay.ChargedBytes != 0 {
		t.Fatal("replay charged again", replay)
	}
	if after := usRestrictionsQuery(t, w, `SELECT total_changes()`); after != before {
		t.Fatal("replay made new effects", before, after)
	}
	resolve, _ := usRestrictionsReplay(t, w, r, proof, input.kind, inv)
	if resolve.Outcome.State != Applied || !resolve.Outcome.Replayed || !reflect.DeepEqual(resolve.Value, out.Value) {
		t.Fatal("resolve with preparation re-executed", resolve)
	}

	// Floor-only ACK: it advances one sequence and refunds nothing.
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	ack, floor, err := runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: inv.request, ack: true})
	if err != nil || floor.AcknowledgedThrough != 1 || !reflect.DeepEqual(ack, usRestrictionsDispatchResult{}) {
		t.Fatal("ACK", ack, floor, err)
	}
	if usRestrictionsQuery(t, w, `SELECT acknowledged_sequence FROM security_command_owners`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT retained_count FROM security_command_owners`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT retained_variable_bytes FROM security_command_owners`) != out.ChargedBytes {
		t.Fatal("ACK refunded or discarded the retained bytes")
	}
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_results`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_result_labels`) != 2 {
		t.Fatal("ACK physically removed the cohort")
	}
	retired, _ := usRestrictionsReplay(t, w, r, proof, input.kind, &usRestrictionsInvocation{request: inv.request})
	if retired.Outcome.State != ResultRetired || retired.Value.ownerID != "" {
		t.Fatal("ACKed original was served as Applied", retired)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if _, floor, err = runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: inv.request, ack: true}); err != nil || floor.AcknowledgedThrough != 1 {
		t.Fatal("ACK was not idempotent", floor, err)
	}
}

// The replacement always writes and always advances the revision by exactly one
// from the ACTUAL source revision, even when the document is unchanged.
func TestUSRestrictionsDispatchActualRevision(t *testing.T) {
	w, _, r, proof, first := usRestrictionsFixture(t, true, nil)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, first.kind, 0)
	original, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, first))
	if err != nil || original.Value.revision != 2 {
		t.Fatal(original, err)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, first.kind, 0)
	if _, _, err = runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: usRestrictionsInput(t, first).request, ack: true}); err != nil {
		t.Fatal(err)
	}
	// A second, identical replacement at the next retained sequence.
	identity := first.identity
	identity.Sequence = 2
	identity.OperationID = [16]byte{3}
	request := first.request
	request.expectedRevision = 2
	next, err := prepareUSRestrictions(USProfileRestrictions, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, first.kind, 0)
	out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, next))
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Value.revision != 3 {
		t.Fatal("identical replacement did not advance the actual revision", out)
	}
	if revision, _, _, labels, _ := usRestrictionsSource(t, w, usRestrictionsProfileID); revision != 3 || labels != `["language","violence"]` {
		t.Fatal("source revision", revision, labels)
	}
	// ACK the second original so the next retained sequence is admissible.
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, first.kind, 0)
	if _, _, err = runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: usRestrictionsInput(t, next).request, ack: true}); err != nil {
		t.Fatal(err)
	}
	// A stale expected revision conflicts and writes nothing new.
	identity.Sequence = 3
	identity.OperationID = [16]byte{4}
	stale := first.request
	stale.expectedRevision = 1
	conflict, err := prepareUSRestrictions(USProfileRestrictions, identity, stale)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, first.kind, 0)
	if out, _, err = runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, conflict)); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
		t.Fatal("stale revision", out, err)
	}
	if revision, _, _, _, _ := usRestrictionsSource(t, w, usRestrictionsProfileID); revision != 3 {
		t.Fatal("conflicting original wrote a prefix", revision)
	}
}

// A profile with no stored document is inserted at the documented default
// revision 1, and a submission that expected revision 2 conflicts.
func TestUSRestrictionsDispatchFirstReplacementInsert(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, false, func(q *usRestrictionsRequest) {
		// The issuer's own primary profile has no stored restriction row.
		q.profile, q.profileLifetime = q.issuer.profile, q.issuer.profileLifetime
	})
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, input))
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Value.revision != 2 || out.Value.profile != input.request.profile {
		t.Fatal("first replacement", out)
	}
	if revision, _, _, labels, _ := usRestrictionsSource(t, w, input.request.profile); revision != 2 || labels != `["language","violence"]` {
		t.Fatal("inserted source row", revision, labels)
	}
	// expectedRevision 2 against a MISSING row still means the default is 1.
	w, _, r, proof, fresh := usRestrictionsFixture(t, false, func(q *usRestrictionsRequest) {
		q.profile, q.profileLifetime = q.issuer.profile, q.issuer.profileLifetime
		q.expectedRevision = 2
	})
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, fresh.kind, 0)
	if out, _, err = runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, fresh)); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
		t.Fatal("expected 2 against the default revision", out, err)
	}
}

// A denial is a denial before any receipt or source effect, with no ordering or
// label detail leaked.
func TestUSRestrictionsDispatchDenialAndOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seed  bool
		edit  func(*usRestrictionsRequest)
		setup func(*executor, *usRestrictionsCommand)
		want  error
	}{
		{"deleted_profile", true, nil, func(w *executor, c *usRestrictionsCommand) {
			wc008NativeExec(t, w, `UPDATE direct_profiles SET deleted=1 WHERE id=?`, c.request.profile)
		}, ErrDisclosureDenied},
		{"wrong_lifetime", true, nil, func(w *executor, c *usRestrictionsCommand) {
			wc008NativeExec(t, w, `UPDATE direct_profiles SET lifetime=? WHERE id=?`, strings.Repeat("9", 32), c.request.profile)
		}, ErrDisclosureDenied},
		{"cross_account", false, func(q *usRestrictionsRequest) { q.account = "other-account" }, nil, ErrDisclosureDenied},
		{"stale_revision", true, func(q *usRestrictionsRequest) { q.expectedRevision = 2 }, nil, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, r, proof, input := usRestrictionsFixture(t, tc.seed, tc.edit)
			if tc.setup != nil {
				tc.setup(w, input)
			}
			turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
			out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, input))
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
				t.Fatal(out, err)
			}
			if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
				usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_results`) != 0 ||
				usRestrictionsQuery(t, w, `SELECT last_sequence FROM security_command_owners`) != 0 {
				t.Fatal("denied original wrote a prefix")
			}
			if tc.seed {
				if revision, _, _, _, _ := usRestrictionsSource(t, w, input.request.profile); revision != 1 {
					t.Fatal("denied original wrote source", revision)
				}
			}
		})
	}
}

// Replay returns the retained original after real source drift and constructs
// nothing from the live row.
func TestUSRestrictionsDispatchReplayAfterSourceDrift(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	inv := usRestrictionsInput(t, input)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	original, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	// Real current-source drift: a different document, revision and deleted row.
	wc008NativeExec(t, w, `UPDATE profile_restrictions SET rating_system='',maximum_age_rating='',maximum_age=-1,blocked_labels='[]',allow_dvr=1,revision=revision+1 WHERE profile_id=?`, usRestrictionsProfileID)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET deleted=1 WHERE id=?`, usRestrictionsProfileID)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	// No retained-result path may consult the live target statements.
	for _, id := range []stmt.ID{usRestrictionsScopeCheck, usRestrictionsTargetFacts, usRestrictionsCurrentRow, usRestrictionsUpdate, usRestrictionsInsert} {
		delete(turn.steps, id)
	}
	replay, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: inv.request})
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) || replay.Digest != original.Digest {
		t.Fatal("replay", replay, err)
	}
	// The drifted source and the retained cohort are untouched.
	if n := usRestrictionsQuery(t, w, `SELECT retained_count FROM security_command_owners`); n != 1 {
		t.Fatal("replay changed the retained count", n)
	}
	if n := usRestrictionsQuery(t, w, `SELECT count(*) FROM profile_restrictions WHERE profile_id=?`, usRestrictionsProfileID); n != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 ||
		usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_result_labels`) != 2 {
		t.Fatal("replay mutated source or cohort", n)
	}
}

// Receipt/label failure after a nonempty source prefix rolls the whole command
// back, including the inserted/replaced restriction row.
func TestUSRestrictionsDispatchFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*commandTurn)
	}{
		{"result_insert", func(turn *commandTurn) { delete(turn.steps, usRestrictionsResultInsert) }},
		{"label_insert", func(turn *commandTurn) { delete(turn.steps, usRestrictionsLabelInsert) }},
		{"owner_advance", func(turn *commandTurn) { delete(turn.steps, usRestrictionsOwnerAdvance) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
			turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
			tc.remove(turn)
			if out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, input)); err == nil || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
				t.Fatal("partial prefix escaped", out, err)
			}
			if revision, _, _, labels, _ := usRestrictionsSource(t, w, usRestrictionsProfileID); revision != 1 || labels != `["old"]` {
				t.Fatal("source effect survived rollback", revision, labels)
			}
			if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
				usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_results`) != 0 ||
				usRestrictionsQuery(t, w, `SELECT count(*) FROM security_restriction_result_labels`) != 0 ||
				usRestrictionsQuery(t, w, `SELECT last_sequence FROM security_command_owners`) != 0 ||
				usRestrictionsQuery(t, w, `SELECT retained_variable_bytes FROM security_command_owners`) != 0 {
				t.Fatal("receipt prefix survived rollback")
			}
		})
	}
}

// An uncertain COMMIT is never Applied and carries zero output.
func TestUSRestrictionsDispatchUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			w, path, r, proof, input := usRestrictionsFixture(t, true, nil)
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(w, fault)
			turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
			out, floor, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, input))
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("uncertain commit exposed success", out, floor, err)
			}
			if !w.ambiguous || !w.fenced {
				t.Fatal("uncertain writer was not fenced")
			}
			if fault.commits.Load() != 1 || native != after {
				t.Fatal("physical COMMIT disposition changed", fault.commits.Load(), native, after)
			}
			if _, err := w.begin(t.Context(), input.kind, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("fenced writer admitted another turn", err)
			}
		})
	}
}

// A canceled preparation denies before any read; a bare Resolve is never Applied.
func TestUSRestrictionsDispatchCanceledAndBareResolve(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	turn, a, cancel := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	cancel()
	if out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, input)); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
		t.Fatal("canceled preparation was accepted", out, err)
	}
	if revision, _, _, labels, _ := usRestrictionsSource(t, w, usRestrictionsProfileID); revision != 1 || labels != `["old"]` {
		t.Fatal("canceled preparation wrote source", revision, labels)
	}
	// A bare Resolve at last+1 (0+1) may report Pending but never Applied.
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	q, err := usRestrictionsReceiptForCommand(input)
	if err != nil {
		t.Fatal(err)
	}
	out, floor, err := runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: q})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State == Applied || out.Value.revision != 0 || out.Digest != (ResultDigest{}) || out.ChargedBytes != 0 || floor.AcknowledgedThrough != 0 {
		t.Fatal("bare Resolve was delivered as an applied original", out, floor)
	}
	if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("bare Resolve wrote a receipt")
	}
}

// Both retained bounds refuse before any source effect: 64 receipts and 1 MiB.
func TestUSRestrictionsDispatchQuotaCountAndBytes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		count   int64
		bytes   int64
		profile bool
	}{
		{"count", 64, 1, false},
		{"bytes", 1, usRestrictionsOwnerReceiptBytes - 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
			wc008NativeExec(t, w, `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=?,retained_variable_bytes=?`, tc.count, tc.bytes)
			identity := input.identity
			identity.Sequence = 2
			next, err := prepareUSRestrictions(USProfileRestrictions, identity, input.request)
			if err != nil {
				t.Fatal(err)
			}
			turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
			out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, next))
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
				t.Fatal("quota", out, err)
			}
			if revision, _, _, labels, _ := usRestrictionsSource(t, w, usRestrictionsProfileID); revision != 1 || labels != `["old"]` {
				t.Fatal("capacity refusal wrote an effect", revision, labels)
			}
			if usRestrictionsQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
				t.Fatal("capacity refusal wrote a receipt")
			}
		})
	}
}

// A retired owner is not resurrected: replay stays readable, ACK and fresh
// writes refuse.
func TestUSRestrictionsDispatchRetiredOwner(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	inv := usRestrictionsInput(t, input)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	original, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE security_command_owners SET retired_ms=1`)
	replay, _ := usRestrictionsReplay(t, w, r, proof, input.kind, &usRestrictionsInvocation{request: inv.request})
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal("retired owner replay", replay)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, &usRestrictionsInvocation{request: inv.request, ack: true}); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
		t.Fatal("retired owner ACK", out, err)
	}
	identity := input.identity
	identity.Sequence = 2
	next, err := prepareUSRestrictions(USProfileRestrictions, identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if out, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, usRestrictionsInput(t, next)); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usRestrictionsDispatchResult{}) {
		t.Fatal("retired owner accepted a fresh write", out, err)
	}
	if revision, _, _, _, _ := usRestrictionsSource(t, w, usRestrictionsProfileID); revision != 2 {
		t.Fatal("retired owner was mutated", revision)
	}
}

// The dispatch value and completion redact cleanly in logs and JSON.
func TestUSRestrictionsDispatchRedaction(t *testing.T) {
	value := usRestrictionsSavedResult{ratingSystem: "secret-system", labels: []string{"secret-label"}}
	for _, v := range []any{usRestrictionsDispatchResult{Value: value}, usRestrictionsCompletion{out: usRestrictionsDispatchResult{Value: value}}} {
		for _, rendered := range []string{fmtValue(v), fmtGoValue(v)} {
			if strings.Contains(rendered, "secret") {
				t.Fatal("dispatch value leaked", rendered)
			}
		}
	}
}

// The source/effect and receipt inventories stay closed, hot and captured.
func usRestrictionsAssertCapture(t *testing.T) {
	t.Helper()
	defs := append(usRestrictionsDefinitions(), usRestrictionsReceiptDefinitions()...)
	if len(defs) != 19 {
		t.Fatal("restrictions statement inventory changed", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("restrictions statement must stay hot, captured and waiver-free", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_restrictions_statements.go", "us_restrictions_snapshot.go", "us_restrictions_receipts.go", "us_restrictions_receipt_statements.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete restrictions inventory")
	}
}

func TestUSRestrictionsStatementCapture(t *testing.T) {
	usRestrictionsAssertCapture(t)
}

// One closed first instance: the captured statement inventory and the private
// composition both work against the canonical schema. This is a component
// proof only, never a product caller or a qualification claim.
func TestUSRestrictionsFirstInstanceClosed(t *testing.T) {
	usRestrictionsAssertCapture(t)
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	inv := usRestrictionsInput(t, input)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	original, _, err := runUSRestrictionsDispatch(t.Context(), turn, a, inv)
	if err != nil || original.Outcome.State != Applied || original.Value.revision != 2 {
		t.Fatal(original, err)
	}
	replay, _ := usRestrictionsReplay(t, w, r, proof, input.kind, &usRestrictionsInvocation{request: inv.request})
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal(replay)
	}
}
