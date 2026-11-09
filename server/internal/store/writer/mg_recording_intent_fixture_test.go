package writer

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The fixture uses the REAL embedded A/B/G+013 schema through mgPersonalFixture
// (no draft overlay, no dropped table) and adds one current live source head
// with two channels/programmes plus a replacement generation. Programme and
// channel identities are the existing 64-hex public ids; source and generation
// ids are the existing 48-hex opaque ids.
type mgRecordingFixtureValues struct {
	owner          mgRecordingOwner
	scope          mgRecordingScope
	other          mgRecordingScope
	third          mgRecordingScope
	programme      mgRecordingProgramme
	otherProgramme mgRecordingProgramme
	thirdProgramme mgRecordingProgramme
	replacement    string
	root           string
	proof          *usOwnerAuthorityProof
	now            time.Time
}

func mgRecordingHexValue(c byte, n int) string { return strings.Repeat(string(c), n) }

func (v mgRecordingFixtureValues) options() mgRecordingOptions {
	return mgRecordingOptions{BeforeMS: 60000, AfterMS: 300000, Priority: 5, RetentionDays: 7, EpisodeLimit: 2}
}

func (v mgRecordingFixtureValues) schedule() mgRecordingScheduleRequest {
	return mgRecordingScheduleRequest{owner: v.owner, scope: v.scope, programme: v.programme, options: v.options()}
}

func (v mgRecordingFixtureValues) update(id string, expected int64, o mgRecordingOptions) mgRecordingUpdateRequest {
	return mgRecordingUpdateRequest{owner: v.owner, id: id, expected: expected, scope: v.scope, programme: v.programme, options: o}
}

func (v mgRecordingFixtureValues) cancel(id string, expected int64) mgRecordingCancelRequest {
	return mgRecordingCancelRequest{owner: v.owner, id: id, expected: expected}
}

func (v mgRecordingFixtureValues) keep(id string, expected int64, keep bool) mgRecordingKeepRequest {
	return mgRecordingKeepRequest{owner: v.owner, id: id, expected: expected, keep: keep}
}

func mgRecordingFixture(t *testing.T) (*executor, string, mgRecordingFixtureValues) {
	t.Helper()
	w, path, p := mgPersonalFixture(t)
	now := w.now()
	i := p.proof.witness.issuer
	source, generation, replacement := mgRecordingHexValue('1', 48), mgRecordingHexValue('2', 48), mgRecordingHexValue('3', 48)
	channel, otherChannel := mgRecordingHexValue('4', 64), mgRecordingHexValue('7', 64)
	thirdChannel := mgRecordingHexValue('8', 64)
	programme, otherProgramme := mgRecordingHexValue('5', 64), mgRecordingHexValue('6', 64)
	thirdProgramme := mgRecordingHexValue('9', 64)
	start := now.Add(24 * time.Hour).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	startText, endText := start.Format(time.RFC3339), end.Format(time.RFC3339)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO live_source_identities(id) VALUES(?)`, []any{source}},
		{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES(?,?,'Guide',1,2,2,'2026-11-07T00:00:00Z'),(?,?,'Next guide',1,0,0,'2026-11-07T00:00:00Z')`, []any{generation, source, replacement, source}},
		{`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES(?,'Live',7,'active',1,?,'2026-11-07T00:00:00Z')`, []any{source, generation}},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'provider-1','Channel','1','Group',0,X'01','',''),(?,?,'provider-2','Other','2','Group',1,X'01','',''),(?,?,'provider-3','Third','3','Group',2,X'01','','')`, []any{generation, channel, generation, otherChannel, generation, thirdChannel}},
		{`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES(?,?,?,'p1','Programme',?,?,'lineage'),(?,?,?,'p2','Other',?,?,'other'),(?,?,?,'p3','Third',?,?,'third')`, []any{generation, programme, channel, startText, endText, generation, otherProgramme, otherChannel, startText, endText, generation, thirdProgramme, thirdChannel, startText, endText}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	v := mgRecordingFixtureValues{
		owner: mgRecordingOwner{
			account: i.account, profile: i.profile, memberIncarnation: i.memberIncarnation,
			profileLifetime: i.profileLifetime, accountEpoch: 1, memberRevision: 1, profileRevision: 1,
		},
		scope:          mgRecordingScope{source: source, generation: generation, head: generation, channel: channel, programme: programme, revision: 7},
		other:          mgRecordingScope{source: source, generation: generation, head: generation, channel: otherChannel, programme: otherProgramme, revision: 7},
		third:          mgRecordingScope{source: source, generation: generation, head: generation, channel: thirdChannel, programme: thirdProgramme, revision: 7},
		programme:      mgRecordingProgramme{id: programme, channel: channel, provider: "p1", title: "Programme", start: startText, end: endText, lineage: "lineage", facts: mgRecordingNullableText{text: "{}", valid: true}},
		otherProgramme: mgRecordingProgramme{id: otherProgramme, channel: otherChannel, provider: "p2", title: "Other", start: startText, end: endText, lineage: "other", facts: mgRecordingNullableText{text: "{}", valid: true}},
		thirdProgramme: mgRecordingProgramme{id: thirdProgramme, channel: thirdChannel, provider: "p3", title: "Third", start: startText, end: endText, lineage: "third", facts: mgRecordingNullableText{text: "{}", valid: true}},
		replacement:    replacement,
		root:           p.root,
		proof:          p.proof,
		now:            now,
	}
	return w, path, v
}

// mgRecordingExpectedResult is the exact saved scalar the frame must return for
// one intent state; it is built from the same freeze the effect uses, never
// from a live row read.
func mgRecordingExpectedResult(t *testing.T, v mgRecordingFixtureValues, scope mgRecordingScope, p mgRecordingProgramme, o mgRecordingOptions, state string, revision, keep, manual, ownerRevision int64) mgRecordingIntentResult {
	t.Helper()
	frozen, err := freezeMGRecordingIntent(p, o)
	if err != nil {
		t.Fatal(err)
	}
	return mgRecordingIntentResult{
		id: mgRecordingID(v.owner, scope.source, scope.programme), owner: v.owner.key(),
		source: scope.source, channel: scope.channel, programme: scope.programme, generation: scope.generation,
		state: state, optionsJSON: frozen.optionsJSON, startMS: frozen.startMS, endMS: frozen.endMS,
		priority: o.Priority, keep: keep, manual: manual, revision: revision, ownerRevision: ownerRevision,
	}
}

// The owner point is MG-11's shared owner-facts statement; this task does not
// redefine it. Every turn composes the root custody, the owner point and this
// task's own recording-intent statements.
func mgRecordingDefinitionsUnion() []stmt.Definition {
	defs := mgPersonalRootDefinitions()
	defs = append(defs, mgRecordingIntentDefinitions()...)
	return append(defs, mgOwnerDefinition())
}

func mgRecordingTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgRecordingDefinitionsUnion()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{
		limit:    Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond},
		setup:    Work{8, 100, 8, 4096, time.Millisecond},
		terminal: Work{8, 100, 8, 4096, time.Millisecond},
	}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}

func mgRecordingFake(v mgRecordingFixtureValues) *mgPersonalFakeRoots {
	return &mgPersonalFakeRoots{root: v.root, identity: mgPersonalFakeIdentity(v.proof), witness: v.proof}
}

func mgRecordingScheduleReference(t *testing.T, id CommandIdentity, r mgRecordingScheduleRequest) ReceiptReference {
	t.Helper()
	c, err := newMGRecordingScheduleCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgRecordingUpdateReference(t *testing.T, id CommandIdentity, r mgRecordingUpdateRequest) ReceiptReference {
	t.Helper()
	c, err := newMGRecordingUpdateCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgRecordingCancelReference(t *testing.T, id CommandIdentity, r mgRecordingCancelRequest) ReceiptReference {
	t.Helper()
	c, err := newMGRecordingCancelCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgRecordingKeepReference(t *testing.T, id CommandIdentity, r mgRecordingKeepRequest) ReceiptReference {
	t.Helper()
	c, err := newMGRecordingKeepCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

// scheduleMGRecording is the fixture's one accepted fresh schedule; tests that
// need an existing occurrence start here rather than writing rows by hand.
func scheduleMGRecording(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots, identity CommandIdentity) mgRecordingAcceptance {
	t.Helper()
	return scheduleMGRecordingScope(t, w, v, fake, identity, v.scope, v.programme)
}

// scheduleMGRecordingScope is the same accepted fresh schedule for the
// fixture's secondary channels/programmes.
func scheduleMGRecordingScope(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots, identity CommandIdentity, scope mgRecordingScope, programme mgRecordingProgramme) mgRecordingAcceptance {
	t.Helper()
	request := v.schedule()
	request.scope, request.programme = scope, programme
	reference := mgRecordingScheduleReference(t, identity, request)
	turn := mgRecordingTurn(t, w, MGScheduleRecording)
	out, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	mgPersonalCommit(t, turn)
	return out
}

// disableMGRecordingReservation reproduces the legacy reconcile/worker state a
// source disable, guide replacement or tuner conflict leaves behind: the intent
// moves to a waiting/conflicted state and its reservation is only disabled, not
// deleted. It is a native observation, never authority.
func disableMGRecordingReservation(t *testing.T, w *executor, id, state, reason string) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_recordings SET state=?,reason=? WHERE id=?`, state, reason, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_reservations SET enabled=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
}

func ackMGRecording(t *testing.T, w *executor, root string, sequence int64) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=? WHERE root_id=? AND last_command_sequence=?`, sequence, root, sequence); err != nil {
		t.Fatal(err)
	}
}

func mgRecordingCounts(t *testing.T, w *executor, root string) (recordings, reservations, dependencies, receipts, sequence int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM dvr_recordings),(SELECT count(*) FROM live_reservations),(SELECT count(*) FROM live_source_dependencies),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root).Scan(&recordings, &reservations, &dependencies, &receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	return
}
