package playbackv1

import (
	"context"
	"testing"
	"time"
)

func TestProfileRetirementDropsCapturedObserverBeforeAnyLeaseOrStateWrite(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	// Test the exact queued capture rather than accepting another HTTP sample.
	o := f.service.observations()
	o.mu.Lock()
	entry := o.entries[id]
	capture := o.captureLocked(entry)
	o.mu.Unlock()
	var beforeLease, afterLease int64
	var beforeState, afterState string
	if err := f.catalog.DB.QueryRow(`SELECT lease_expires_ms,state FROM playback_v1_sessions WHERE id=?`, id).Scan(&beforeLease, &beforeState); err != nil {
		t.Fatal(err)
	}
	p := f.caller.Principal
	if _, err := f.catalog.DB.Exec(`UPDATE direct_profiles SET deleted=1 WHERE account_id=? AND id=?`, p.AccountID, p.ProfileID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.catalog.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, _, err := f.service.applyObservationTx(context.Background(), tx, o, capture, false)
	if err != nil || !result.retire || !result.consumed {
		t.Fatalf("captured observer survived: %+v %v", result, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = f.catalog.DB.QueryRow(`SELECT lease_expires_ms,state FROM playback_v1_sessions WHERE id=?`, id).Scan(&afterLease, &afterState); err != nil {
		t.Fatal(err)
	}
	if afterLease != beforeLease || afterState != beforeState {
		t.Fatalf("retired observation updated lease/state: %d/%s -> %d/%s", beforeLease, beforeState, afterLease, afterState)
	}
}
