package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// MP-08 (703-705) fixtures reuse MP-07's canonical adopted F schema and its
// trusted start-preparation frame. Accept/retire/timeout are the only commands
// under test here; helper work never starts from this frame.

func mpStartAcceptCaptureFixture(t *testing.T, c *sql.Conn, cfg sqliteconn.Config, defs []stmt.Definition, sources ...string) plan.Golden {
	t.Helper()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range sources {
		b, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		hash.Write([]byte(name))
		hash.Write(b)
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed,
		Implementations: "MP08 start preparation accept/retire/timeout on canonical adopted F (484242fb) through sqliteconn/bootstrap, FK1/DQS0/0; complete adopted policy; synthetic resource forecasts; not performance qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if golden.Receipt() == nil || len(golden.Manifest) != len(defs) {
		t.Fatal("incomplete canonical F Capture")
	}
	return golden
}

func mpStartAcceptCompose(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	all := []stmt.Definition{}
	for _, entry := range f.w.effective.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	defs := mpStartAcceptDefinitions()
	for _, d := range defs {
		found := false
		for _, old := range all {
			if old.ID == d.ID {
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	conn, err := f.e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	capturePolicy := f.e.config
	capturePolicy.Synchronous = sqliteconn.Normal
	golden := mpStartAcceptCaptureFixture(t, conn, capturePolicy, all,
		"mp_start_accept_test.go", "mp_start_accept.go", "mp_start_accept_receipts.go", "mp_start_accept_statements.go", "mp_expiry.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	p := effectiveFixturePlan()
	p.registration = golden.Receipt()
	p.steps = nil
	p.mpWorkerMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range defs {
		p.steps = append(p.steps, statementStep{d.ID, Work{32, 8000, 32, 65536, 100 * time.Microsecond}})
	}
	if err = composeMPStartAccept(t.Context(), f.w.effective, p); err != nil {
		t.Fatal(err)
	}
}

func mpStartAcceptFixtureFor(t *testing.T) mpStartPrepFixture {
	t.Helper()
	f := mpStartPrepFixtureFor(t)
	mpStartPrepComposeFixture(t, f)
	mpStartAcceptCompose(t, f)
	return f
}

func (f mpStartPrepFixture) acceptFence(execution string, revision int64) mpStartAcceptFence {
	return mpStartAcceptFence{id: "mp-prep", execution: execution, revision: revision, actor: f.actor,
		source: f.source, queue: f.queue, sourceWitnessed: true}
}

func (f mpStartPrepFixture) terminal(t *testing.T, kind CommandID, input any, sequence int64, op byte) (mpStartAcceptResult, error) {
	t.Helper()
	return f.terminalContext(t, t.Context(), kind, input, sequence, op)
}

func (f mpStartPrepFixture) terminalContext(t *testing.T, ctx context.Context, kind CommandID, input any, sequence int64, op byte) (mpStartAcceptResult, error) {
	t.Helper()
	id := CommandIdentity{Incarnation: f.inc, Sequence: sequence, OperationID: [16]byte{op}}
	command, err := newMPStartAcceptCommand(kind, id, input)
	if err != nil {
		t.Fatal(err)
	}
	return f.w.runMPStartAccept(ctx, f.cap, kind, command.reference, input)
}

func (f mpStartPrepFixture) accept(t *testing.T, revision int64, execution string, sequence int64, op byte) (mpStartAcceptResult, error) {
	t.Helper()
	return f.terminal(t, MPStartPrepAccept, mpStartAcceptInput{fence: f.acceptFence(execution, revision)}, sequence, op)
}

func (f mpStartPrepFixture) retire(t *testing.T, revision int64, execution string, sequence int64, op byte) (mpStartAcceptResult, error) {
	t.Helper()
	return f.terminal(t, MPStartPrepRetire, mpStartRetireInput{fence: f.acceptFence(execution, revision)}, sequence, op)
}

func (f mpStartPrepFixture) timeout(t *testing.T, revision int64, execution string, sequence int64, op byte) (mpStartAcceptResult, error) {
	t.Helper()
	return f.terminal(t, MPStartPrepTimeout, mpStartTimeoutInput{fence: f.acceptFence(execution, revision)}, sequence, op)
}

// mpStartAcceptSealed stages the MP-07 birth/claim/seal chain and leaves the
// preparation prepared at revision 3 with the 702 receipt acknowledged.
func mpStartAcceptSealed(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	if _, err := f.begin(t); err != nil {
		t.Fatal(err)
	}
	if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
		t.Fatal(err)
	}
	f.ack(t, 1)
	if _, err := f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65); err != nil {
		t.Fatal(err)
	}
	f.ack(t, 2)
}

// mpStartExpire moves the stored deadline into the past without touching any
// physical row. The DB CHECK keeps deadline_ms>created_ms and <=token_expires_ms.
func mpStartExpire(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	result, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparations SET created_ms=created_ms-60000,deadline_ms=created_ms-30000 WHERE id='mp-prep'`)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("deadline did not move", n, err)
	}
}

type mpStartAcceptFacts struct {
	state, custody, resultDigest string
	revision, sealed, receipts   int64
	last, ack, rows, bytes       int64
	physicalExited, cleaned      int64
	stagedRows, stagedBytes      int64
	executionRows, preparations  int64
	sessions, mediaInputs        int64
}

func mpStartAcceptReadFacts(t *testing.T, f mpStartPrepFixture) mpStartAcceptFacts {
	t.Helper()
	var o mpStartAcceptFacts
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT state,revision,sealed FROM playback_start_preparation_lifecycle WHERE operation_id='mp-prep'`).Scan(&o.state, &o.revision, &o.sealed); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes FROM playback_start_preparations WHERE id='mp-prep'`).Scan(&o.last, &o.ack, &o.rows, &o.bytes); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&o.receipts); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*),coalesce(max(custody_media_session_id),''),coalesce(max(physical_exited),0),coalesce(max(cleaned),0),coalesce(max(result_digest),''),coalesce(max(staged_rows),0),coalesce(max(staged_bytes),0) FROM mp_start_execution WHERE operation_id='mp-prep'`).Scan(&o.executionRows, &o.custody, &o.physicalExited, &o.cleaned, &o.resultDigest, &o.stagedRows, &o.stagedBytes); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_start_preparations`).Scan(&o.preparations); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM playback_sessions WHERE id='mp-session'`).Scan(&o.sessions); err != nil {
		t.Fatal(err)
	}
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_media_inputs WHERE media_session_id='mp-session'`).Scan(&o.mediaInputs); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestMPStartAcceptTransfersCustodyOnce(t *testing.T) {
	f := mpStartAcceptFixtureFor(t)
	mpStartAcceptSealed(t, f)
	before := mpStartAcceptReadFacts(t, f)
	if before.state != "prepared" || before.revision != 3 || before.sealed != 1 || before.receipts != 2 || before.custody != "" || before.executionRows != 1 {
		t.Fatalf("unexpected sealed preparation %+v", before)
	}
	accept, err := f.accept(t, 3, "mp-execution", 3, 0x70)
	if err != nil || accept.Outcome.State != Applied || accept.Outcome.Replayed {
		t.Fatal(accept, err)
	}
	want := mpStartAcceptValue{Preparation: "mp-prep", State: "accepted", Execution: "mp-execution", Custody: "mp-session",
		Session: "mp-session", LegacySession: "mp-legacy", LegacyGeneration: 9, Snapshot: "mp-snapshot",
		Revision: 4, DeadlineMS: f.deadline, AppliedMS: accept.Value.AppliedMS}
	if accept.Value != want || accept.Digest == (ResultDigest{}) {
		t.Fatalf("accept result %+v want %+v", accept.Value, want)
	}
	after := mpStartAcceptReadFacts(t, f)
	if after.state != "accepted" || after.revision != 4 || after.custody != "mp-session" ||
		after.receipts != 3 || after.last != 3 || after.ack != 2 || after.rows != 3 || after.bytes <= 0 {
		t.Fatalf("accept effects %+v", after)
	}
	// Custody was transferred once and no physical session/input/public birth or
	// exit/cleanup was minted by acceptance.
	if after.physicalExited != 0 || after.cleaned != 0 || after.executionRows != 1 ||
		after.stagedRows != 4 || after.stagedBytes != 128 || after.resultDigest != strings.Repeat("f", 64) ||
		after.sessions != 0 || after.mediaInputs != 0 || after.preparations != 1 {
		t.Fatalf("accept freed or duplicated physical ownership %+v", after)
	}
	// The saved original survives a fresh turn with identical bytes.
	saved, err := f.accept(t, 3, "mp-execution", 3, 0x70)
	if err != nil || saved.Outcome.State != Applied || !saved.Outcome.Replayed ||
		saved.Value != accept.Value || saved.Digest != accept.Digest {
		t.Fatal("accept replay", saved, err)
	}
	if again := mpStartAcceptReadFacts(t, f); again != after {
		t.Fatalf("replay changed native state: before %+v after %+v", after, again)
	}
	// A different execution on the same sequence is a conflict, never a takeover.
	if out, e := f.accept(t, 3, "other-execution", 3, 0x70); e == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
		t.Fatal("conflicting duplicate accepted", out, e)
	}
	// A stale revision on the next sequence is a conflict too.
	f.ack(t, 3)
	if out, e := f.accept(t, 3, "mp-execution", 4, 0x72); e == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
		t.Fatal("stale-revision accept", out, e)
	}
	if last := mpStartAcceptReadFacts(t, f); last.custody != "mp-session" || last.revision != 4 || last.receipts != 3 {
		t.Fatalf("refusals changed custody %+v", last)
	}
	mpStartPrepAssertReturned(t, f)
}

func TestMPStartAcceptReplayAfterAckRetired(t *testing.T) {
	f := mpStartAcceptFixtureFor(t)
	mpStartAcceptSealed(t, f)
	accept, err := f.accept(t, 3, "mp-execution", 3, 0x70)
	if err != nil {
		t.Fatal(err)
	}
	// Head drift must not change the original bytes while it is unacknowledged.
	if _, err = f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_owner_revision=mp_owner_revision+9`); err != nil {
		t.Fatal(err)
	}
	replay, err := f.accept(t, 3, "mp-execution", 3, 0x70)
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed ||
		replay.Value != accept.Value || replay.Digest != accept.Digest {
		t.Fatal("head-drift replay", replay, err)
	}
	f.ack(t, 3)
	retired, err := f.accept(t, 3, "mp-execution", 3, 0x70)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (mpStartAcceptValue{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("ACKed accept served as Applied", retired, err)
	}
	if accept.Value.State != "accepted" || accept.Value.Custody != "mp-session" {
		t.Fatal("originals changed")
	}
	mpStartPrepAssertReturned(t, f)
}

// mpStartAcceptAckBump simulates the future MP-29 acknowledgement (command 780)
// landing on the main path: it advances the acknowledged floor with the same raw
// SQL f.ack uses AND bumps the receipt-debt counter mp_owner_revision. The
// lifecycle revision is untouched, so the two counters drift apart exactly as
// they will once ACK can run before every terminal command.
func mpStartAcceptAckBump(t *testing.T, f mpStartPrepFixture, sequence int64) {
	t.Helper()
	result, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparations SET mp_command_acknowledged_sequence=?,mp_owner_revision=mp_owner_revision+1 WHERE id='mp-prep' AND mp_command_last_sequence>=? AND mp_command_acknowledged_sequence<=?`, sequence, sequence, sequence)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("ACK-style bump did not land", n, err)
	}
}

func mpStartAcceptCounters(t *testing.T, f mpStartPrepFixture) (lifecycle, owner int64) {
	t.Helper()
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT l.revision,p.mp_owner_revision FROM playback_start_preparations AS p INNER JOIN playback_start_preparation_lifecycle AS l ON l.operation_id=p.id WHERE p.id='mp-prep'`).Scan(&lifecycle, &owner); err != nil {
		t.Fatal(err)
	}
	return lifecycle, owner
}

// TestMPStartAcceptFencesLifecycleRevision pins the 703/704/705 fence to the
// lifecycle revision, never the receipt-debt counter. Once ACK (780) bumps
// mp_owner_revision on the main path, comparing the fence against it would make
// every terminal command a permanent wrong ErrConflict.
func TestMPStartAcceptFencesLifecycleRevision(t *testing.T) {
	t.Run("accept", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		mpStartAcceptSealed(t, f)
		mpStartAcceptAckBump(t, f, 2)
		if lifecycle, owner := mpStartAcceptCounters(t, f); lifecycle != 3 || owner != lifecycle+1 {
			t.Fatalf("fixture counters did not drift: lifecycle=%d owner=%d", lifecycle, owner)
		}
		accept, err := f.accept(t, 3, "mp-execution", 3, 0x70)
		if err != nil || accept.Outcome.State != Applied || accept.Outcome.Replayed ||
			accept.Value.State != "accepted" || accept.Value.Custody != "mp-session" || accept.Value.Revision != 4 {
			t.Fatal("accept fenced on the receipt-debt revision", accept, err)
		}
		facts := mpStartAcceptReadFacts(t, f)
		if facts.state != "accepted" || facts.revision != 4 || facts.custody != "mp-session" || facts.receipts != 3 {
			t.Fatalf("accept effects %+v", facts)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("retire", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 1)
		mpStartAcceptAckBump(t, f, 1)
		if lifecycle, owner := mpStartAcceptCounters(t, f); lifecycle != 2 || owner != lifecycle+1 {
			t.Fatalf("fixture counters did not drift: lifecycle=%d owner=%d", lifecycle, owner)
		}
		retire, err := f.retire(t, 2, "mp-execution", 2, 0x71)
		if err != nil || retire.Outcome.State != Applied || retire.Value.State != "retired" ||
			retire.Value.Custody != "" || retire.Value.Revision != 3 {
			t.Fatal("retire fenced on the receipt-debt revision", retire, err)
		}
		if facts := mpStartAcceptReadFacts(t, f); facts.state != "retired" || facts.revision != 3 || facts.receipts != 2 {
			t.Fatalf("retire effects %+v", facts)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("timeout", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 1)
		mpStartExpire(t, f)
		mpStartAcceptAckBump(t, f, 1)
		if lifecycle, owner := mpStartAcceptCounters(t, f); lifecycle != 2 || owner != lifecycle+1 {
			t.Fatalf("fixture counters did not drift: lifecycle=%d owner=%d", lifecycle, owner)
		}
		timeout, err := f.timeout(t, 2, "mp-execution", 2, 0x72)
		if err != nil || timeout.Outcome.State != Applied || timeout.Value.State != "retired" ||
			timeout.Value.Custody != "" || timeout.Value.Revision != 3 {
			t.Fatal("timeout fenced on the receipt-debt revision", timeout, err)
		}
		if facts := mpStartAcceptReadFacts(t, f); facts.state != "retired" || facts.revision != 3 || facts.receipts != 2 {
			t.Fatalf("timeout effects %+v", facts)
		}
		mpStartPrepAssertReturned(t, f)
	})
}

// mpStartAcceptWithReplacement seals a preparation whose replaced logical
// session still exists, so the fresh replacement gate has a witness to lose.
func mpStartAcceptWithReplacement(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	if _, err := f.e.db.ExecContext(t.Context(), `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES('mp-previous','mp-start-device',?,?,'local','mp-previous-key','mp-previous-digest','movie','primary','active','{}',1,2,?,100,100)`, f.actor.account, f.actor.profile, f.actor.horizonMS); err != nil {
		t.Fatal(err)
	}
	q := f.beginInput()
	q.replaces, q.replacesGeneration = "mp-previous", 2
	command, err := newMPStartPrepCommand(MPStartPrepBegin, f.beginIdentity(), q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.run(t, MPStartPrepBegin, command.reference, q); err != nil {
		t.Fatal(err)
	}
	if _, err = f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
		t.Fatal(err)
	}
	f.ack(t, 1)
	if _, err = f.seal(t, 2, mpStartPrepCompletion{execution: "mp-execution", resultDigest: strings.Repeat("f", 64), stagedRows: 4, stagedBytes: 128}, 2, 0x65); err != nil {
		t.Fatal(err)
	}
	f.ack(t, 2)
}

func mpStartAcceptMoveQueue(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET custody_generation=8 WHERE id='mp-queue'`); err != nil {
		t.Fatal(err)
	}
}

func mpStartAcceptEndReplacedSession(t *testing.T, f mpStartPrepFixture) {
	t.Helper()
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE playback_v1_sessions SET state='ended',ended_ms=updated_ms+1 WHERE id='mp-previous'`); err != nil {
		t.Fatal(err)
	}
}

// TestMPStartAcceptReplayAfterFreshStateMoved pins receipt-first replay for
// 703/704/705: a lost-response retry of an APPLIED but unACKed terminal command
// returns its saved original with zero new effects after the deadline passed,
// the queue moved or the replaced session ended. A genuinely FRESH 703 in the
// same states is still refused.
func TestMPStartAcceptReplayAfterFreshStateMoved(t *testing.T) {
	cases := []struct {
		name   string
		seal   func(*testing.T, mpStartPrepFixture)
		move   func(*testing.T, mpStartPrepFixture)
		reason string
	}{
		{"deadline-passed", mpStartAcceptSealed, mpStartExpire, "expired"},
		{"queue-moved", mpStartAcceptSealed, mpStartAcceptMoveQueue, "queue drift"},
		{"replaced-session-ended", mpStartAcceptWithReplacement, mpStartAcceptEndReplacedSession, "ended replacement"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Replay the saved original first: the same applied, unACKed 703
			// sequence is retried after the head moved.
			f := mpStartAcceptFixtureFor(t)
			c.seal(t, f)
			accept, err := f.accept(t, 3, "mp-execution", 3, 0x70)
			if err != nil || accept.Outcome.State != Applied || accept.Outcome.Replayed {
				t.Fatal(accept, err)
			}
			c.move(t, f)
			moved := mpStartAcceptReadFacts(t, f)
			replay, err := f.accept(t, 3, "mp-execution", 3, 0x70)
			if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed ||
				replay.Value != accept.Value || replay.Digest != accept.Digest {
				t.Fatal("replay after "+c.reason, replay, err)
			}
			if after := mpStartAcceptReadFacts(t, f); after != moved || after.state != "accepted" || after.custody != "mp-session" {
				t.Fatalf("replay after %s added effects %+v -> %+v", c.reason, moved, after)
			}
			mpStartPrepAssertReturned(t, f)

			// The same head state must still refuse a genuinely fresh 703 with
			// zero effects: replay is the ONLY path that skips the fresh gates.
			g := mpStartAcceptFixtureFor(t)
			c.seal(t, g)
			c.move(t, g)
			before := mpStartAcceptReadFacts(t, g)
			if out, e := g.accept(t, 3, "mp-execution", 3, 0x70); e == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
				t.Fatal("fresh accept admitted after "+c.reason, out, e)
			}
			if after := mpStartAcceptReadFacts(t, g); after != before || after.custody != "" || after.state != "prepared" {
				t.Fatalf("fresh refusal after %s changed state %+v -> %+v", c.reason, before, after)
			}
			mpStartPrepAssertReturned(t, g)
		})
	}
}

func TestMPStartAcceptRefusalsKeepOriginalState(t *testing.T) {
	for _, name := range []string{"withdrawn-actor", "revoked-family", "stale-source", "stale-queue", "expired", "unsealed", "forged-actor", "wrong-revision", "copied", "cancelled", "stale-authority"} {
		t.Run(name, func(t *testing.T) {
			f := mpStartAcceptFixtureFor(t)
			mpStartAcceptSealed(t, f)
			// Apply the attacker/expiry mutation first so the pre-command facts are
			// the exact durable state the refusal must leave untouched.
			switch name {
			case "withdrawn-actor":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=?`, f.actor.sessionHash); e != nil {
					t.Fatal(e)
				}
			case "revoked-family":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1 WHERE id=?`, f.actor.family); e != nil {
					t.Fatal(e)
				}
			case "stale-authority":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET generation=2 WHERE family_id=? AND generation=1`, f.actor.family); e != nil {
					t.Fatal(e)
				}
			case "stale-source":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE inventory_objects SET revision='r2' WHERE id='mp-object'`); e != nil {
					t.Fatal(e)
				}
			case "stale-queue":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET custody_generation=8 WHERE id='mp-queue'`); e != nil {
					t.Fatal(e)
				}
			case "expired":
				mpStartExpire(t, f)
			case "unsealed":
				if _, e := f.e.db.ExecContext(t.Context(), `UPDATE playback_start_preparation_lifecycle SET state='preparing',revision=2 WHERE operation_id='mp-prep'`); e != nil {
					t.Fatal(e)
				}
			}
			before := mpStartAcceptReadFacts(t, f)
			var out mpStartAcceptResult
			var err error
			switch name {
			case "withdrawn-actor", "revoked-family", "stale-authority", "stale-source", "stale-queue", "expired", "unsealed":
				out, err = f.accept(t, 3, "mp-execution", 3, 0x70)
			case "forged-actor":
				q := mpStartAcceptInput{fence: f.acceptFence("mp-execution", 3)}
				q.fence.actor.device = "forged-device"
				out, err = f.terminal(t, MPStartPrepAccept, q, 3, 0x70)
			case "wrong-revision":
				out, err = f.accept(t, 2, "mp-execution", 3, 0x70)
			case "copied":
				f.cap = &mpStartPrepDisclosure{self: f.cap, writer: f.cap.writer, lifetime: f.cap.lifetime, actor: f.cap.actor, library: f.cap.library, incarnation: f.cap.incarnation}
				out, err = f.accept(t, 3, "mp-execution", 3, 0x70)
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				out, err = f.terminalContext(t, ctx, MPStartPrepAccept, mpStartAcceptInput{fence: f.acceptFence("mp-execution", 3)}, 3, 0x70)
			}
			if err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
				t.Fatal("refusal disclosed a result", out, err)
			}
			after := mpStartAcceptReadFacts(t, f)
			if after != before || after.custody != "" || after.physicalExited != 0 || after.cleaned != 0 || after.sessions != 0 || after.mediaInputs != 0 {
				t.Fatalf("refusal changed custody/effects %+v -> %+v", before, after)
			}
			mpStartPrepAssertReturned(t, f)
		})
	}
}

// mpStartAcceptPrefixFault fails one exact statement so the turn is observed
// after its earlier writes already ran inside the same transaction. Both the
// query and exec driver paths are intercepted because the terminal CAS
// statements deliberately return no row (they are not QueryContext-only).
type mpStartAcceptPrefixFault struct {
	writerDriverConn
	target     string
	occurrence int
	seen       int
}

func (c *mpStartAcceptPrefixFault) fire(q string) error {
	if q != c.target {
		return nil
	}
	c.seen++
	if c.seen == c.occurrence {
		return errMPMutation
	}
	return nil
}

func (c *mpStartAcceptPrefixFault) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.fire(q); err != nil {
		return nil, err
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}

func (c *mpStartAcceptPrefixFault) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.fire(q); err != nil {
		return nil, err
	}
	return c.writerDriverConn.ExecContext(ctx, q, args)
}

func mpStartAcceptReopenFaultPool(t *testing.T, f mpStartPrepFixture, fault *mpStartAcceptPrefixFault) {
	t.Helper()
	if err := f.e.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(f.w.effective.path, f.e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	fault.writerDriverConn = nil
	f.e.openPool(connector, func(c driver.Conn) driver.Conn {
		fault.writerDriverConn = c.(writerDriverConn)
		return fault
	})
	primeEffectiveFixturePool(t, f.e)
}

func TestMPStartAcceptPrefixRollback(t *testing.T) {
	for _, p := range []struct {
		name string
		step CommandID
		id   stmt.ID
	}{
		{"accept-custody", MPStartPrepAccept, "mp.accept.custody"},
		{"accept-lifecycle", MPStartPrepAccept, "mp.accept.lifecycle"},
		{"accept-receipt", MPStartPrepAccept, "mp.accept.receipt_insert"},
		{"accept-debt", MPStartPrepAccept, "mp.accept.debt"},
		{"retire-lifecycle", MPStartPrepRetire, "mp.accept.retire"},
		{"retire-receipt", MPStartPrepRetire, "mp.accept.receipt_insert"},
		{"retire-debt", MPStartPrepRetire, "mp.accept.debt"},
		{"timeout-lifecycle", MPStartPrepTimeout, "mp.accept.timeout"},
		{"timeout-receipt", MPStartPrepTimeout, "mp.accept.receipt_insert"},
		{"timeout-debt", MPStartPrepTimeout, "mp.accept.debt"},
	} {
		t.Run(p.name, func(t *testing.T) {
			f := mpStartAcceptFixtureFor(t)
			if p.step == MPStartPrepAccept {
				mpStartAcceptSealed(t, f)
			} else {
				if _, err := f.begin(t); err != nil {
					t.Fatal(err)
				}
				if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
					t.Fatal(err)
				}
				f.ack(t, 1)
				if p.step == MPStartPrepTimeout {
					mpStartExpire(t, f)
				}
			}
			before := mpStartAcceptReadFacts(t, f)
			var target string
			for _, d := range mpStartAcceptDefinitions() {
				if d.ID == p.id {
					target = d.SQL
				}
			}
			if target == "" {
				t.Fatal(p.id)
			}
			mpStartAcceptReopenFaultPool(t, f, &mpStartAcceptPrefixFault{target: target, occurrence: 1})
			var out mpStartAcceptResult
			var err error
			switch p.step {
			case MPStartPrepAccept:
				out, err = f.accept(t, 3, "mp-execution", 3, 0x70)
			case MPStartPrepRetire:
				out, err = f.retire(t, 2, "mp-execution", 2, 0x71)
			case MPStartPrepTimeout:
				out, err = f.timeout(t, 2, "mp-execution", 2, 0x72)
			}
			if !errors.Is(err, errMPMutation) || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
				t.Fatal(out, err)
			}
			if after := mpStartAcceptReadFacts(t, f); after != before {
				t.Fatalf("prefix left partial effects: before %+v after %+v", before, after)
			}
			mpStartPrepAssertReturned(t, f)
		})
	}
}

func TestMPStartAcceptUnknownCommitNeverApplied(t *testing.T) {
	cases := []struct {
		name    string
		kind    CommandID
		sealed  bool
		expires bool
		run     func(t *testing.T, f mpStartPrepFixture) (mpStartAcceptResult, error)
		want    mpStartAcceptFacts
	}{
		{name: "accept", kind: MPStartPrepAccept, sealed: true,
			run: func(t *testing.T, f mpStartPrepFixture) (mpStartAcceptResult, error) {
				return f.accept(t, 3, "mp-execution", 3, 0x70)
			},
			want: mpStartAcceptFacts{state: "accepted", revision: 4, sealed: 1, receipts: 3, custody: "mp-session", physicalExited: 0, cleaned: 0}},
		{name: "retire", kind: MPStartPrepRetire,
			run: func(t *testing.T, f mpStartPrepFixture) (mpStartAcceptResult, error) {
				return f.retire(t, 2, "mp-execution", 2, 0x71)
			},
			want: mpStartAcceptFacts{state: "retired", revision: 3, sealed: 1, receipts: 2, custody: "", physicalExited: 0, cleaned: 0}},
		{name: "timeout", kind: MPStartPrepTimeout, expires: true,
			run: func(t *testing.T, f mpStartPrepFixture) (mpStartAcceptResult, error) {
				return f.timeout(t, 2, "mp-execution", 2, 0x72)
			},
			want: mpStartAcceptFacts{state: "retired", revision: 3, sealed: 1, receipts: 2, custody: "", physicalExited: 0, cleaned: 0}},
	}
	for _, c := range cases {
		for _, after := range []bool{false, true} {
			t.Run(c.name+"/"+map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
				f := mpStartAcceptFixtureFor(t)
				if c.sealed {
					mpStartAcceptSealed(t, f)
				} else {
					if _, err := f.begin(t); err != nil {
						t.Fatal(err)
					}
					if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
						t.Fatal(err)
					}
					f.ack(t, 1)
				}
				if c.expires {
					mpStartExpire(t, f)
				}
				mpStartPrepFaultPool(t, f, &faultConnector{replyError: true, afterCommit: after})
				out, err := c.run(t, f)
				if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
					t.Fatal("uncertain commit exposed success", out, err)
				}
				// Observe durable state through a FRESH read-only native connection,
				// never the quarantined writer pool.
				reader, _, err := sqliteconn.Open(t.Context(), f.w.effective.path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				var state, custody string
				var revision, receipts int64
				if err = reader.QueryRowContext(t.Context(), `SELECT state,revision FROM playback_start_preparation_lifecycle WHERE operation_id='mp-prep'`).Scan(&state, &revision); err != nil {
					t.Fatal(err)
				}
				if err = reader.QueryRowContext(t.Context(), `SELECT count(*) FROM mp_start_command_receipts`).Scan(&receipts); err != nil {
					t.Fatal(err)
				}
				if err = reader.QueryRowContext(t.Context(), `SELECT coalesce(max(custody_media_session_id),'') FROM mp_start_execution WHERE operation_id='mp-prep'`).Scan(&custody); err != nil {
					t.Fatal(err)
				}
				want := mpStartAcceptFacts{state: "prepared", revision: 3, receipts: 2}
				if c.name == "retire" || c.name == "timeout" {
					want = mpStartAcceptFacts{state: "preparing", revision: 2, receipts: 1}
				}
				if after {
					want = c.want
				}
				if state != want.state || revision != want.revision || receipts != want.receipts || custody != want.custody {
					t.Fatalf("unknown-commit durable state state=%s revision=%d receipts=%d custody=%q", state, revision, receipts, custody)
				}
				mpStartPrepAssertReturned(t, f)
			})
		}
	}
}

func TestMPExpiryRetireAndTimeoutRetainPhysicalDebt(t *testing.T) {
	t.Run("retire-preparing", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 1)
		// Retiring an abandoned preparation must not adopt today's source/queue:
		// drift both and still retire the stale operation.
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE inventory_objects SET revision='r2' WHERE id='mp-object'`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE queues_v1 SET custody_generation=8 WHERE id='mp-queue'`); err != nil {
			t.Fatal(err)
		}
		before := mpStartAcceptReadFacts(t, f)
		retire, err := f.retire(t, 2, "mp-execution", 2, 0x71)
		if err != nil || retire.Outcome.State != Applied || retire.Value.State != "retired" ||
			retire.Value.Execution != "mp-execution" || retire.Value.Custody != "" || retire.Value.Revision != 3 ||
			retire.Value.PhysicalExited != 0 || retire.Value.Cleaned != 0 {
			t.Fatal(retire, err)
		}
		after := mpStartAcceptReadFacts(t, f)
		if after.state != "retired" || after.revision != 3 || after.custody != "" || after.receipts != 2 {
			t.Fatalf("retire effects %+v", after)
		}
		if after.executionRows != before.executionRows || after.stagedRows != before.stagedRows ||
			after.stagedBytes != before.stagedBytes || after.resultDigest != before.resultDigest ||
			after.physicalExited != 0 || after.cleaned != 0 || after.sessions != 0 || after.mediaInputs != 0 {
			t.Fatalf("retire freed physical debt %+v -> %+v", before, after)
		}
		f.ack(t, 2)
		retired, err := f.retire(t, 2, "mp-execution", 2, 0x71)
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (mpStartAcceptValue{}) {
			t.Fatal("ACKed retire served as Applied", retired, err)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("retire-pending", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		retire, err := f.retire(t, 1, "", 1, 0x71)
		if err != nil || retire.Outcome.State != Applied || retire.Value.State != "retired" ||
			retire.Value.Execution != "" || retire.Value.Custody != "" || retire.Value.Revision != 2 {
			t.Fatal(retire, err)
		}
		facts := mpStartAcceptReadFacts(t, f)
		if facts.state != "retired" || facts.revision != 2 || facts.executionRows != 0 || facts.receipts != 1 {
			t.Fatalf("pending retire effects %+v", facts)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("timeout-not-due", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 1)
		before := mpStartAcceptReadFacts(t, f)
		if out, err := f.timeout(t, 2, "mp-execution", 2, 0x72); err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
			t.Fatal("future deadline timed out", out, err)
		}
		if after := mpStartAcceptReadFacts(t, f); after != before {
			t.Fatalf("early timeout changed state %+v -> %+v", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("timeout-pending", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		mpStartExpire(t, f)
		timeout, err := f.timeout(t, 1, "", 1, 0x72)
		if err != nil || timeout.Outcome.State != Applied || timeout.Value.State != "retired" ||
			timeout.Value.Execution != "" || timeout.Value.Custody != "" || timeout.Value.Revision != 2 ||
			timeout.Value.PhysicalExited != 0 || timeout.Value.Cleaned != 0 {
			t.Fatal(timeout, err)
		}
		if facts := mpStartAcceptReadFacts(t, f); facts.state != "retired" || facts.revision != 2 || facts.executionRows != 0 || facts.receipts != 1 {
			t.Fatalf("pending timeout effects %+v", facts)
		}
		f.ack(t, 1)
		retired, err := f.timeout(t, 1, "", 1, 0x72)
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (mpStartAcceptValue{}) {
			t.Fatal("ACKed timeout served as Applied", retired, err)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("timeout-preparing", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		if _, err := f.begin(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 1)
		before := mpStartAcceptReadFacts(t, f)
		mpStartExpire(t, f)
		timeout, err := f.timeout(t, 2, "mp-execution", 2, 0x72)
		if err != nil || timeout.Outcome.State != Applied || timeout.Value.State != "retired" ||
			timeout.Value.Execution != "mp-execution" || timeout.Value.Custody != "" || timeout.Value.Revision != 3 {
			t.Fatal(timeout, err)
		}
		after := mpStartAcceptReadFacts(t, f)
		if after.state != "retired" || after.revision != 3 || after.custody != "" || after.receipts != 2 {
			t.Fatalf("timeout effects %+v", after)
		}
		if after.executionRows != before.executionRows || after.stagedRows != before.stagedRows ||
			after.stagedBytes != before.stagedBytes || after.resultDigest != before.resultDigest ||
			after.physicalExited != 0 || after.cleaned != 0 {
			t.Fatalf("timeout freed physical debt %+v -> %+v", before, after)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("accept-versus-timeout", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		mpStartAcceptSealed(t, f)
		mpStartExpire(t, f)
		if out, err := f.accept(t, 3, "mp-execution", 3, 0x70); err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
			t.Fatal("expired accept admitted", out, err)
		}
		timeout, err := f.timeout(t, 3, "mp-execution", 3, 0x72)
		if err != nil || timeout.Outcome.State != Applied || timeout.Value.State != "retired" ||
			timeout.Value.Execution != "mp-execution" || timeout.Value.Custody != "" || timeout.Value.Revision != 4 {
			t.Fatal(timeout, err)
		}
		f.ack(t, 3)
		if out, err := f.accept(t, 3, "mp-execution", 4, 0x70); err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
			t.Fatal("accept after timeout admitted", out, err)
		}
		if facts := mpStartAcceptReadFacts(t, f); facts.state != "retired" || facts.custody != "" || facts.receipts != 3 {
			t.Fatalf("timeout displaced accept %+v", facts)
		}
		mpStartPrepAssertReturned(t, f)
	})
	t.Run("accept-then-terminal", func(t *testing.T) {
		f := mpStartAcceptFixtureFor(t)
		mpStartAcceptSealed(t, f)
		if _, err := f.accept(t, 3, "mp-execution", 3, 0x70); err != nil {
			t.Fatal(err)
		}
		f.ack(t, 3)
		if out, err := f.retire(t, 3, "mp-execution", 4, 0x71); err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
			t.Fatal("accepted preparation retired", out, err)
		}
		mpStartExpire(t, f)
		if out, err := f.timeout(t, 3, "mp-execution", 4, 0x72); err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
			t.Fatal("accepted preparation timed out", out, err)
		}
		if facts := mpStartAcceptReadFacts(t, f); facts.state != "accepted" || facts.custody != "mp-session" {
			t.Fatalf("terminal command displaced accept %+v", facts)
		}
		mpStartPrepAssertReturned(t, f)
	})
}

func TestMPExpiryRefusalsKeepOriginalState(t *testing.T) {
	for _, name := range []string{"withdrawn-actor", "stale-authority", "forged-actor", "copied", "cancelled", "wrong-revision"} {
		for _, kind := range []CommandID{MPStartPrepRetire, MPStartPrepTimeout} {
			t.Run(name+"/"+map[CommandID]string{MPStartPrepRetire: "retire", MPStartPrepTimeout: "timeout"}[kind], func(t *testing.T) {
				f := mpStartAcceptFixtureFor(t)
				if _, err := f.begin(t); err != nil {
					t.Fatal(err)
				}
				if _, err := f.claim(t, 1, "mp-execution", 1, 0x62); err != nil {
					t.Fatal(err)
				}
				f.ack(t, 1)
				if kind == MPStartPrepTimeout {
					mpStartExpire(t, f)
				}
				switch name {
				case "withdrawn-actor":
					if _, e := f.e.db.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=?`, f.actor.sessionHash); e != nil {
						t.Fatal(e)
					}
				case "stale-authority":
					if _, e := f.e.db.ExecContext(t.Context(), `UPDATE authorization_family_tokens SET generation=2 WHERE family_id=? AND generation=1`, f.actor.family); e != nil {
						t.Fatal(e)
					}
				}
				before := mpStartAcceptReadFacts(t, f)
				var out mpStartAcceptResult
				var err error
				revision := int64(2)
				if name == "wrong-revision" {
					revision = 1
				}
				input := any(mpStartRetireInput{fence: f.acceptFence("mp-execution", revision)})
				if kind == MPStartPrepTimeout {
					input = mpStartTimeoutInput{fence: f.acceptFence("mp-execution", revision)}
				}
				switch name {
				case "forged-actor":
					if kind == MPStartPrepRetire {
						q := mpStartRetireInput{fence: f.acceptFence("mp-execution", revision)}
						q.fence.actor.device = "forged-device"
						input = q
					} else {
						q := mpStartTimeoutInput{fence: f.acceptFence("mp-execution", revision)}
						q.fence.actor.device = "forged-device"
						input = q
					}
					out, err = f.terminal(t, kind, input, 2, 0x71)
				case "copied":
					f.cap = &mpStartPrepDisclosure{self: f.cap, writer: f.cap.writer, lifetime: f.cap.lifetime, actor: f.cap.actor, library: f.cap.library, incarnation: f.cap.incarnation}
					out, err = f.terminal(t, kind, input, 2, 0x71)
				case "cancelled":
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					out, err = f.terminalContext(t, ctx, kind, input, 2, 0x71)
				default:
					out, err = f.terminal(t, kind, input, 2, 0x71)
				}
				if err == nil || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
					t.Fatal("refusal disclosed a result", out, err)
				}
				if after := mpStartAcceptReadFacts(t, f); after != before || after.custody != "" {
					t.Fatalf("refusal changed custody/effects %+v -> %+v", before, after)
				}
				mpStartPrepAssertReturned(t, f)
			})
		}
	}
}

func mpStartAcceptTestValue(kind CommandID) mpStartAcceptValue {
	if kind == MPStartPrepAccept {
		return mpStartAcceptValue{Preparation: "mp-prep", State: "accepted", Execution: "mp-execution", Custody: "mp-session",
			Session: "mp-session", LegacySession: "mp-legacy", LegacyGeneration: 9, Snapshot: "mp-snapshot",
			Revision: 4, DeadlineMS: 900, AppliedMS: 100}
	}
	return mpStartAcceptValue{Preparation: "mp-prep", State: "retired", Execution: "mp-execution",
		Session: "mp-session", LegacySession: "mp-legacy", LegacyGeneration: 9, Snapshot: "mp-snapshot",
		Revision: 3, DeadlineMS: 900, AppliedMS: 100}
}

func TestMPStartAcceptPayloadIsACompleteOriginal(t *testing.T) {
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 3, OperationID: [16]byte{2}}
	request := RequestDigest{3}
	for _, kind := range []CommandID{MPStartPrepAccept, MPStartPrepRetire} {
		value := mpStartAcceptTestValue(kind)
		payload, digest, err := mpStartAcceptPayload(id, request, value)
		if err != nil || len(payload) == 0 || len(payload) > mpStartAcceptPayloadCap || digest == (ResultDigest{}) {
			t.Fatal(kind, len(payload), digest, err)
		}
		decoded, err := decodeMPStartAcceptPayload(id, request, payload, digest)
		if err != nil || decoded != value {
			t.Fatal("original bytes changed", decoded, err)
		}
		other := id
		other.Sequence++
		if _, err := decodeMPStartAcceptPayload(other, request, payload, digest); err == nil {
			t.Fatal("sequence drift accepted")
		}
		other = id
		other.OperationID[0] ^= 1
		if _, err := decodeMPStartAcceptPayload(other, request, payload, digest); err == nil {
			t.Fatal("operation drift accepted")
		}
		if _, err := decodeMPStartAcceptPayload(id, RequestDigest{4}, payload, digest); err == nil {
			t.Fatal("request drift accepted")
		}
		if _, err := decodeMPStartAcceptPayload(id, request, append(append([]byte{}, payload...), 0), digest); err == nil {
			t.Fatal("trailing bytes accepted")
		}
		forged := value
		forged.Custody = "other-session"
		if _, _, err := mpStartAcceptPayload(id, request, forged); err == nil {
			t.Fatal("impossible custody encoded", kind)
		}
	}
	for _, v := range []mpStartAcceptValue{
		{Preparation: "prep", State: "accepted", Revision: 4, DeadlineMS: 1, AppliedMS: 1, LegacyGeneration: 1, Execution: "e", Session: "s"},
		{Preparation: "prep", State: "retired", Revision: 3, DeadlineMS: 1, AppliedMS: 1, LegacyGeneration: 1, Custody: "s", Session: "s"},
		{Preparation: "", State: "retired", Revision: 3, DeadlineMS: 1, AppliedMS: 1, LegacyGeneration: 1, Session: "s"},
		{Preparation: "prep", State: "retired", Revision: 3, DeadlineMS: 1, AppliedMS: 1, LegacyGeneration: 1, Session: "s", PhysicalExited: 1, Cleaned: 0},
	} {
		if err := preflightMPStartAcceptValue(v); err == nil {
			t.Fatal("invalid result encoded", v)
		}
	}
	if mpStartAcceptValueKind("accepted") != MPStartPrepAccept || mpStartAcceptValueKind("retired") != MPStartPrepRetire ||
		mpStartAcceptValueKind("prepared") != 0 {
		t.Fatal("result state lost its command")
	}
}

func TestMPStartAcceptClosedDefinitionSyntax(t *testing.T) {
	for _, d := range mpStartAcceptDefinitions() {
		if strings.Contains(d.SQL, "octet_length(") {
			continue
		}
		if issues := plan.LintSQL(d.SQL); len(issues) > 0 {
			t.Fatal(d.ID, issues)
		}
	}
	for _, d := range mpStartAcceptDefinitions() {
		if d.Kind == stmt.Write && strings.HasPrefix(strings.TrimSpace(d.SQL), "INSERT") && (len(d.Plan.Indexes) != 0 || len(d.Plan.PrimaryKeys) != 0) {
			t.Fatal("plain INSERT carries a read seek", d.ID)
		}
	}
	// A terminal command never writes a playback session, media input or grant.
	for _, d := range mpStartAcceptDefinitions() {
		if d.Kind == stmt.Write && strings.Contains(d.SQL, "INSERT INTO playback_sessions") {
			t.Fatal("terminal start minted a physical session", d.ID)
		}
	}
}

func TestMPStartAcceptCompositionStaysClosed(t *testing.T) {
	f := mpStartPrepFixtureFor(t)
	// Accept/retire/timeout need MP-07's staged rows and receipt root, so the
	// preparation profile is installed before the first birth.
	mpStartPrepComposeFixture(t, f)
	if _, err := f.begin(t); err != nil {
		t.Fatal(err)
	}
	out, err := f.accept(t, 1, "mp-execution", 1, 0x70)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, mpStartAcceptResult{}) {
		t.Fatal("uninstalled terminal command ran", out, err)
	}
	if err := composeMPStartAccept(t.Context(), f.w.effective, effectiveFixturePlan()); err == nil || f.w.effective.mpStartAcceptPlan != nil {
		t.Fatal("unqualified terminal profile installed", err)
	}
	if f.w.effective.mpStartAcceptPlan != nil {
		t.Fatal("plan left installed")
	}
	mpStartAcceptCompose(t, f)
	r := f.w.effective
	old := r.mpStartAcceptPlan
	bad := *old
	bad.registration = nil
	r.mpStartAcceptPlan = nil
	if err := composeMPStartAccept(t.Context(), r, bad); err == nil || r.mpStartAcceptPlan != nil {
		t.Fatal("empty terminal registration installed", err)
	}
	r.mpStartAcceptPlan = old
	if err := composeMPStartAccept(t.Context(), r, effectiveFixturePlan()); err == nil {
		t.Fatal("second terminal profile installed")
	}
}
