//go:build linux || darwin

package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func derivedFixture(t *testing.T) *discFixture {
	t.Helper()
	f := newDiscFixture(t)
	f.snapshot.Actor.Authority = "local"
	// The real original session expires before the authority horizon. A child
	// must not gain time by inheriting only the larger actor horizon later.
	if err := f.exec(`UPDATE playback_sessions SET expires_at=? WHERE id=?`, time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339Nano), discSessionID); err != nil {
		t.Fatal(err)
	}
	if err := f.publish(f.snapshot, plainCheck()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(f.snapshot.key(), f.snapshot.ID, plainCheck()); err != nil {
		t.Fatal(err)
	}
	seedDiscFamilyContinuation(t, f, f.snapshot, "derived-current-token")
	return f
}

func TestDiscDerivedGenerationRetainsExactInputAndOriginalExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := derivedFixture(t)
	parent := f.snapshot
	childKey, childID := Key{discSessionID, 2}, "derived-child"
	expires := mustInt(t, f.db, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=?`, parent.ID)
	want, err := freeze(parent)
	if err != nil {
		t.Fatal(err)
	}
	want.ID, want.Generation = childID, childKey.Generation
	var got Snapshot
	err = f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		claim, err := f.store.ClaimDerivedGenerationTx(ctx, tx, parent.key(), parent.ID, childKey, childID, plainCheck())
		if err != nil {
			return err
		}
		copy := *claim
		if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET generation=2,expires_at=? WHERE id=? AND generation=1`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), discSessionID); err != nil {
			return err
		}
		// The ordinary new-input path stays strict after same-family renewal.
		if err = f.store.PublishPendingTx(ctx, tx, want, plainCheck()); !errors.Is(err, ErrCurrent) {
			t.Fatalf("normal pending publication adopted a renewed token: %v", err)
		}
		if err = f.store.PublishDerivedGenerationTx(ctx, tx, claim, plainCheck()); err != nil {
			return err
		}
		if err = f.store.PublishDerivedGenerationTx(ctx, tx, &copy, plainCheck()); !errors.Is(err, ErrCurrent) {
			t.Fatalf("copied claim published twice: %v", err)
		}
		if _, err = f.store.ActivateTx(ctx, tx, childKey, childID, plainCheck()); !errors.Is(err, ErrCurrent) {
			t.Fatalf("normal pending activation adopted a renewed token: %v", err)
		}
		got, err = f.store.ActivateDerivedGenerationTx(ctx, tx, claim, plainCheck())
		if err != nil {
			return err
		}
		if _, err = f.store.ActivateDerivedGenerationTx(ctx, tx, &copy, plainCheck()); !errors.Is(err, ErrCurrent) {
			t.Fatalf("copied claim activated twice: %v", err)
		}
		return f.store.RetireTx(ctx, tx, parent.key(), parent.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("derived presentation changed original measurement, actor or source")
	}
	if until := mustInt(t, f.db, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=?`, childID); until != expires {
		t.Fatal("generation transition extended original presentation expiry", until, expires)
	}
	if _, err := f.resolve(childKey, childID, plainCheck()); err != nil {
		t.Fatal("active derived child cannot resolve independently", err)
	}
}

func TestDiscDerivedClaimRequiresOriginalActiveParentAndExactWriter(t *testing.T) {
	f := derivedFixture(t)
	parent := f.snapshot
	refused := errors.New("original parent proof refused")
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		for _, check := range []Check{nil, func(context.Context, *sql.Tx, Snapshot) error { return refused }} {
			if _, err := f.store.ClaimDerivedGenerationTx(ctx, tx, parent.key(), parent.ID, Key{discSessionID, 2}, "child", check); err == nil {
				t.Fatal("missing/refusing original proof issued a claim")
			}
		}
		for _, key := range []Key{{"different-session", 2}, {discSessionID, 1}, {discSessionID, 3}} {
			if _, err := f.store.ClaimDerivedGenerationTx(ctx, tx, parent.key(), parent.ID, key, "child", plainCheck()); err == nil {
				t.Fatal("claim selected a different session or generation", key)
			}
		}
		claim, err := f.store.ClaimDerivedGenerationTx(ctx, tx, parent.key(), parent.ID, Key{discSessionID, 2}, "child", plainCheck())
		if err != nil {
			return err
		}
		other, err := New(discServerID, nil)
		if err != nil {
			return err
		}
		if err = other.PublishDerivedGenerationTx(ctx, tx, claim, plainCheck()); !errors.Is(err, ErrInput) {
			t.Fatal("another store borrowed the claim", err)
		}
		if err = f.store.PublishDerivedGenerationTx(ctx, tx, &DerivedGeneration{}, plainCheck()); !errors.Is(err, ErrInput) {
			t.Fatal("empty caller-constructed claim admitted", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshots WHERE generation=2`); n != 0 {
		t.Fatal("claim alone published a child", n)
	}
	// A claim from a committed writer is not reusable in a later transaction.
	var old *DerivedGeneration
	if err = f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		old, err = f.store.ClaimDerivedGenerationTx(ctx, tx, parent.key(), parent.ID, Key{discSessionID, 2}, "later-child", plainCheck())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		if err := f.store.PublishDerivedGenerationTx(ctx, tx, old, plainCheck()); !errors.Is(err, ErrInput) {
			t.Fatal("a successor transaction borrowed the claim", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDiscDerivedPublicationRepeatsOriginalAndCurrentProof(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, name := range []string{"original refused", "current missing", "current refused", "retired parent", "hash changed", "family revoked", "object changed", "before generation", "original context cancelled"} {
		t.Run(name, func(t *testing.T) {
			f := derivedFixture(t)
			parent := f.snapshot
			err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
				original, cancel := context.WithCancel(ctx)
				defer cancel()
				live := true
				checkParent := func(context.Context, *sql.Tx, Snapshot) error {
					if !live {
						return ErrCurrent
					}
					return nil
				}
				claim, err := f.store.ClaimDerivedGenerationTx(original, tx, parent.key(), parent.ID, Key{discSessionID, 2}, "child", checkParent)
				if err != nil {
					return err
				}
				if name != "before generation" {
					if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET generation=2 WHERE id=?`, discSessionID); err != nil {
						return err
					}
				}
				current := plainCheck()
				switch name {
				case "original refused":
					live = false
				case "current missing":
					current = nil
				case "current refused":
					current = func(context.Context, *sql.Tx, Snapshot) error { return ErrCurrent }
				case "retired parent":
					err = f.store.RetireTx(ctx, tx, parent.key(), parent.ID)
				case "hash changed":
					_, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET session_hash='another-token' WHERE id=?`, discSessionID)
				case "family revoked":
					_, err = tx.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id=?`, parent.Actor.FamilyID)
				case "object changed":
					_, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET revision='changed' WHERE id=?`, discObjectID)
				case "original context cancelled":
					cancel()
				}
				if err != nil {
					return err
				}
				if err = f.store.PublishDerivedGenerationTx(ctx, tx, claim, current); err == nil {
					t.Fatal("retired or incomplete generation transition admitted", name)
				}
				var n int
				if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_disc_snapshots WHERE generation=2`).Scan(&n); err != nil || n != 0 {
					t.Fatal("refused publication left a child", n, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDiscDerivedActivationRefusalRollsBackOriginalGenerationTransition(t *testing.T) {
	f := derivedFixture(t)
	parent := f.snapshot
	refused := errors.New("current accepting child proof refused")
	err := f.inWrite(dbwork.ClassPlaybackStart, func(ctx context.Context, tx *sql.Tx) error {
		claim, err := f.store.ClaimDerivedGenerationTx(ctx, tx, parent.key(), parent.ID, Key{discSessionID, 2}, "child", plainCheck())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET generation=2 WHERE id=?`, discSessionID); err != nil {
			return err
		}
		if err = f.store.PublishDerivedGenerationTx(ctx, tx, claim, plainCheck()); err != nil {
			return err
		}
		_, err = f.store.ActivateDerivedGenerationTx(ctx, tx, claim, func(context.Context, *sql.Tx, Snapshot) error { return refused })
		return err
	})
	if !errors.Is(err, refused) {
		t.Fatal("activation lost the actual current refusal", err)
	}
	if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_snapshots WHERE generation=2`); n != 0 {
		t.Fatal("refused transition retained child publication", n)
	}
	if generation := mustInt(t, f.db, `SELECT generation FROM playback_sessions WHERE id=?`, discSessionID); generation != 1 {
		t.Fatal("refused transition changed the actual legacy generation", generation)
	}
	if _, err := f.resolve(parent.key(), parent.ID, plainCheck()); err != nil {
		t.Fatal("refused child retired its original parent", err)
	}
}
