package writer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// These tests reuse MG-07's ONE UO-root fake and its real embedded-schema
// fixture. The source used here is that fixture's live source; this task adds
// only its own source-command statements and request/result codecs.
type mgSourceCommandFixture struct {
	w      *executor
	path   string
	values mgPersonalFixtureValues
	r      *stmt.Registry
	proof  *mgActorProof
	root   *mgPersonalFakeRoots
}

func mgSourceCommandNewFixture(t *testing.T) *mgSourceCommandFixture {
	t.Helper()
	w, path, values := mgPersonalFixture(t)
	defs := mgAuthorityDefinitions()
	defs = append(defs, mgSourceDefinitions()...)
	defs = append(defs, mgPersonalRootDefinitions()...)
	defs = append(defs, mgSourceCommandDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	if n := mgSourceCommandCount(t, w, `SELECT count(*) FROM user_mutation_command_owners WHERE root_id=?`, values.root); n != 1 {
		t.Fatal("fixture root owner", n)
	}
	return &mgSourceCommandFixture{
		w: w, path: path, values: values, r: r,
		proof: &mgActorProof{native: *values.proof, kind: mgActorOwner},
		root:  mgPersonalFake(t, values),
	}
}

func (f *mgSourceCommandFixture) turn(t *testing.T, kind CommandID) (*commandTurn, mgAuthorityBudget) {
	t.Helper()
	recipe := testRecipe()
	recipe.limit = Work{10000, 1_000_000, 10000, 8 << 20, 25 * time.Millisecond}
	tx, err := f.w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range f.r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{8, 5000, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, f.r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn, mgAuthorityBudget{memory: 4 << 20, fixture: &mgAuthorityFixturePermit{frame: tx}}
}

type mgSourceCommandRun struct {
	turn  *commandTurn
	out   mgSourceCommandOutcome
	floor AckResult
	err   error
}

func (f *mgSourceCommandFixture) execute(t *testing.T, kind CommandID, reference ReceiptReference, req mgSourceCommandRequest, input *mgSourceCommand, ack bool) mgSourceCommandRun {
	t.Helper()
	turn, budget := f.turn(t, kind)
	inv := &mgSourceCommandInvocation{reference: reference, request: req, input: input, ack: ack}
	out, floor, err := runMGSourceCommand(t.Context(), turn, f.proof, t.Context(), f.root, budget, inv)
	return mgSourceCommandRun{turn: turn, out: out, floor: floor, err: err}
}

func mgSourceCommandIdentity(sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: mgPersonalIncarnation(), Sequence: sequence, OperationID: mgPersonalOperation(operation)}
}

func mgSourceCommandRequestFor(source string, kind CommandID, expected int64) mgSourceCommandRequest {
	q := mgSourceCommandRequest{Kind: kind, Source: source, ExpectedRevision: expected}
	switch kind {
	case MGConfigureTuner:
		q.Name, q.Physical, q.OwnerLimit, q.UsePhysical = "Configured", 4, 2, true
	case MGSetSourceEnabled:
		q.Enabled = false
	case MGSetSourceViewerAccess:
		q.ViewerAccess = "server-members"
	case MGRetireSource:
		q.Removal = "retire-request"
	}
	return q
}

func mgSourceCommandPrepare(t *testing.T, f *mgSourceCommandFixture, kind CommandID, sequence int64, operation byte, expected int64) (*mgSourceCommand, mgSourceCommandRequest) {
	t.Helper()
	req := mgSourceCommandRequestFor(f.values.live.source, kind, expected)
	prepared, err := newMGSourceCommand(kind, mgSourceCommandIdentity(sequence, operation), req)
	if err != nil {
		t.Fatal(err)
	}
	return prepared, req
}

func mgSourceCommandCommit(t *testing.T, run mgSourceCommandRun) {
	t.Helper()
	if run.err != nil {
		t.Fatal(run.err)
	}
	if err := run.turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := run.turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func mgSourceCommandFinish(t *testing.T, run mgSourceCommandRun) {
	t.Helper()
	if err := run.turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := run.turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func mgSourceCommandACK(t *testing.T, f *mgSourceCommandFixture, kind CommandID, reference ReceiptReference, req mgSourceCommandRequest) {
	t.Helper()
	ack := f.execute(t, kind, reference, req, nil, true)
	if ack.err != nil || ack.floor.AcknowledgedThrough != reference.Identity.Sequence || ack.out != (mgSourceCommandOutcome{}) {
		t.Fatal("ACK", ack.out, ack.floor, ack.err)
	}
	mgSourceCommandCommit(t, ack)
}

func mgSourceCommandCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// All four source commands share one accepting entry, save the complete
// original settings/revision/state/head and replay them with zero effects.
func TestMGSourceCommandAllFourKindsSavedAndReplayed(t *testing.T) {
	for _, kind := range []CommandID{MGConfigureTuner, MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess} {
		t.Run(fmt.Sprintf("%d", kind), func(t *testing.T) {
			f := mgSourceCommandNewFixture(t)
			prepared, req := mgSourceCommandPrepare(t, f, kind, 1, 1, 7)
			run := f.execute(t, kind, prepared.reference, req, prepared, false)
			if run.err != nil || run.out.Outcome.State != Pending || run.out.ChargedBytes <= 0 || run.out.OwnerCount != 1 {
				t.Fatal("fresh", run.out, run.err)
			}
			value := run.out.Value
			if value.kind != kind || value.source.id != f.values.live.source || value.source.revision != 8 || value.source.generation != "live-generation" ||
				value.source.published == "" || value.appliedMS <= 0 || run.out.Digest == (ResultDigest{}) {
				t.Fatalf("saved result: %+v", value)
			}
			switch kind {
			case MGConfigureTuner:
				if value.source.name != "Configured" || value.source.physical != 4 || value.source.ownerLimit != 2 || value.source.tuners != 2 || value.source.state != "active" {
					t.Fatal(value.source)
				}
			case MGSetSourceEnabled:
				if value.source.state != "disabled" || value.source.name != "Live" || value.source.tuners != 1 || value.source.viewerAccess != "owner-only" {
					t.Fatal("enable refresh lost configuration", value.source)
				}
			case MGSetSourceViewerAccess:
				if value.source.viewerAccess != "server-members" || value.source.state != "active" || value.source.name != "Live" {
					t.Fatal("access refresh lost configuration", value.source)
				}
			case MGRetireSource:
				if value.source.removed != 1 || value.source.state != "disabled" || value.source.removalRequest.String != "retire-request" ||
					value.source.removalRevision.Int64 != 8 || value.source.removedAt.String == "" {
					t.Fatal("retire result", value.source)
				}
			}
			mgSourceCommandCommit(t, run)
			if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, f.values.root); n != 1 {
				t.Fatal("receipt", n)
			}
			if n := mgSourceCommandCount(t, f.w, `SELECT receipt_rows FROM user_mutation_command_owners WHERE root_id=?`, f.values.root); n != 1 {
				t.Fatal("owner rows", n)
			}
			if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM live_generations WHERE source_id=?`, f.values.live.source); n != 2 {
				t.Fatal("source child custody was cascaded", n)
			}
			// Replay: same original identity, no input, no new effect.
			before := mgSourceCommandCount(t, f.w, `SELECT total_changes()`)
			replay := f.execute(t, kind, prepared.reference, req, nil, false)
			if replay.err != nil || replay.out.Outcome.State != Applied || !replay.out.Outcome.Replayed ||
				replay.out.Digest != run.out.Digest || !reflect.DeepEqual(replay.out.Value, value) || replay.out.ChargedBytes != 0 {
				t.Fatal("replay", replay.out, replay.err)
			}
			mgSourceCommandFinish(t, replay)
			if after := mgSourceCommandCount(t, f.w, `SELECT total_changes()`); after != before {
				t.Fatal("replay wrote", before, after)
			}
			// Floor-only ACK, then the same original is Retired with no value.
			mgSourceCommandACK(t, f, kind, prepared.reference, req)
			retired := f.execute(t, kind, prepared.reference, req, nil, false)
			if retired.err != nil || retired.out.Outcome.State != ResultRetired || retired.out.Value != (mgSourceCommandResult{}) || retired.out.Digest != (ResultDigest{}) {
				t.Fatal("ACKed original", retired.out, retired.err)
			}
			mgSourceCommandFinish(t, retired)
			if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?`, f.values.root); n != 1 {
				t.Fatal("ACK removed the receipt", n)
			}
			// Root custody is current-release authority: a changed root cannot
			// disclose the retained original.
			wc008NativeExec(t, f.w, `UPDATE user_mutation_roots SET profile_id='other' WHERE id=?`, f.values.root)
			denied := f.execute(t, kind, prepared.reference, req, nil, false)
			if !errors.Is(denied.err, ErrUORootGone) || denied.out != (mgSourceCommandOutcome{}) {
				t.Fatal("changed root disclosed", denied.out, denied.err)
			}
			mgSourceCommandFinish(t, denied)
		})
	}
}

// A replay is independent of the live head/name; only the saved original is
// returned, still with zero effects.
func TestMGSourceCommandReplayAfterHeadChange(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	prepared, req := mgSourceCommandPrepare(t, f, MGConfigureTuner, 1, 2, 7)
	run := f.execute(t, MGConfigureTuner, prepared.reference, req, prepared, false)
	mgSourceCommandCommit(t, run)
	wc008NativeExec(t, f.w, `UPDATE live_sources SET active_generation='replacement-generation',name='Changed',revision=9 WHERE id=?`, f.values.live.source)
	replay := f.execute(t, MGConfigureTuner, prepared.reference, req, nil, false)
	if replay.err != nil || replay.out.Outcome.State != Applied || replay.out.Value.source.generation != "live-generation" ||
		replay.out.Value.source.name != "Configured" || replay.out.Value.source.revision != 8 {
		t.Fatal("replay followed the live head", replay.out, replay.err)
	}
	mgSourceCommandFinish(t, replay)
}

func TestMGSourceCommandRejectsForgedAuthority(t *testing.T) {
	for name, forge := range map[string]func(*mgActorProof) *mgActorProof{
		"nil": func(*mgActorProof) *mgActorProof { return nil },
		"forged_family": func(p *mgActorProof) *mgActorProof {
			fake := *p
			fake.native.witness.issuer.family = "forged"
			return &fake
		},
		"forged_token": func(p *mgActorProof) *mgActorProof {
			fake := *p
			fake.native.witness.issuer.tokenDigest = usCredentialFixtureHash("forged-token")
			return &fake
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := mgSourceCommandNewFixture(t)
			prepared, req := mgSourceCommandPrepare(t, f, MGSetSourceViewerAccess, 1, 3, 7)
			turn, budget := f.turn(t, MGSetSourceViewerAccess)
			inv := &mgSourceCommandInvocation{reference: prepared.reference, request: req, input: prepared}
			out, _, err := runMGSourceCommand(t.Context(), turn, forge(f.proof), t.Context(), f.root, budget, inv)
			if !errors.Is(err, ErrDisclosureDenied) || out != (mgSourceCommandOutcome{}) {
				t.Fatal("forged authority", out, err)
			}
			mgSourceCommandFinish(t, mgSourceCommandRun{turn: turn})
			if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal("forged authority minted a receipt", n)
			}
			if n := mgSourceCommandCount(t, f.w, `SELECT settings_viewer_access='server-members' FROM live_source_identities WHERE id=?`, f.values.live.source); n != 0 {
				t.Fatal("forged authority wrote source", n)
			}
		})
	}
}

func TestMGSourceCommandRejectsStaleAuthority(t *testing.T) {
	for name, mutation := range map[string]string{
		"revoked_family":  `UPDATE authorization_session_families SET revoked=1`,
		"retired_token":   `UPDATE authorization_family_tokens SET retired=1`,
		"disabled_member": `UPDATE direct_memberships SET disabled=1`,
		"role_change":     `UPDATE direct_memberships SET role='member'`,
		"member_lifetime": `UPDATE direct_memberships SET incarnation='cccccccccccccccccccccccccccccccc'`,
	} {
		t.Run(name, func(t *testing.T) {
			f := mgSourceCommandNewFixture(t)
			prepared, req := mgSourceCommandPrepare(t, f, MGSetSourceEnabled, 1, 4, 7)
			wc008NativeExec(t, f.w, mutation)
			run := f.execute(t, MGSetSourceEnabled, prepared.reference, req, prepared, false)
			if !errors.Is(run.err, ErrDisclosureDenied) || run.out != (mgSourceCommandOutcome{}) {
				t.Fatal("stale authority", run.out, run.err)
			}
			mgSourceCommandFinish(t, run)
			if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
				t.Fatal("stale authority minted a receipt", n)
			}
		})
	}
}

func TestMGSourceCommandRejectsCancelledLifetime(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	prepared, req := mgSourceCommandPrepare(t, f, MGSetSourceEnabled, 1, 5, 7)
	turn, budget := f.turn(t, MGSetSourceEnabled)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	inv := &mgSourceCommandInvocation{reference: prepared.reference, request: req, input: prepared}
	out, _, err := runMGSourceCommand(cancelled, turn, f.proof, cancelled, f.root, budget, inv)
	if err == nil || out != (mgSourceCommandOutcome{}) {
		t.Fatal("cancelled lifetime", out, err)
	}
	mgSourceCommandFinish(t, mgSourceCommandRun{turn: turn})
	if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
		t.Fatal("cancelled lifetime minted a receipt", n)
	}
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestMGSourceCommandUnknownCommitIsNotSuccess(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	prepared, req := mgSourceCommandPrepare(t, f, MGSetSourceEnabled, 1, 6, 7)
	if err := f.w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(f.path, f.w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(f.w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	run := f.execute(t, MGSetSourceEnabled, prepared.reference, req, prepared, false)
	if run.err != nil || run.out.Outcome.State != Pending {
		t.Fatal("fresh before commit", run.out, run.err)
	}
	result, err := run.turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal("unknown commit reported success", result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), f.path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM live_sources WHERE id=? AND state='disabled' AND revision=8 AND name='Live'`, []any{f.values.live.source}, 1},
		{`SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=? AND command_kind=809`, []any{f.values.root}, 1},
		{`SELECT count(*) FROM user_mutation_command_owners WHERE root_id=? AND last_command_sequence=1 AND acknowledged_command_sequence=0 AND receipt_rows=1 AND receipt_variable_bytes>0`, []any{f.values.root}, 1},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query, check.args...).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	// An unknown commit fences the executor; no new original runs past it.
	if _, err = f.w.begin(t.Context(), MGSetSourceEnabled, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("executor advanced past an unknown commit", err)
	}
}

func TestMGSourceCommandChangedBodyOrDigestConflicts(t *testing.T) {
	t.Run("changed_body", func(t *testing.T) {
		f := mgSourceCommandNewFixture(t)
		prepared, req := mgSourceCommandPrepare(t, f, MGConfigureTuner, 1, 7, 7)
		forged := *prepared
		forged.request.Name = "Forged"
		run := f.execute(t, MGConfigureTuner, prepared.reference, req, &forged, false)
		if !errors.Is(run.err, ErrConflict) || run.out != (mgSourceCommandOutcome{}) {
			t.Fatal("changed body", run.out, run.err)
		}
		mgSourceCommandFinish(t, run)
	})
	t.Run("changed_digest", func(t *testing.T) {
		f := mgSourceCommandNewFixture(t)
		prepared, req := mgSourceCommandPrepare(t, f, MGConfigureTuner, 1, 8, 7)
		reference := prepared.reference
		reference.RequestDigest[0]++
		run := f.execute(t, MGConfigureTuner, reference, req, prepared, false)
		if !errors.Is(run.err, ErrConflict) || run.out != (mgSourceCommandOutcome{}) {
			t.Fatal("changed digest", run.out, run.err)
		}
		mgSourceCommandFinish(t, run)
	})
}

func TestMGSourceCommandStaleExpectedRevisionConflicts(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	prepared, req := mgSourceCommandPrepare(t, f, MGSetSourceViewerAccess, 1, 9, 6)
	run := f.execute(t, MGSetSourceViewerAccess, prepared.reference, req, prepared, false)
	if !errors.Is(run.err, ErrConflict) || run.out != (mgSourceCommandOutcome{}) {
		t.Fatal("stale revision", run.out, run.err)
	}
	mgSourceCommandFinish(t, run)
	if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
		t.Fatal("stale revision minted a receipt", n)
	}
	if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM live_source_identities WHERE settings_viewer_access='server-members'`); n != 0 {
		t.Fatal("stale revision wrote source", n)
	}
}

func TestMGSourceCommandRepeatDisableKeepsCustodyAndRepeatRetireRefused(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	first, req := mgSourceCommandPrepare(t, f, MGSetSourceEnabled, 1, 10, 7)
	if run := f.execute(t, MGSetSourceEnabled, first.reference, req, first, false); run.err != nil {
		t.Fatal(run.err)
	} else {
		mgSourceCommandCommit(t, run)
	}
	mgSourceCommandACK(t, f, MGSetSourceEnabled, first.reference, req)
	second, req2 := mgSourceCommandPrepare(t, f, MGSetSourceEnabled, 2, 11, 8)
	run := f.execute(t, MGSetSourceEnabled, second.reference, req2, second, false)
	if run.err != nil || run.out.Value.source.revision != 9 || run.out.Value.source.state != "disabled" {
		t.Fatal("repeat disable", run.out, run.err)
	}
	mgSourceCommandCommit(t, run)
	mgSourceCommandACK(t, f, MGSetSourceEnabled, second.reference, req2)
	if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM live_generations WHERE source_id=?`, f.values.live.source); n != 2 {
		t.Fatal("repeat disable cascaded source children", n)
	}
	retire, retireReq := mgSourceCommandPrepare(t, f, MGRetireSource, 3, 12, 9)
	if run := f.execute(t, MGRetireSource, retire.reference, retireReq, retire, false); run.err != nil {
		t.Fatal(run.err)
	} else {
		mgSourceCommandCommit(t, run)
	}
	mgSourceCommandACK(t, f, MGRetireSource, retire.reference, retireReq)
	again, againReq := mgSourceCommandPrepare(t, f, MGRetireSource, 4, 13, 10)
	refused := f.execute(t, MGRetireSource, again.reference, againReq, again, false)
	if !errors.Is(refused.err, ErrConflict) || refused.out != (mgSourceCommandOutcome{}) {
		t.Fatal("retired source was retired again", refused.out, refused.err)
	}
	mgSourceCommandFinish(t, refused)
	if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM live_sources WHERE id=? AND revision=10`, f.values.live.source); n != 1 {
		t.Fatal("repeat retire wrote the source", n)
	}
}

func TestMGSourceCommandOwnerQuotaRefusesBeforeEffect(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	wc008NativeExec(t, f.w, `UPDATE user_mutation_command_owners SET receipt_rows=64,receipt_variable_bytes=1048576 WHERE root_id=?`, f.values.root)
	prepared, req := mgSourceCommandPrepare(t, f, MGConfigureTuner, 1, 14, 7)
	run := f.execute(t, MGConfigureTuner, prepared.reference, req, prepared, false)
	var refusal *Refusal
	if !errors.As(run.err, &refusal) || refusal.Code != RefusalCapacity || run.out != (mgSourceCommandOutcome{}) {
		t.Fatal("quota", run.out, run.err)
	}
	mgSourceCommandFinish(t, run)
	if n := mgSourceCommandCount(t, f.w, `SELECT revision FROM live_sources WHERE id=?`, f.values.live.source); n != 7 {
		t.Fatal("quota refusal wrote source", n)
	}
}

func TestMGSourceCommandProductionRootRefuses(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	prepared, req := mgSourceCommandPrepare(t, f, MGConfigureTuner, 1, 15, 7)
	turn, budget := f.turn(t, MGConfigureTuner)
	inv := &mgSourceCommandInvocation{reference: prepared.reference, request: req, input: prepared}
	out, _, err := runMGSourceCommand(t.Context(), turn, f.proof, t.Context(), mgPersonalProductionRoots{}, budget, inv)
	if !errors.Is(err, ErrMGRootUnavailable) || out != (mgSourceCommandOutcome{}) {
		t.Fatal("production root", out, err)
	}
	mgSourceCommandFinish(t, mgSourceCommandRun{turn: turn})
	if n := mgSourceCommandCount(t, f.w, `SELECT count(*) FROM user_mutation_command_receipts`); n != 0 {
		t.Fatal("production refusal minted a receipt", n)
	}
	if n := mgSourceCommandCount(t, f.w, `SELECT revision FROM live_sources WHERE id=?`, f.values.live.source); n != 7 {
		t.Fatal("production refusal wrote source", n)
	}
}

func TestMGSourceCommandResultCodecRoundTrip(t *testing.T) {
	f := mgSourceCommandNewFixture(t)
	prepared, req := mgSourceCommandPrepare(t, f, MGRetireSource, 1, 16, 7)
	run := f.execute(t, MGRetireSource, prepared.reference, req, prepared, false)
	if run.err != nil {
		t.Fatal(run.err)
	}
	payload, digest, err := mgSourceCommandPayload(prepared.reference.Identity, prepared.reference.RequestDigest, run.out.Value)
	if err != nil || len(payload) == 0 {
		t.Fatal(err)
	}
	decoded, err := decodeMGSourceCommandResult(prepared.reference.Identity, prepared.reference.RequestDigest, payload, digest)
	if err != nil || !reflect.DeepEqual(decoded, run.out.Value) {
		t.Fatalf("codec round trip: %+v %v", decoded, err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 1
	if _, err = decodeMGSourceCommandResult(prepared.reference.Identity, prepared.reference.RequestDigest, tampered, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered payload decoded", err)
	}
	if _, _, err = mgSourceCommandPayload(prepared.reference.Identity, prepared.reference.RequestDigest, mgSourceCommandResult{}); err == nil {
		t.Fatal("invalid result encoded")
	}
	mgSourceCommandFinish(t, run)
}
