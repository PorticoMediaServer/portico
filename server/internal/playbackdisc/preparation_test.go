//go:build linux || darwin

package playbackdisc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Regression SOURCES only for the Root67 original-Play preparation. These reuse
// the real temporary persistence database and the sealed disc fixture in
// store_test.go; nothing here is a production claim, renewed preparation or
// byte/session grant.
func prepPlain() PreparationCheck {
	return func(context.Context, *sql.Tx, Preparation) error { return nil }
}

func prepContinuationPlain() PreparationContinuationCheck {
	return func(context.Context, *sql.Tx, Preparation) error { return nil }
}

func prepAcceptedPlain() AcceptedPreparationCheck {
	return func(context.Context, *sql.Tx, Preparation, PreparationAccepted) error { return nil }
}

func prepLimits() PreparationLimits { return PreparationLimits{Retained: 16, Active: 4, PerDevice: 2} }

// prepActor makes the original actor's session hash a real digest. validActor
// accepts free text, but a preparation additionally requires the original hash
// digest the caller proved; the fixture's playback session is updated to match
// before a snapshot is published.
func prepActor(a Actor) Actor {
	a.SessionHash = strings.Repeat("f", 64)
	return a
}

func prepBinding(f *discFixture, actor Actor, source Source, queue Operation, itemID string) PreparationBinding {
	return PreparationBinding{
		Actor: actor, StartBodyDigest: queue.StartBodyDigest, ItemID: itemID, AssetID: source.AssetID,
		PartIndex: 0, Source: source, Queue: queue,
		OriginalTokenExpiresMs: f.until, DeadlineMs: time.Now().Add(time.Minute).UnixMilli(),
	}
}

// prepFixture is one valid original binding that needs no published snapshot:
// admission and claim vectors never resolve a session or disc result.
func prepFixture(t *testing.T) (*discFixture, PreparationKey, PreparationBinding) {
	t.Helper()
	f := newDiscFixture(t)
	b := prepBinding(f, prepActor(f.snapshot.Actor), f.snapshot.Source, f.snapshot.Operation, f.publicPID)
	key := PreparationKey{DeviceID: b.Actor.DeviceID, StartKeyDigest: b.Queue.StartKeyDigest}
	return f, key, b
}

func (f *discFixture) prepAdmit(id string, key PreparationKey, binding PreparationBinding, limits PreparationLimits, check PreparationCheck) (Preparation, error) {
	f.t.Helper()
	var out Preparation
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.AdmitPreparationTx(ctx, tx, id, key, binding, limits, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepFind(key PreparationKey, actor Actor, body string, check PreparationCheck) (Preparation, error) {
	f.t.Helper()
	var out Preparation
	err := f.inReadTx(func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.FindPreparationTx(ctx, tx, key, actor, body, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepClaim(pending PreparationClaim, execution string, check PreparationCheck) (PreparationClaim, error) {
	f.t.Helper()
	var out PreparationClaim
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.ClaimPreparationTx(ctx, tx, pending, execution, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepCheck(claim PreparationClaim, check PreparationCheck) (Preparation, error) {
	f.t.Helper()
	var out Preparation
	err := f.inReadTx(func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.CheckPreparationClaimTx(ctx, tx, claim, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepMark(claim PreparationClaim, check PreparationCheck) (PreparationClaim, error) {
	f.t.Helper()
	var out PreparationClaim
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.MarkPreparationPreparedTx(ctx, tx, claim, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepCheckPrepared(claim PreparationClaim, check PreparationCheck) (Preparation, error) {
	f.t.Helper()
	var out Preparation
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.CheckPreparedClaimTx(ctx, tx, claim, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepAccept(claim PreparationClaim, out PreparationAccepted, worker PreparationCheck, continuation PreparationContinuationCheck) error {
	f.t.Helper()
	return f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		return f.store.AcceptPreparationTx(ctx, tx, claim, out, worker, continuation)
	})
}

func (f *discFixture) prepCheckContinuation(claim PreparationClaim, worker PreparationCheck, continuation PreparationContinuationCheck) (Preparation, error) {
	f.t.Helper()
	var out Preparation
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.CheckPreparedContinuationTx(ctx, tx, claim, worker, continuation)
		return e
	})
	return out, err
}

func (f *discFixture) prepResolve(id string, expected PreparationAccepted, check AcceptedPreparationCheck) (Preparation, error) {
	f.t.Helper()
	var out Preparation
	err := f.inReadTx(func(ctx context.Context, tx *sql.Tx) (e error) {
		out, e = f.store.ResolveAcceptedPreparationTx(ctx, tx, id, expected, check)
		return e
	})
	return out, err
}

func (f *discFixture) prepRetire(claim PreparationClaim, terminal PreparationTerminal) error {
	f.t.Helper()
	return f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		return f.store.RetirePreparationTx(ctx, tx, claim, terminal)
	})
}

func (f *discFixture) prepUnaccepted(after string, limit int) ([]PreparationClaim, string, error) {
	f.t.Helper()
	var claims []PreparationClaim
	var next string
	err := f.inReadTx(func(ctx context.Context, tx *sql.Tx) (e error) {
		claims, next, e = f.store.UnacceptedPreparationClaimsTx(ctx, tx, after, limit)
		return e
	})
	return claims, next, err
}

func (f *discFixture) inReadTx(fn func(context.Context, *sql.Tx) error) error {
	f.t.Helper()
	ctx := context.Background()
	tx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return fn(ctx, tx)
}

func prepDeviceBinding(base PreparationBinding, device, keyDigest string) (PreparationKey, PreparationBinding) {
	b := base
	b.Actor.DeviceID = device
	b.Queue.StartKeyDigest = keyDigest
	return PreparationKey{DeviceID: device, StartKeyDigest: keyDigest}, b
}

func prepNoRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM playback_start_preparations`,
		`SELECT count(*) FROM playback_start_preparation_lifecycle`,
		`SELECT count(*) FROM playback_start_preparation_actor`,
		`SELECT count(*) FROM playback_start_preparation_source`,
		`SELECT count(*) FROM playback_start_preparation_queue`,
		`SELECT count(*) FROM playback_start_preparation_results`,
	} {
		if n := mustInt(t, db, query); n != 0 {
			t.Fatalf("%s = %d after a refused preparation write, want 0", query, n)
		}
	}
}

// prepPreparedSetup returns a fully prepared claim over the real accepted disc
// graph. The legacy v1 session is inserted only in the positive acceptance test.
func prepPreparedSetup(t *testing.T) (*discFixture, PreparationKey, PreparationBinding, PreparationClaim, string) {
	t.Helper()
	f := newDiscFixture(t)
	rawKey := "preparation-raw-start-key"
	sum := sha256.Sum256([]byte(rawKey))
	snap := f.snapshot
	snap.Actor = prepActor(snap.Actor)
	snap.Operation.StartKeyDigest = hex.EncodeToString(sum[:])
	if err := f.exec(`UPDATE playback_sessions SET session_hash=? WHERE id=?`, snap.Actor.SessionHash, discSessionID); err != nil {
		t.Fatalf("bind fixture session hash: %v", err)
	}
	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatalf("publish disc candidate: %v", err)
	}
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); err != nil {
		t.Fatalf("activate disc candidate: %v", err)
	}
	binding := prepBinding(f, snap.Actor, snap.Source, snap.Operation, f.publicPID)
	key := PreparationKey{DeviceID: binding.Actor.DeviceID, StartKeyDigest: binding.Queue.StartKeyDigest}
	pending, err := f.prepAdmit("prep-op-1", key, binding, prepLimits(), prepPlain())
	if err != nil {
		t.Fatalf("admit preparation: %v", err)
	}
	claim, err := f.prepClaim(pending.Claim, "prep-execution-1", prepPlain())
	if err != nil {
		t.Fatalf("claim preparation: %v", err)
	}
	claim, err = f.prepMark(claim, prepPlain())
	if err != nil {
		t.Fatalf("mark prepared: %v", err)
	}
	return f, key, binding, claim, rawKey
}

// TestPreparationAdmitRefusesUngatedWriters keeps admission behind the real
// gated writer, with a mandatory original check and finite resource limits.
func TestPreparationAdmitRefusesUngatedWriters(t *testing.T) {
	f, key, binding := prepFixture(t)

	if _, err := New("", nil); !errors.Is(err, ErrInput) {
		t.Fatalf("New with no server id = %v, want ErrInput", err)
	}
	var absent *Store
	if _, err := absent.AdmitPreparationTx(context.Background(), nil, "prep-op", key, binding, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("nil store = %v, want ErrInput", err)
	}
	if _, err := f.store.AdmitPreparationTx(nil, nil, "prep-op", key, binding, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("nil context = %v, want ErrInput", err)
	}
	if _, err := f.store.AdmitPreparationTx(context.Background(), nil, "prep-op", key, binding, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("nil transaction = %v, want ErrInput", err)
	}

	untracked, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin untracked: %v", err)
	}
	if _, err = f.store.AdmitPreparationTx(context.Background(), untracked, "prep-op", key, binding, prepLimits(), prepPlain()); err == nil {
		t.Fatal("an untracked transaction was accepted")
	}
	_ = untracked.Rollback()

	readonly, err := f.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read-only: %v", err)
	}
	if _, err = f.store.AdmitPreparationTx(context.Background(), readonly, "prep-op", key, binding, prepLimits(), prepPlain()); err == nil {
		_ = readonly.Rollback()
		t.Fatal("a read-only transaction was accepted")
	}
	_ = readonly.Rollback()

	if _, err = f.prepAdmit("prep-op", key, binding, prepLimits(), nil); !errors.Is(err, ErrInput) {
		t.Fatalf("admit with nil check = %v, want ErrInput", err)
	}
	for name, limits := range map[string]PreparationLimits{
		"no retained":    {Retained: 0, Active: 1, PerDevice: 1},
		"active above":   {Retained: 4, Active: 5, PerDevice: 1},
		"device above":   {Retained: 8, Active: 4, PerDevice: 5},
		"retained above": {Retained: 70000, Active: 1, PerDevice: 1},
	} {
		if _, err = f.prepAdmit("prep-op", key, binding, limits, prepPlain()); !errors.Is(err, ErrInput) {
			t.Fatalf("admit with %s = %v, want ErrInput", name, err)
		}
	}
	if _, err = f.prepAdmit("", key, binding, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("admit with no operation id = %v, want ErrInput", err)
	}
	prepNoRows(t, f.db)

	// A check that refuses leaves no root, lifecycle or normalized witness row.
	refused := errors.New("original preparation proof refused")
	if _, err = f.prepAdmit("prep-op", key, binding, prepLimits(), func(context.Context, *sql.Tx, Preparation) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("admit with refusing check = %v, want %v", err, refused)
	}
	prepNoRows(t, f.db)
}

// TestPreparationOriginalProofMismatchRefused covers the original actor, hash,
// token generation, body and source key: each mismatch refuses before rows.
func TestPreparationOriginalProofMismatchRefused(t *testing.T) {
	f, key, binding := prepFixture(t)
	want := binding
	verify := func(_ context.Context, _ *sql.Tx, v Preparation) error {
		if v.Binding.Actor != want.Actor || v.Key != key || v.Binding.Source != want.Source || v.Binding.StartBodyDigest != want.StartBodyDigest {
			return ErrCurrent
		}
		return nil
	}
	admitted, err := f.prepAdmit("prep-op-1", key, binding, prepLimits(), verify)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if admitted.State != "pending" || admitted.Claim.Revision != 1 {
		t.Fatalf("admitted = %#v, want pending revision 1", admitted)
	}

	wrongActor := binding.Actor
	wrongActor.TokenGeneration++
	if _, err = f.prepFind(key, wrongActor, binding.StartBodyDigest, prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("find with another actor = %v, want ErrCurrent", err)
	}
	if _, err = f.prepFind(key, binding.Actor, strings.Repeat("a", 64), prepPlain()); !errors.Is(err, ErrConflict) {
		t.Fatalf("find with another body = %v, want ErrConflict", err)
	}
	if _, err = f.prepFind(key, binding.Actor, binding.StartBodyDigest, func(context.Context, *sql.Tx, Preparation) error { return ErrCurrent }); !errors.Is(err, ErrCurrent) {
		t.Fatalf("find with a refusing original check = %v, want ErrCurrent", err)
	}

	// Each of these passes value validation but contradicts the original proof
	// the caller's check repeats.
	for name, mutate := range map[string]func() (PreparationKey, PreparationBinding, PreparationCheck){
		"token generation": func() (PreparationKey, PreparationBinding, PreparationCheck) {
			b := binding
			b.Actor.TokenGeneration++
			return key, b, verify
		},
		"start key hash": func() (PreparationKey, PreparationBinding, PreparationCheck) {
			k, b := key, binding
			k.StartKeyDigest = strings.Repeat("b", 64)
			b.Queue.StartKeyDigest = k.StartKeyDigest
			return k, b, verify
		},
		"source key": func() (PreparationKey, PreparationBinding, PreparationCheck) {
			b := binding
			b.Source.InventoryObjectID = "object-other"
			return key, b, verify
		},
		"body": func() (PreparationKey, PreparationBinding, PreparationCheck) {
			b := binding
			b.StartBodyDigest = strings.Repeat("c", 64)
			b.Queue.StartBodyDigest = b.StartBodyDigest
			return key, b, verify
		},
	} {
		k, b, check := mutate()
		if _, err = f.prepAdmit("prep-op-mismatch", k, b, prepLimits(), check); err == nil {
			t.Fatalf("%s mismatch was admitted", name)
		}
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparations`); n != 1 {
		t.Fatalf("preparations = %d, want only the original", n)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_actor`); n != 1 {
		t.Fatalf("actor rows = %d, want only the original", n)
	}
}

// TestPreparationDeadlineCannotBeRenewed bounds the wait by the original token,
// the original horizon and the 120 s maximum, and keeps the stored deadline.
func TestPreparationDeadlineCannotBeRenewed(t *testing.T) {
	f, key, binding := prepFixture(t)
	now := time.Now()

	tooLong := binding
	tooLong.DeadlineMs = now.Add(MaxPreparationLifetime + time.Second).UnixMilli()
	if _, err := f.prepAdmit("prep-op", key, tooLong, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("deadline beyond 120 s = %v, want ErrInput", err)
	}
	pastToken := binding
	pastToken.OriginalTokenExpiresMs = now.Add(30 * time.Second).UnixMilli()
	pastToken.DeadlineMs = now.Add(time.Minute).UnixMilli()
	if _, err := f.prepAdmit("prep-op", key, pastToken, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("deadline beyond the original token = %v, want ErrInput", err)
	}
	pastHorizon := binding
	pastHorizon.Actor.OriginalHorizonMs = now.Add(30 * time.Second).UnixMilli()
	pastHorizon.DeadlineMs = now.Add(time.Minute).UnixMilli()
	if _, err := f.prepAdmit("prep-op", key, pastHorizon, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("deadline beyond the original horizon = %v, want ErrInput", err)
	}
	prepNoRows(t, f.db)

	pending, err := f.prepAdmit("prep-op", key, binding, prepLimits(), prepPlain())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	stored := mustInt(t, f.db, `SELECT deadline_ms FROM playback_start_preparations WHERE id=?`, pending.ID)
	claim, err := f.prepClaim(pending.Claim, "prep-execution", prepPlain())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err = f.prepMark(claim, prepPlain()); err != nil {
		t.Fatalf("mark prepared: %v", err)
	}
	if got := mustInt(t, f.db, `SELECT deadline_ms FROM playback_start_preparations WHERE id=?`, pending.ID); got != stored {
		t.Fatalf("deadline changed from %d to %d during preparation", stored, got)
	}
	duplicate := binding
	duplicate.ItemID = f.publicPID
	if _, err = f.prepAdmit("prep-op-duplicate", key, duplicate, prepLimits(), prepPlain()); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate key = %v, want ErrConflict", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparations`); n != 1 {
		t.Fatalf("preparations = %d, want 1", n)
	}
}

// TestPreparationClaimIsSingleAndRevisionsAreCAS keeps one original pending
// claim, one process execution and exact revision compare-and-swap at each step.
func TestPreparationClaimIsSingleAndRevisionsAreCAS(t *testing.T) {
	f, key, binding := prepFixture(t)
	pending, err := f.prepAdmit("prep-op", key, binding, prepLimits(), prepPlain())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if _, err = f.prepClaim(PreparationClaim{OperationID: pending.ID, Revision: 2}, "prep-execution", prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("claim with a stale pending revision = %v, want ErrCurrent", err)
	}
	if _, err = f.prepClaim(PreparationClaim{OperationID: pending.ID, ExecutionID: "given", Revision: 1}, "prep-execution", prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("claim with a caller execution id = %v, want ErrInput", err)
	}
	claimed, err := f.prepClaim(pending.Claim, "prep-execution", prepPlain())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.Revision != 2 || claimed.ExecutionID != "prep-execution" {
		t.Fatalf("claim = %#v, want revision 2 with the issued execution", claimed)
	}
	if _, err = f.prepClaim(pending.Claim, "prep-execution", prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("second claim of a pending row = %v, want ErrCurrent", err)
	}
	if _, err = f.prepClaim(claimed, "prep-execution", prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("claim of a claimed row = %v, want ErrInput", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_lifecycle WHERE execution_id='prep-execution'`); n != 1 {
		t.Fatalf("execution rows = %d, want exactly 1", n)
	}

	if _, err = f.prepCheck(pending.Claim, prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("check with the no-execution pending claim = %v, want ErrInput", err)
	}
	if _, err = f.prepCheck(claimed, prepPlain()); err != nil {
		t.Fatalf("check with the live claim: %v", err)
	}
	prepared, err := f.prepMark(claimed, prepPlain())
	if err != nil {
		t.Fatalf("mark prepared: %v", err)
	}
	if prepared.Revision != 3 || prepared.ExecutionID != claimed.ExecutionID {
		t.Fatalf("prepared claim = %#v, want revision 3 with the same execution", prepared)
	}
	if _, err = f.prepMark(claimed, prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("mark with the superseded claim = %v, want ErrCurrent", err)
	}
	if _, err = f.prepCheckPrepared(claimed, prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("prepared check with the superseded claim = %v, want ErrCurrent", err)
	}
	if _, err = f.prepCheckPrepared(prepared, prepPlain()); err != nil {
		t.Fatalf("prepared check with the live claim: %v", err)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, pending.ID); state != "prepared" {
		t.Fatalf("state = %q, want prepared", state)
	}
	if err = f.prepAccept(prepared, PreparationAccepted{SessionID: "missing-session", SnapshotID: "missing-snapshot", Legacy: Key{SessionID: "legacy", Generation: 1}}, prepPlain(), prepContinuationPlain()); err == nil {
		t.Fatal("accept without any real result graph was accepted")
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, pending.ID); state != "prepared" {
		t.Fatalf("state after refused accept = %q, want prepared", state)
	}
}

// prepSeedLegacySession inserts the real legacy continuation row the store and
// result trigger both require: its key hash, body, device, actor, media binding,
// item and queue are the original values; for part 0 the v1 version must be the
// exact chosen asset.
func prepSeedLegacySession(t *testing.T, f *discFixture, binding PreparationBinding, out PreparationAccepted, rawKey, version string, part int) {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := f.exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,item_id,version_id,part_index,queue_id,entry_id,request,revision,generation,media_session_id,media_generation,presentation,location,lease_expires_ms,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		out.SessionID, binding.Actor.DeviceID, binding.Actor.AccountID, binding.Actor.ProfileID, binding.Actor.Authority,
		rawKey, binding.StartBodyDigest, "vod", "local", "playing", 7, version, part, binding.Queue.QueueID, binding.Queue.QueueEntryID,
		`{"requestId":"request-test"}`, 1, 1, out.Legacy.SessionID, out.Legacy.Generation,
		`{"decision":{"video":{"action":"copy"},"audio":{"action":"copy"},"subtitles":{"action":"none"}}}`, "local", now+3_600_000, now, now); err != nil {
		t.Fatalf("seed legacy v1 session: %v", err)
	}
}

// TestPreparationAcceptanceRequiresExactRealGraph proves the positive path only
// over the real accepted disc snapshot and a real legacy v1 session whose key,
// body, device, actor, media binding, item, part and queue are the originals.
func TestPreparationAcceptanceRequiresExactRealGraph(t *testing.T) {
	f, _, binding, claim, rawKey := prepPreparedSetup(t)
	snapshotID := f.snapshot.ID
	legacy := Key{SessionID: f.snapshot.SessionID, Generation: f.snapshot.Generation}
	out := PreparationAccepted{SessionID: "v1-session-test", SnapshotID: snapshotID, Legacy: legacy}
	prepSeedLegacySession(t, f, binding, out, rawKey, binding.AssetID, 0)

	refused := errors.New("original prepared measurement refused")
	if err := f.prepAccept(claim, out, func(context.Context, *sql.Tx, Preparation) error { return refused }, prepContinuationPlain()); !errors.Is(err, refused) {
		t.Fatalf("accept with a refusing prepared check = %v, want %v", err, refused)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_results`); n != 0 {
		t.Fatalf("results = %d after a refused accept, want 0", n)
	}
	fake := out
	fake.SessionID = "v1-session-missing"
	if err := f.prepAccept(claim, fake, prepPlain(), prepContinuationPlain()); err == nil {
		t.Fatal("accept with a nonexistent v1 session was accepted")
	}
	fake = out
	fake.SnapshotID = "snapshot-missing"
	if err := f.prepAccept(claim, fake, prepPlain(), prepContinuationPlain()); err == nil {
		t.Fatal("accept with a nonexistent disc snapshot was accepted")
	}
	fake = out
	fake.Legacy = Key{SessionID: legacy.SessionID, Generation: legacy.Generation + 1}
	if err := f.prepAccept(claim, fake, prepPlain(), prepContinuationPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("accept with a fake legacy generation = %v, want ErrCurrent", err)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, claim.OperationID); state != "prepared" {
		t.Fatalf("state after refused accepts = %q, want prepared", state)
	}

	if err := f.prepAccept(claim, out, prepPlain(), prepContinuationPlain()); err != nil {
		t.Fatalf("accept exact real result: %v", err)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, claim.OperationID); state != "accepted" {
		t.Fatalf("state = %q, want accepted", state)
	}
	var session, legacySession, snapshot string
	var generation int64
	if err := f.db.QueryRow(`SELECT session_id,legacy_session_id,legacy_generation,snapshot_id FROM playback_start_preparation_results WHERE operation_id=?`, claim.OperationID).Scan(&session, &legacySession, &generation, &snapshot); err != nil {
		t.Fatalf("read accepted result: %v", err)
	}
	if session != out.SessionID || legacySession != legacy.SessionID || generation != int64(legacy.Generation) || snapshot != snapshotID {
		t.Fatalf("stored result = (%q,%q,%d,%q), want the exact accepted values", session, legacySession, generation, snapshot)
	}
	accepted, err := f.prepResolve(claim.OperationID, out, prepAcceptedPlain())
	if err != nil {
		t.Fatalf("resolve accepted: %v", err)
	}
	if accepted.State != "accepted" || accepted.Accepted != out {
		t.Fatalf("resolved = %#v, want the committed accepted result", accepted)
	}
	wrong := out
	wrong.Legacy.Generation++
	if _, err = f.prepResolve(claim.OperationID, wrong, prepAcceptedPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("resolve with another expected result = %v, want ErrCurrent", err)
	}
	if err = f.prepAccept(claim, out, prepPlain(), prepContinuationPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("second accept = %v, want ErrCurrent", err)
	}
}

// TestPreparationChangedOriginalCheckCancelsPublication proves a changed
// original source measurement refuses publication and keeps rows terminal
// idempotency tombstones rather than reviving them.
func TestPreparationChangedOriginalCheckCancelsPublication(t *testing.T) {
	f, _, _, claim, _ := prepPreparedSetup(t)
	changed := errors.New("original source changed")
	if err := f.prepAccept(claim, PreparationAccepted{SessionID: "v1", SnapshotID: f.snapshot.ID, Legacy: Key{SessionID: f.snapshot.SessionID, Generation: f.snapshot.Generation}},
		func(context.Context, *sql.Tx, Preparation) error { return changed }, prepContinuationPlain()); !errors.Is(err, changed) {
		t.Fatalf("accept after source change = %v, want %v", err, changed)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, claim.OperationID); state != "prepared" {
		t.Fatalf("state after cancelled publication = %q, want prepared", state)
	}
	if err := f.prepRetire(claim, PreparationFailed); err != nil {
		t.Fatalf("retire failed: %v", err)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, claim.OperationID); state != "failed" {
		t.Fatalf("state = %q, want failed", state)
	}
	if _, err := f.prepClaim(claim, "prep-execution-2", prepPlain()); !errors.Is(err, ErrInput) {
		t.Fatalf("claim after terminal = %v, want ErrInput", err)
	}
	if _, err := f.prepMark(claim, prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("mark after terminal = %v, want ErrCurrent", err)
	}
	if err := f.prepAccept(claim, PreparationAccepted{SessionID: "v1", SnapshotID: f.snapshot.ID, Legacy: Key{SessionID: f.snapshot.SessionID, Generation: f.snapshot.Generation}}, prepPlain(), prepContinuationPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("accept after terminal = %v, want ErrCurrent", err)
	}
	if err := f.prepRetire(claim, PreparationRetired); !errors.Is(err, ErrCurrent) {
		t.Fatalf("second retire = %v, want ErrCurrent", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparations`); n != 1 {
		t.Fatalf("preparations = %d, want the one tombstone", n)
	}
}

// TestPreparationContinuationIsSeparateFromWorkerProof requires the live admitted
// original POST continuation in addition to the worker's retained proof; neither
// a nil, refusing nor retired continuation can accept a session.
func TestPreparationContinuationIsSeparateFromWorkerProof(t *testing.T) {
	f, _, _, claim, _ := prepPreparedSetup(t)
	out := PreparationAccepted{SessionID: "v1-session-test", SnapshotID: f.snapshot.ID, Legacy: Key{SessionID: f.snapshot.SessionID, Generation: f.snapshot.Generation}}
	retired := errors.New("admitted original POST retired")
	workerRefused := errors.New("worker retained proof refused")

	if _, err := f.prepCheckContinuation(claim, prepPlain(), nil); !errors.Is(err, ErrInput) {
		t.Fatalf("prepared continuation with nil check = %v, want ErrInput", err)
	}
	if _, err := f.prepCheckContinuation(claim, prepPlain(), func(context.Context, *sql.Tx, Preparation) error { return retired }); !errors.Is(err, retired) {
		t.Fatalf("prepared continuation with a retired POST = %v, want %v", err, retired)
	}
	prepared, err := f.prepCheckContinuation(claim, prepPlain(), prepContinuationPlain())
	if err != nil {
		t.Fatalf("prepared continuation with both proofs: %v", err)
	}
	if prepared.State != "prepared" || prepared.Claim != claim {
		t.Fatalf("prepared continuation = %#v, want the live prepared claim", prepared)
	}

	if err = f.prepAccept(claim, out, prepPlain(), nil); !errors.Is(err, ErrInput) {
		t.Fatalf("accept with nil continuation = %v, want ErrInput", err)
	}
	if err = f.prepAccept(claim, out, prepPlain(), func(context.Context, *sql.Tx, Preparation) error { return ErrCurrent }); !errors.Is(err, ErrCurrent) {
		t.Fatalf("accept with a retired continuation = %v, want ErrCurrent", err)
	}
	if err = f.prepAccept(claim, out, prepPlain(), func(context.Context, *sql.Tx, Preparation) error { return retired }); !errors.Is(err, retired) {
		t.Fatalf("accept with a refusing continuation = %v, want %v", err, retired)
	}
	if err = f.prepAccept(claim, out, func(context.Context, *sql.Tx, Preparation) error { return workerRefused }, prepContinuationPlain()); !errors.Is(err, workerRefused) {
		t.Fatalf("accept with only a refusing worker proof = %v, want %v", err, workerRefused)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, claim.OperationID); state != "prepared" {
		t.Fatalf("state after refused continuations = %q, want prepared", state)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_results`); n != 0 {
		t.Fatalf("results = %d after refused continuations, want 0", n)
	}
}

// TestPreparationAcceptanceRequiresCurrentLegacyAndChosenAsset keeps the exact
// current legacy generation/asset/actor/request join and, for part 0, the v1
// version equal to the chosen asset and the original part index.
func TestPreparationAcceptanceRequiresCurrentLegacyAndChosenAsset(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, f *discFixture, binding PreparationBinding, out PreparationAccepted, rawKey string)
	}{
		{"legacy request drift", func(t *testing.T, f *discFixture, binding PreparationBinding, out PreparationAccepted, rawKey string) {
			prepSeedLegacySession(t, f, binding, out, rawKey, binding.AssetID, 0)
			if err := f.exec(`UPDATE playback_sessions SET request_id='request-disc-other' WHERE id=?`, discSessionID); err != nil {
				t.Fatalf("drift legacy request: %v", err)
			}
		}},
		{"v1 version drift", func(t *testing.T, f *discFixture, binding PreparationBinding, out PreparationAccepted, rawKey string) {
			prepSeedLegacySession(t, f, binding, out, rawKey, "asset-other", 0)
		}},
		{"v1 part drift", func(t *testing.T, f *discFixture, binding PreparationBinding, out PreparationAccepted, rawKey string) {
			prepSeedLegacySession(t, f, binding, out, rawKey, binding.AssetID, 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _, binding, claim, rawKey := prepPreparedSetup(t)
			out := PreparationAccepted{SessionID: "v1-session-test", SnapshotID: f.snapshot.ID, Legacy: Key{SessionID: f.snapshot.SessionID, Generation: f.snapshot.Generation}}
			tc.prepare(t, f, binding, out, rawKey)
			if err := f.prepAccept(claim, out, prepPlain(), prepContinuationPlain()); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, claim.OperationID); state != "prepared" {
				t.Fatalf("state after %s = %q, want prepared", tc.name, state)
			}
			if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_results`); n != 0 {
				t.Fatalf("results after %s = %d, want 0", tc.name, n)
			}
		})
	}
}

// TestPreparationTerminalRetiredDoesNotRevive keeps a retired original also a
// tombstone: no claim, no acceptance and no deletion of its witnesses.
func TestPreparationTerminalRetiredDoesNotRevive(t *testing.T) {
	f, key, binding := prepFixture(t)
	pending, err := f.prepAdmit("prep-op", key, binding, prepLimits(), prepPlain())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err = f.prepRetire(pending.Claim, PreparationRetired); err != nil {
		t.Fatalf("retire pending: %v", err)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_start_preparation_lifecycle WHERE operation_id=?`, pending.ID); state != "retired" {
		t.Fatalf("state = %q, want retired", state)
	}
	if _, err = f.prepClaim(pending.Claim, "prep-execution", prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("claim after retire = %v, want ErrCurrent", err)
	}
	if _, err = f.prepFind(key, binding.Actor, binding.StartBodyDigest, prepPlain()); !errors.Is(err, ErrCurrent) {
		t.Fatalf("find after retire = %v, want ErrCurrent", err)
	}
	if err = f.prepRetire(pending.Claim, PreparationRetired); !errors.Is(err, ErrCurrent) {
		t.Fatalf("second retire = %v, want ErrCurrent", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_actor WHERE operation_id=?`, pending.ID); n != 1 {
		t.Fatalf("actor witness rows = %d, want the retained original", n)
	}
}

// TestPreparationRestartEnumerationIsBoundedCleanupOnly returns only this
// server's unaccepted claims, ordered and paginated, never accepted or terminal.
func TestPreparationRestartEnumerationIsBoundedCleanupOnly(t *testing.T) {
	f, key, binding := prepFixture(t)
	limits := prepLimits()

	pending, err := f.prepAdmit("prep-op-a", key, binding, limits, prepPlain())
	if err != nil {
		t.Fatalf("admit pending: %v", err)
	}
	keyB, bindingB := prepDeviceBinding(binding, "device-b", strings.Repeat("1", 64))
	prepB, err := f.prepAdmit("prep-op-b", keyB, bindingB, limits, prepPlain())
	if err != nil {
		t.Fatalf("admit preparing: %v", err)
	}
	claimB, err := f.prepClaim(prepB.Claim, "prep-execution-b", prepPlain())
	if err != nil {
		t.Fatalf("claim preparing: %v", err)
	}
	keyC, bindingC := prepDeviceBinding(binding, "device-c", strings.Repeat("2", 64))
	prepC, err := f.prepAdmit("prep-op-c", keyC, bindingC, limits, prepPlain())
	if err != nil {
		t.Fatalf("admit prepared: %v", err)
	}
	preparedClaim, err := f.prepClaim(prepC.Claim, "prep-execution-c", prepPlain())
	if err != nil {
		t.Fatalf("claim prepared: %v", err)
	}
	if preparedClaim, err = f.prepMark(preparedClaim, prepPlain()); err != nil {
		t.Fatalf("mark prepared: %v", err)
	}
	keyD, bindingD := prepDeviceBinding(binding, "device-d", strings.Repeat("3", 64))
	failed, err := f.prepAdmit("prep-op-d", keyD, bindingD, limits, prepPlain())
	if err != nil {
		t.Fatalf("admit failed: %v", err)
	}
	if err = f.prepRetire(failed.Claim, PreparationFailed); err != nil {
		t.Fatalf("retire failed: %v", err)
	}
	keyE, bindingE := prepDeviceBinding(binding, "device-e", strings.Repeat("4", 64))
	retired, err := f.prepAdmit("prep-op-e", keyE, bindingE, limits, prepPlain())
	if err != nil {
		t.Fatalf("admit retired: %v", err)
	}
	if err = f.prepRetire(retired.Claim, PreparationRetired); err != nil {
		t.Fatalf("retire pending: %v", err)
	}
	// A row from another server is never enumerated here.
	other, err := New("other-server", nil)
	if err != nil {
		t.Fatalf("other store: %v", err)
	}
	keyOther, otherBinding := prepDeviceBinding(binding, "device-other", strings.Repeat("5", 64))
	otherBinding.Actor.ServerID = "other-server"
	if err = f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		_, e := other.AdmitPreparationTx(ctx, tx, "prep-op-other", keyOther, otherBinding, limits, prepPlain())
		return e
	}); err != nil {
		t.Fatalf("admit other server: %v", err)
	}

	claims, next, err := f.prepUnaccepted("", 2)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(claims) != 2 || next != claims[1].OperationID {
		t.Fatalf("first page = (%d claims, next %q), want 2 and the last id", len(claims), next)
	}
	if claims[0].OperationID != pending.ID || claims[1].OperationID != prepB.ID {
		t.Fatalf("first page = %#v, want the two oldest unaccepted claims", claims)
	}
	if claims[1] != claimB {
		t.Fatalf("enumerated claim = %#v, want the exact execution and revision", claims[1])
	}
	rest, next, err := f.prepUnaccepted(next, 4)
	if err != nil {
		t.Fatalf("enumerate page two: %v", err)
	}
	if len(rest) != 1 || next != "" || rest[0] != preparedClaim {
		t.Fatalf("second page = (%#v, next %q), want only the prepared claim", rest, next)
	}
	for _, claim := range append(claims, rest...) {
		if claim.OperationID == failed.ID || claim.OperationID == retired.ID || claim.OperationID == "prep-op-other" {
			t.Fatalf("enumerated a terminal or another server's row: %#v", claim)
		}
	}
	if _, _, err = f.prepUnaccepted("", 0); !errors.Is(err, ErrInput) {
		t.Fatalf("limit 0 = %v, want ErrInput", err)
	}
	if _, _, err = f.prepUnaccepted("", 65); !errors.Is(err, ErrInput) {
		t.Fatalf("limit above 64 = %v, want ErrInput", err)
	}
	if _, _, err = f.prepUnaccepted("bad\tafter", 4); !errors.Is(err, ErrInput) {
		t.Fatalf("malformed cursor = %v, want ErrInput", err)
	}
}
