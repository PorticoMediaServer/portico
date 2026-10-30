//go:build linux || darwin

package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// Source regressions only. This seeds an ordinary queue with its REAL default
// custody=0 and reproves the exact row in the caller's mandatory Tx checker.
// It does not simulate a transfer grant or qualify the playback factory.
func ordinaryQueueFixture(t *testing.T, f *discFixture) (Operation, func(context.Context, *sql.Tx) error) {
	t.Helper()
	op := f.snapshot.Operation
	op.QueueOwnerKind, op.QueueOwnerID = "device", f.snapshot.Actor.DeviceID
	op.QueueRevision, op.QueueCustodyGeneration = 1, 0
	now := time.Now().UnixMilli()
	_, err := f.db.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,current_entry_id,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		op.QueueID, op.QueueOwnerKind, op.QueueOwnerID, f.snapshot.Actor.Authority, f.snapshot.Actor.AccountID, f.snapshot.Actor.ProfileID, op.QueueRevision, op.QueueEntryID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		var ownerKind, ownerID, authority, account, profile, entry string
		var revision, custody int64
		if err := tx.QueryRowContext(ctx, `SELECT owner_kind,owner_id,authority,account_id,profile_id,current_entry_id,revision,custody_generation FROM queues_v1 WHERE id=?`, op.QueueID).
			Scan(&ownerKind, &ownerID, &authority, &account, &profile, &entry, &revision, &custody); err != nil {
			return err
		}
		if ownerKind != op.QueueOwnerKind || ownerID != op.QueueOwnerID || authority != f.snapshot.Actor.Authority || account != f.snapshot.Actor.AccountID || profile != f.snapshot.Actor.ProfileID || entry != op.QueueEntryID || revision != op.QueueRevision || custody != op.QueueCustodyGeneration {
			return ErrCurrent
		}
		return nil
	}
	return op, check
}

func TestDiscPresentationRetainsOriginalZeroQueueCustody(t *testing.T) {
	f := newDiscFixture(t)
	op, original := ordinaryQueueFixture(t, f)
	snapshot := f.snapshot
	snapshot.Operation = op
	check := func(ctx context.Context, tx *sql.Tx, value Snapshot) error {
		if value.Operation != op {
			return ErrCurrent
		}
		return original(ctx, tx)
	}
	if err := f.publish(snapshot, check); err != nil {
		t.Fatal("ordinary queue custody zero was refused", err)
	}
	key := Key{SessionID: snapshot.SessionID, Generation: snapshot.Generation}
	active, err := f.activate(key, snapshot.ID, check)
	if err != nil || active.Operation != op {
		t.Fatal("activation changed the original custody tuple", err)
	}
	if got := mustInt(t, f.db, `SELECT queue_custody_generation FROM playback_disc_snapshot_operation WHERE snapshot_id=?`, snapshot.ID); got != 0 {
		t.Fatal("stored presentation invented nonzero custody", got)
	}
	if got := mustInt(t, f.db, `SELECT custody_generation FROM queues_v1 WHERE id=?`, op.QueueID); got != 0 {
		t.Fatal("private publication mutated the ordinary queue", got)
	}
	if err = f.exec(`UPDATE queues_v1 SET custody_generation=1 WHERE id=?`, op.QueueID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.resolve(key, snapshot.ID, check); !errors.Is(err, ErrCurrent) {
		t.Fatal("an old custody-zero presentation adopted transferred custody", err)
	}
}

func TestPreparationRetainsOriginalZeroQueueCustody(t *testing.T) {
	f, key, binding := prepFixture(t)
	op, original := ordinaryQueueFixture(t, f)
	binding.Queue = op
	check := func(ctx context.Context, tx *sql.Tx, value Preparation) error {
		if value.Binding.Queue != op {
			return ErrCurrent
		}
		return original(ctx, tx)
	}
	pending, err := f.prepAdmit("ordinary-zero-preparation", key, binding, prepLimits(), check)
	if err != nil || pending.Binding.Queue != op {
		t.Fatal("preparation refused or rewrote custody zero", err)
	}
	if got := mustInt(t, f.db, `SELECT queue_custody_generation FROM playback_start_preparation_queue WHERE operation_id=?`, pending.ID); got != 0 {
		t.Fatal("preparation invented nonzero custody", got)
	}
	if _, err = f.prepFind(key, binding.Actor, binding.StartBodyDigest, check); err != nil {
		t.Fatal("original zero custody did not roundtrip", err)
	}
	if err = f.exec(`UPDATE queues_v1 SET custody_generation=1 WHERE id=?`, op.QueueID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.prepFind(key, binding.Actor, binding.StartBodyDigest, check); !errors.Is(err, ErrCurrent) {
		t.Fatal("preparation adopted a new custody generation", err)
	}
	if got := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparation_results`); got != 0 {
		t.Fatal("custody refusal published a result", got)
	}
}

func TestZeroQueueCustodyDoesNotSupplyMissingQueueProof(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"negative-custody", "entry", "owner-kind", "owner-id", "revision", "missing-queue"} {
		t.Run(change, func(t *testing.T) {
			f, key, binding := prepFixture(t)
			binding.Queue.QueueCustodyGeneration = 0
			switch change {
			case "negative-custody":
				binding.Queue.QueueCustodyGeneration = -1
			case "entry":
				binding.Queue.QueueEntryID = ""
			case "owner-kind":
				binding.Queue.QueueOwnerKind = ""
			case "owner-id":
				binding.Queue.QueueOwnerID = ""
			case "revision":
				binding.Queue.QueueRevision = 0
			case "missing-queue":
				binding.Queue.QueueID = ""
			}
			if _, err := f.prepAdmit("invalid-zero-preparation", key, binding, prepLimits(), prepPlain()); !errors.Is(err, ErrInput) {
				t.Fatal("zero custody supplied missing/invalid queue evidence", err)
			}
			if got := mustInt(t, f.db, `SELECT count(*) FROM playback_start_preparations`); got != 0 {
				t.Fatal("invalid queue tuple published a preparation", got)
			}
		})
	}
}
