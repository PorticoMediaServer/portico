package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Channel control fixtures. The canonical base supplies lc-channel at revision
// 7 with the retained lc-old head, one old entry and one playback reference.
// Every helper reads the immediate native witness from the retained owner; a
// caller never invents a revision, head or directory page.
func mgLCControlTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgLCControlDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 64 << 20, 25 * time.Millisecond},
		setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_, _ = tx.rollback(nil)
		}
	})
	steps := make([]statementStep, 0, len(defs))
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 64, 1 << 20, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func mgLCControlReadWitness(t *testing.T, w *executor, id string) mgLCControlChannel {
	t.Helper()
	turn := mgLCControlTurn(t, w, MGSaveLCConfiguration)
	ch, err := readMGLCControlChannel(t.Context(), turn, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	return ch
}

func mgLCControlReadDirectory(t *testing.T, w *executor) []mgLCControlChannel {
	t.Helper()
	turn := mgLCControlTurn(t, w, MGReorderLCChannels)
	all, err := readMGLCControlDirectory(t.Context(), turn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	return all
}

func mgLCControlIssuer() [16]byte { return [16]byte{42} }

// mgLCControlAcceptsOn installs the ordinary US owner population on w and
// returns the actor proof every accepting control frame now requires.
func mgLCControlAcceptsOn(t *testing.T, w *executor) *mgActorProof {
	t.Helper()
	now := w.now().UTC().Truncate(time.Second)
	proof := usOwnerAuthorityPopulation(t, w, now)
	return &mgActorProof{native: *proof, kind: mgActorOwner}
}

func mgLCControlFixture(t *testing.T) (*executor, mgLCBuild, *mgActorProof) {
	t.Helper()
	w, b := mgLCFixture(t)
	return w, b, mgLCControlAcceptsOn(t, w)
}

// mgLCControlAuthorityTurn registers both the control and the MG actor
// statements on the same native turn and funds one governor-shaped budget.
func mgLCControlAuthorityTurn(t *testing.T, w *executor, purpose CommandID) (*commandTurn, mgAuthorityBudget) {
	t.Helper()
	defs := append(mgLCControlDefinitions(), mgAuthorityDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{10000, 10000000, 10000, 64 << 20, 25 * time.Millisecond},
		setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_, _ = tx.rollback(nil)
		}
	})
	steps := make([]statementStep, 0, len(defs))
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 5000, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn, mgAuthorityBudget{memory: 4 << 20, fixture: &mgAuthorityFixturePermit{frame: tx}}
}

func mgLCControlSaveWitness(t *testing.T, w *executor, id string) mgLCControlWitness {
	t.Helper()
	return mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlReadWitness(t, w, id)}
}

func mgLCControlOrderWitness(t *testing.T, w *executor) mgLCControlWitness {
	t.Helper()
	return mgLCControlWitness{issuer: mgLCControlIssuer(), directory: mgLCControlReadDirectory(t, w)}
}

func mgLCControlCommandFor(kind mgLCControlKind, w mgLCControlWitness, in mgLCControlInput, operation byte) mgLCControlCommand {
	return mgLCControlCommand{
		identity: CommandIdentity{Incarnation: w.issuer, Sequence: 1, OperationID: [16]byte{operation}},
		request:  mgLCControlRequestDigest(kind, w, in),
	}
}

func mgLCControlConfigFor(id, name string, enabled, position int64) mgLCControlConfig {
	return mgLCControlConfig{canonical: `{"id":"` + id + `"}`, name: name, enabled: enabled, position: position}
}

// One committed control command.
func mgLCControlApply(t *testing.T, w *executor, proof *mgActorProof, purpose CommandID, witness mgLCControlWitness, in mgLCControlInput, operation byte) mgLCControlSaved {
	t.Helper()
	kind, _ := mgLCControlKindFor(purpose)
	turn, budget := mgLCControlAuthorityTurn(t, w, purpose)
	saved, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, purpose, witness, mgLCControlCommandFor(kind, witness, in, operation), in)
	if err != nil {
		t.Fatal(purpose, err)
	}
	if decision != receiptFresh {
		t.Fatal("fresh control command did not resolve fresh", decision)
	}
	mgLCCommit(t, turn)
	return saved
}

// One control attempt that must fail; the turn is rolled back and the durable
// row is read back through the ordinary writer pool afterwards.
func mgLCControlRefuse(t *testing.T, w *executor, proof *mgActorProof, purpose CommandID, witness mgLCControlWitness, in mgLCControlInput, operation byte) error {
	t.Helper()
	kind, _ := mgLCControlKindFor(purpose)
	turn, budget := mgLCControlAuthorityTurn(t, w, purpose)
	before := mgLCControlChanges(t, turn)
	saved, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, purpose, witness, mgLCControlCommandFor(kind, witness, in, operation), in)
	if err == nil || !reflect.DeepEqual(saved, mgLCControlSaved{}) {
		t.Fatal("control attempt was not refused", saved, err)
	}
	if after := mgLCControlChanges(t, turn); after != before {
		t.Fatal("refused control performed SQL effects", before, after)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	return err
}

func mgLCControlChanges(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var n int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mgLCControlReceiptCount(t *testing.T, w *executor) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM lc_receipts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMGLCControlBirthEditAndNoHeadCustody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, _, proof := mgLCControlFixture(t)
		birth := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlChannel{id: "control-new"}}
		config := mgLCControlConfigFor("control-new", "New", 1, 0)
		saved := mgLCControlApply(t, w, proof, MGSaveLCConfiguration, birth, mgLCControlInput{config: &config}, 1)
		if saved.kind != mgLCControlSaveKind || saved.result.channel.revision != 1 || saved.result.channel.head != "" ||
			saved.result.channel.state != "generating" || saved.result.channel.enabled != 1 || saved.digest == (ResultDigest{}) {
			t.Fatal("no-head birth", saved)
		}
		var head string
		var ref sql.NullString
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,active_generation_ref FROM lc_channels WHERE id='control-new'`).Scan(&head, &ref); err != nil || head != "" || ref.Valid {
			t.Fatal("birth invented a head", head, ref, err)
		}
		// The same original request replays the saved scalar with zero effects.
		turn, budget := mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
		before := mgLCControlChanges(t, turn)
		replay, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, birth,
			mgLCControlCommandFor(mgLCControlSaveKind, birth, mgLCControlInput{config: &config}, 1), mgLCControlInput{config: &config})
		if err != nil || decision != receiptReplay || !reflect.DeepEqual(replay, saved) {
			t.Fatal("birth replay", replay, decision, err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("birth replay made effects", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// A headless edit keeps the empty stock head and its NULL generated ref.
		editWitness := mgLCControlSaveWitness(t, w, "control-new")
		edited := mgLCControlConfigFor("control-new", "Edited", 0, 3)
		next := mgLCControlApply(t, w, proof, MGSaveLCConfiguration, editWitness, mgLCControlInput{config: &edited}, 2)
		if next.result.channel.revision != 2 || next.result.channel.name != "Edited" || next.result.channel.enabled != 0 ||
			next.result.channel.state != "disabled" || next.result.channel.head != "" {
			t.Fatal("headless edit", next)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,active_generation_ref FROM lc_channels WHERE id='control-new'`).Scan(&head, &ref); err != nil || head != "" || ref.Valid {
			t.Fatal("edit invented a head", head, ref, err)
		}
		// The same operation cannot be replayed with a different payload.
		if err := mgLCControlRefuse(t, w, proof, MGSaveLCConfiguration, editWitness, mgLCControlInput{config: &edited}, 1); !errors.Is(err, ErrConflict) {
			t.Fatal("operation reuse with different input", err)
		}
	})
}

func mgLCControlInsertPending(t *testing.T, w *executor, id, channel string, revision int64, base, lease string) {
	t.Helper()
	q := `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,lease_token,lease_until_ms,command_incarnation,create_operation_id,birth_request_digest,entry_digest,verify_end_ms,verified_entry_digest)
	VALUES(?,?,?,'{}','fence','seed','pending','candidates',?,1000,61000,1000,1000,1,?,9999999999999,zeroblob(16),zeroblob(16),zeroblob(32),zeroblob(32),1000,zeroblob(32))`
	if _, err := w.db.ExecContext(t.Context(), q, id, channel, revision, base, lease); err != nil {
		t.Fatal(err)
	}
}

func TestMGLCControlRegenerationSupersedesExactPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		mgLCControlInsertPending(t, w, "lc-pending", b.channel, 7, b.base, "lease-pending")
		witness := mgLCControlSaveWitness(t, w, b.channel)
		saved := mgLCControlApply(t, w, proof, MGRequestLCRegeneration, witness, mgLCControlInput{}, 3)
		if saved.result.channel.revision != 7 || saved.result.channel.state != "generating" ||
			saved.result.channel.head != b.base || saved.result.channel.enabled != 1 {
			t.Fatal("regeneration control", saved)
		}
		var status, code, lease, head string
		var sequence, ack int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT status,error_code,lease_token,last_command_sequence,acknowledged_command_sequence FROM lc_generations WHERE id='lc-pending'`).Scan(&status, &code, &lease, &sequence, &ack); err != nil {
			t.Fatal(err)
		}
		if status != "superseded" || code != "explicit-regeneration" || lease != "lease-pending" || sequence != 1 || ack != 0 {
			t.Fatal("pending custody changed", status, code, lease, sequence, ack)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,generated_through_ms FROM lc_channels WHERE id=?`, b.channel).Scan(&head, &sequence); err != nil || head != b.base || sequence != 61000 {
			t.Fatal("active generation not retained", head, sequence, err)
		}
		var oldEntries int
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM lc_entries WHERE generation_id=?`, b.base).Scan(&oldEntries); err != nil || oldEntries != 1 {
			t.Fatal("retained old entries", oldEntries, err)
		}
	})
}

func TestMGLCControlPendingHeadConflictStaleAndCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		witness := mgLCControlSaveWitness(t, w, b.channel)
		// A pending build at another revision is a stale head, never retargeted.
		mgLCControlInsertPending(t, w, "lc-pending", b.channel, 8, b.base, "lease-pending")
		if err := mgLCControlRefuse(t, w, proof, MGRequestLCRegeneration, witness, mgLCControlInput{}, 4); !errors.Is(err, ErrConflict) {
			t.Fatal("pending head conflict", err)
		}
		var status string
		if err := w.db.QueryRowContext(t.Context(), `SELECT status FROM lc_generations WHERE id='lc-pending'`).Scan(&status); err != nil || status != "pending" {
			t.Fatal("conflict moved pending", status, err)
		}
		// A stale channel witness refuses before any effect.
		stale := witness
		stale.channel.revision++
		if err := mgLCControlRefuse(t, w, proof, MGSaveLCConfiguration, stale, mgLCControlInput{config: ptrControlConfig(mgLCControlConfigFor(b.channel, "Stale", 1, 0))}, 5); !errors.Is(err, ErrConflict) {
			t.Fatal("stale witness", err)
		}
		// Cancellation is not a refusal that writes.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		turn, budget := mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
		before := mgLCControlChanges(t, turn)
		if _, _, err := mgLCControlDispatch(ctx, turn, proof, ctx, budget, MGSaveLCConfiguration, witness,
			mgLCControlCommandFor(mgLCControlSaveKind, witness, mgLCControlInput{config: ptrControlConfig(mgLCControlConfigFor(b.channel, "Cancel", 1, 0))}, 6),
			mgLCControlInput{config: ptrControlConfig(mgLCControlConfigFor(b.channel, "Cancel", 1, 0))}); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled lifetime", err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("cancelled frame wrote", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}

func ptrControlConfig(c mgLCControlConfig) *mgLCControlConfig { return &c }

func TestMGLCControlRetireRetainsGenerationAndRefusesExpiredReference(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		witness := mgLCControlSaveWitness(t, w, b.channel)
		// The fixture reference lease is long past, but expiry is not physical exit.
		if err := mgLCControlRefuse(t, w, proof, MGRetireLCChannel, witness, mgLCControlInput{}, 7); !errors.Is(err, ErrConflict) {
			t.Fatal("expired playback reference released", err)
		}
		var removed int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT removed FROM lc_channels WHERE id=?`, b.channel).Scan(&removed); err != nil || removed != 0 {
			t.Fatal("retire changed the channel on refusal", removed, err)
		}
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_playback_refs WHERE playback_id='active-player'`); err != nil {
			t.Fatal(err)
		}
		mgLCControlInsertPending(t, w, "lc-pending", b.channel, 7, b.base, "lease-pending")
		saved := mgLCControlApply(t, w, proof, MGRetireLCChannel, witness, mgLCControlInput{}, 8)
		if saved.result.channel.removed != 1 || saved.result.channel.enabled != 0 || saved.result.channel.state != "removed" ||
			saved.result.channel.revision != 8 || saved.result.channel.head != b.base {
			t.Fatal("retirement result", saved)
		}
		var head, status, code string
		var ref sql.NullString
		if err := w.db.QueryRowContext(t.Context(), `SELECT active_generation,active_generation_ref FROM lc_channels WHERE id=?`, b.channel).Scan(&head, &ref); err != nil || head != b.base || !ref.Valid || ref.String != b.base {
			t.Fatal("active head lost", head, ref, err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT status,error_code FROM lc_generations WHERE id='lc-pending'`).Scan(&status, &code); err != nil || status != "superseded" || code != "channel-retired" {
			t.Fatal("pending not superseded by retirement", status, code, err)
		}
		var generations, entries, receipts int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM lc_generations WHERE channel_id=?), (SELECT count(*) FROM lc_entries WHERE channel_id=?), (SELECT count(*) FROM lc_receipts)`, b.channel, b.channel).Scan(&generations, &entries, &receipts); err != nil || generations < 2 || entries != 1 || receipts < 1 {
			t.Fatal("retirement deleted custody", generations, entries, receipts, err)
		}
		// Retirement never frees the generated head reference or owner rows.
		var channels int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM lc_channels WHERE id=?`, b.channel).Scan(&channels); err != nil || channels != 1 {
			t.Fatal("retirement deleted the owner row", channels, err)
		}
	})
}

func TestMGLCControlRetiredDisclosureAndOriginalReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		// A saved pre-retirement result whose channel is later retired must not be
		// disclosed as a current success; the retirement's own result still is.
		saveWitness := mgLCControlSaveWitness(t, w, b.channel)
		config := mgLCControlConfigFor(b.channel, "Before retire", 1, 0)
		savedSave := mgLCControlApply(t, w, proof, MGSaveLCConfiguration, saveWitness, mgLCControlInput{config: &config}, 9)
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM lc_playback_refs WHERE playback_id='active-player'`); err != nil {
			t.Fatal(err)
		}
		retireWitness := mgLCControlSaveWitness(t, w, b.channel)
		savedRetire := mgLCControlApply(t, w, proof, MGRetireLCChannel, retireWitness, mgLCControlInput{}, 10)
		turn, budget := mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
		before := mgLCControlChanges(t, turn)
		got, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, saveWitness,
			mgLCControlCommandFor(mgLCControlSaveKind, saveWitness, mgLCControlInput{config: &config}, 9), mgLCControlInput{config: &config})
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(got, mgLCControlSaved{}) {
			t.Fatal("retired disclosure", got, decision, err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("retired disclosure wrote", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		turn, budget = mgLCControlAuthorityTurn(t, w, MGRetireLCChannel)
		replay, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGRetireLCChannel, retireWitness,
			mgLCControlCommandFor(mgLCControlRetireKind, retireWitness, mgLCControlInput{}, 10), mgLCControlInput{})
		if err != nil || decision != receiptReplay || !reflect.DeepEqual(replay, savedRetire) {
			t.Fatal("retirement replay", replay, decision, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		if savedSave.result.channel.removed != 0 || savedRetire.result.channel.removed != 1 {
			t.Fatal("saved removed flags", savedSave.result.channel.removed, savedRetire.result.channel.removed)
		}
		if mgLCControlReceiptCount(t, w) < 2 {
			t.Fatal("original receipts were deleted")
		}
	})
}

func TestMGLCControlReorderAtomicDirectoryCAS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('lc-second',3,'{"id":"lc-second"}',1,1,'Second','healthy')`); err != nil {
			t.Fatal(err)
		}
		mgLCControlInsertPending(t, w, "lc-pending-second", "lc-second", 3, "", "lease-second")
		witness := mgLCControlOrderWitness(t, w)
		if len(witness.directory) != 2 || witness.directory[0].id != b.channel || witness.directory[1].id != "lc-second" {
			t.Fatal("directory witness", witness.directory)
		}
		order := []mgLCControlOrder{
			{channel: witness.directory[1], config: mgLCControlConfigFor("lc-second", "Second", 1, 0)},
			{channel: witness.directory[0], config: mgLCControlConfigFor(b.channel, "Channel", 1, 1)},
		}
		saved := mgLCControlApply(t, w, proof, MGReorderLCChannels, witness, mgLCControlInput{order: order}, 11)
		if len(saved.result.directory) != 2 || saved.result.directory[0].id != "lc-second" || saved.result.directory[0].position != 0 ||
			saved.result.directory[1].id != b.channel || saved.result.directory[1].position != 1 ||
			saved.result.directory[0].revision != 4 || saved.result.directory[1].revision != 8 {
			t.Fatal("reorder result", saved.result.directory)
		}
		var firstPosition, firstRevision, secondPosition, secondRevision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT position,revision FROM lc_channels WHERE id='lc-second'`).Scan(&firstPosition, &firstRevision); err != nil {
			t.Fatal(err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT position,revision FROM lc_channels WHERE id=?`, b.channel).Scan(&secondPosition, &secondRevision); err != nil {
			t.Fatal(err)
		}
		if firstPosition != 0 || firstRevision != 4 || secondPosition != 1 || secondRevision != 8 {
			t.Fatal("native directory order", firstPosition, firstRevision, secondPosition, secondRevision)
		}
		var config string
		var pendingRevision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT config_revision,config_json FROM lc_generations WHERE id='lc-pending-second'`).Scan(&pendingRevision, &config); err != nil {
			t.Fatal(err)
		}
		if pendingRevision != 4 || config != `{"id":"lc-second"}` {
			t.Fatal("pending build did not follow the reorder", pendingRevision, config)
		}
		var entries, pins int
		if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM lc_entries WHERE generation_id=?), (SELECT count(*) FROM lc_playback_refs WHERE generation_id=?)`, b.base, b.base).Scan(&entries, &pins); err != nil || entries != 1 || pins != 1 {
			t.Fatal("reorder perturbed accepted custody", entries, pins, err)
		}
	})
}

func TestMGLCControlReorderStaleDirectoryRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('lc-second',3,'{"id":"lc-second"}',1,1,'Second','healthy')`); err != nil {
			t.Fatal(err)
		}
		witness := mgLCControlOrderWitness(t, w)
		order := []mgLCControlOrder{
			{channel: witness.directory[1], config: mgLCControlConfigFor("lc-second", "Second", 1, 0)},
			{channel: witness.directory[0], config: mgLCControlConfigFor(b.channel, "Channel", 1, 1)},
		}
		if _, err := w.db.ExecContext(t.Context(), `UPDATE lc_channels SET revision=4 WHERE id='lc-second'`); err != nil {
			t.Fatal(err)
		}
		if err := mgLCControlRefuse(t, w, proof, MGReorderLCChannels, witness, mgLCControlInput{order: order}, 12); !errors.Is(err, ErrConflict) {
			t.Fatal("stale directory reorder", err)
		}
		// An incomplete order list is never a partial reorder.
		if err := mgLCControlRefuse(t, w, proof, MGReorderLCChannels, witness, mgLCControlInput{order: order[:1]}, 18); !errors.Is(err, ErrConflict) {
			t.Fatal("partial directory reorder", err)
		}
		var first, second int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT position FROM lc_channels WHERE id='lc-second'`).Scan(&first); err != nil {
			t.Fatal(err)
		}
		if err := w.db.QueryRowContext(t.Context(), `SELECT position FROM lc_channels WHERE id=?`, b.channel).Scan(&second); err != nil || first != 1 || second != 0 {
			t.Fatal("refused reorder moved positions", first, second, err)
		}
	})
}

func TestMGLCControlLostCommitObservedOnFreshReader(t *testing.T) {
	w, _, proof := mgLCControlFixture(t)
	path := mgGuideDatabasePath(t, w)
	birth := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlChannel{id: "control-lost"}}
	config := mgLCControlConfigFor("control-lost", "Lost", 1, 0)
	in := mgLCControlInput{config: &config}
	command := mgLCControlCommandFor(mgLCControlSaveKind, birth, in, 13)
	turn, budget := mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
	saved, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, birth, command, in)
	if err != nil || decision != receiptFresh {
		t.Fatal(saved, decision, err)
	}
	mgLCCommit(t, turn)
	reader := mgGuideFreshReader(t, path)
	var digest, body string
	if err := reader.QueryRowContext(t.Context(), `SELECT digest,result_json FROM lc_receipts WHERE request_id=?`, mgLCControlRequestKey(command)).Scan(&digest, &body); err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatal("fresh result digest", len(digest))
	}
	raw, err := hex.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMGLCControlResult(raw, command, mgLCControlSaveKind)
	if err != nil || !reflect.DeepEqual(decoded, saved) {
		t.Fatal("fresh receipt decode", decoded, saved, err)
	}
	turn, budget = mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
	before := mgLCControlChanges(t, turn)
	replay, decision, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, birth, command, in)
	if err != nil || decision != receiptReplay || !reflect.DeepEqual(replay, saved) {
		t.Fatal("lost commit replay", replay, decision, err)
	}
	if after := mgLCControlChanges(t, turn); after != before {
		t.Fatal("lost commit replay made effects", before, after)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestMGLCControlPurposeDurabilityAndForgedIssuer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		witness := mgLCControlSaveWitness(t, w, b.channel)
		config := mgLCControlConfigFor(b.channel, "Native normal", 1, 0)
		// A NORMAL native frame that merely claims the FULL purpose is refused.
		turn, budget := mgLCControlAuthorityTurn(t, w, MGConfigureTuner)
		full, err := Describe(MGSaveLCConfiguration)
		if err != nil {
			t.Fatal(err)
		}
		turn.transaction.descriptor = full
		before := mgLCControlChanges(t, turn)
		if _, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, witness,
			mgLCControlCommandFor(mgLCControlSaveKind, witness, mgLCControlInput{config: &config}, 14),
			mgLCControlInput{config: &config}); !errors.Is(err, ErrInvalid) {
			t.Fatal("native NORMAL FULL purpose", err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("refused FULL purpose wrote", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// A forged issuer that does not match the frame witness is refused before
		// the request digest is even considered.
		forged := witness
		forged.issuer[0] ^= 1
		turn, budget = mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
		in := mgLCControlInput{config: &config}
		command := mgLCControlCommandFor(mgLCControlSaveKind, witness, in, 15)
		if _, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, forged, command, in); !errors.Is(err, ErrInvalid) {
			t.Fatal("forged issuer", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// A caller-chosen request digest cannot authorize a fresh effect.
		bogus := mgLCControlCommandFor(mgLCControlSaveKind, witness, mgLCControlInput{config: &config}, 17)
		bogus.request[0] ^= 1
		turn, budget = mgLCControlAuthorityTurn(t, w, MGSaveLCConfiguration)
		before = mgLCControlChanges(t, turn)
		if _, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGSaveLCConfiguration, witness, bogus, mgLCControlInput{config: &config}); !errors.Is(err, ErrConflict) {
			t.Fatal("caller-chosen request digest", err)
		}
		if after := mgLCControlChanges(t, turn); after != before {
			t.Fatal("caller-chosen digest wrote", before, after)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// A control purpose cannot borrow another purpose's input.
		if err := mgLCControlRefuse(t, w, proof, MGRequestLCRegeneration, witness, mgLCControlInput{config: &config}, 16); !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign input", err)
		}
	})
}

func TestMGLCControlBirthRefusesExistingAndAtCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		// An existing channel can never be born again, even from a no-head witness.
		existing := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlChannel{id: b.channel}}
		config := mgLCControlConfigFor(b.channel, "Duplicate", 1, 0)
		if err := mgLCControlRefuse(t, w, proof, MGSaveLCConfiguration, existing, mgLCControlInput{config: &config}, 20); !errors.Is(err, ErrConflict) {
			t.Fatal("birth over an existing channel", err)
		}
		// Fill the directory to the 64-channel bound, then refuse a 65th birth.
		for n := 0; n < 63; n++ {
			id := "cap-" + string(rune('a'+n/26)) + string(rune('a'+n%26))
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES(?,1,'{}',1,?,?,'ready')`, id, n+1, id); err != nil {
				t.Fatal(err)
			}
		}
		overflow := mgLCControlWitness{issuer: mgLCControlIssuer(), channel: mgLCControlChannel{id: "control-overflow"}}
		overflowConfig := mgLCControlConfigFor("control-overflow", "Overflow", 1, 64)
		refusal := &Refusal{}
		if err := mgLCControlRefuse(t, w, proof, MGSaveLCConfiguration, overflow, mgLCControlInput{config: &overflowConfig}, 21); !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
			t.Fatal("birth beyond the directory bound", err)
		}
		var id string
		if err := w.db.QueryRowContext(t.Context(), `SELECT id FROM lc_channels WHERE id='control-overflow'`).Scan(&id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("refused overflow birth wrote", id, err)
		}
	})
}

func TestMGLCControlPendingReturningMismatchRollsBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, b, proof := mgLCControlFixture(t)
		mgLCControlInsertPending(t, w, "lc-pending", b.channel, 7, b.base, "lease-pending")
		if _, err := w.db.ExecContext(t.Context(), `CREATE TRIGGER control_mismatch AFTER UPDATE ON lc_generations BEGIN UPDATE lc_generations SET lease_token='wrong' WHERE id=new.id; END`); err != nil {
			t.Fatal(err)
		}
		witness := mgLCControlSaveWitness(t, w, b.channel)
		kind, _ := mgLCControlKindFor(MGRequestLCRegeneration)
		turn, budget := mgLCControlAuthorityTurn(t, w, MGRequestLCRegeneration)
		in := mgLCControlInput{}
		if _, _, err := mgLCControlDispatch(t.Context(), turn, proof, t.Context(), budget, MGRequestLCRegeneration, witness,
			mgLCControlCommandFor(kind, witness, in, 22), in); !errors.Is(err, ErrConflict) {
			t.Fatal("returning mismatch accepted", err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var status, lease string
		if err := w.db.QueryRowContext(t.Context(), `SELECT status,lease_token FROM lc_generations WHERE id='lc-pending'`).Scan(&status, &lease); err != nil || status != "pending" || lease != "lease-pending" {
			t.Fatal("returning mismatch escaped rollback", status, lease, err)
		}
	})
}

func TestMGLCControlDescriptorInventory(t *testing.T) {
	ds := mgLCControlDescriptors()
	if len(ds) != 4 {
		t.Fatal("control descriptors", len(ds))
	}
	want := []Descriptor{
		{MGSaveLCConfiguration, "mg.lc.configuration", Full},
		{MGRequestLCRegeneration, "mg.lc.regenerate", Normal},
		{MGRetireLCChannel, "mg.lc.retire", Full},
		{MGReorderLCChannels, "mg.lc.reorder", Normal},
	}
	for n, d := range ds {
		if d != want[n] {
			t.Fatal("descriptor", n, d)
		}
		if got, err := Describe(d.ID); err != nil || got != d {
			t.Fatal("registered purpose", d, got, err)
		}
	}
	closed := map[CommandID]Descriptor{}
	for _, d := range mgDomainDescriptors() {
		if _, seen := closed[d.ID]; seen {
			t.Fatal("duplicate domain descriptor", d.ID)
		}
		closed[d.ID] = d
	}
	for _, d := range want {
		if closed[d.ID] != d {
			t.Fatal("domain registry missing control purpose", d)
		}
	}
}
