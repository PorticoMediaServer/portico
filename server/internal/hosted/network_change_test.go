package hosted

import (
	"context"
	"testing"
	"time"
)

// seedSchedule writes the control deadlines directly, which is how a test puts a server into
// the state it spends almost all of its life in: a policy in hand and nothing due.
func seedSchedule(t *testing.T, f *claimFixture, heartbeat, renewal time.Time) {
	t.Helper()
	e := f.runner.Do(context.Background(), func(ctx context.Context) error {
		return f.service.saveCurrentSchedule(ctx, f.installed, controlSchedule{NextHeartbeat: heartbeat, NextRenewal: renewal})
	})
	if e != nil {
		t.Fatal(e)
	}
}

// settleMembers puts the membership push into its steady state: Hosted's index
// already holds everything this server journalled (install links the owner).
func settleMembers(t *testing.T, f *claimFixture) {
	t.Helper()
	if _, e := f.db.Exec(`INSERT OR IGNORE INTO account_portico_links(account_id,hosted_account_id) VALUES(?,'account')`, f.owner.Viewer.AccountID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE hosted_membership_sync SET full_pending=0,attempts=0,next_at=0,acked=(SELECT COALESCE(MAX(sequence),0) FROM hosted_membership_journal); DELETE FROM hosted_membership_journal`); e != nil {
		t.Fatal(e)
	}
}

func controlStep(t *testing.T, f *claimFixture) (time.Time, error) {
	t.Helper()
	var at time.Time
	var stepErr error
	if e := f.runner.Do(context.Background(), func(ctx context.Context) error {
		at, stepErr = f.service.currentControlStep(ctx)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return at, stepErr
}

func scheduleAttempts(t *testing.T, f *claimFixture) int {
	t.Helper()
	var q controlSchedule
	e := f.runner.Do(context.Background(), func(ctx context.Context) error {
		var err error
		q, err = f.service.currentSchedule(ctx, f.installed)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	return q.Attempts
}

// The Hosted origin in this fixture resolves to nothing, so a request to Hosted always fails
// and is recorded as an attempt. That makes "did this call Hosted?" a fact rather than a
// guess: attempts stay at zero exactly when no call was made.
//
// A network change used to force a check-in. Every LAN address change, every VPN adapter
// appearing, every gateway renumbering therefore cost a Hosted request carrying a policy
// revision Hosted already had. Nothing Hosted holds changes when the network moves; the routes
// do, and those are the remote manager's business.
func TestANetworkChangeAloneNeverCallsHosted(t *testing.T) {
	f := newClaimFixture(t)
	settleMembers(t, f)
	seedSchedule(t, f, time.Now().Add(7*24*time.Hour), time.Now().Add(30*24*time.Hour))
	// The real loop, because the forcing this removed lived in the loop's own wake handling.
	ctx, cancel := context.WithCancel(context.Background())
	done, steps := make(chan struct{}), make(chan struct{}, 1)
	// Fixture installation may have left a coalesced wake; start from idle.
	select {
	case <-f.service.wake:
	default:
	}
	go func() {
		defer close(done)
		f.service.runCurrentObserved(ctx, func() {
			select {
			case steps <- struct{}{}:
			case <-ctx.Done():
			}
		})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("control loop did not stop")
		}
	}()
	completed := func() {
		t.Helper()
		select {
		case <-steps:
		case <-done:
			t.Fatal("control loop stopped before completing its pass")
		case <-time.After(5 * time.Second):
			t.Fatal("control loop did not complete a signalled pass")
		}
	}
	completed() // The initial pass found nothing due.
	for range 20 {
		f.service.NetworkChanged()
		completed()
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("control loop did not stop")
	}
	if attempts := scheduleAttempts(t, f); attempts != 0 {
		t.Fatal("a network change called Hosted", attempts)
	}
	// And the step itself reports the far deadline, so the loop sleeps rather
	// than polls: a server makes no periodic contact with Hosted besides its
	// check-in.
	at, e := controlStep(t, f)
	if e != nil {
		t.Fatal("a step with nothing due failed", e)
	}
	if at.IsZero() || time.Until(at) < 6*24*time.Hour {
		t.Fatal("the loop did not sleep until the next real deadline", at)
	}
}

// A burst of changes is one look. The signal channel holds one, so twenty changes between two
// passes wake the loop once rather than twenty times.
func TestNetworkChangeBurstsCoalesceIntoOneWake(t *testing.T) {
	f := newClaimFixture(t)
	for range 20 {
		f.service.NetworkChanged()
	}
	woken := 0
	for {
		select {
		case <-f.service.wake:
			woken++
			continue
		default:
		}
		break
	}
	if woken != 1 {
		t.Fatal("a burst of network changes did not coalesce", woken)
	}
}

// The other half of the rule: when something really is due, the call happens, once. A
// check-in whose deadline has passed is attempted on the next pass and — because Hosted is
// unreachable here — recorded as exactly one failed attempt, then held off by the persisted
// retry deadline rather than repeated.
func TestADueCheckInCallsHostedExactlyOnce(t *testing.T) {
	f := newClaimFixture(t)
	settleMembers(t, f)
	seedSchedule(t, f, time.Now().Add(-time.Minute), time.Now().Add(30*24*time.Hour))
	if _, e := controlStep(t, f); e == nil {
		t.Fatal("an unreachable Hosted reported success")
	}
	if attempts := scheduleAttempts(t, f); attempts != 1 {
		t.Fatal("a due check-in was not attempted exactly once", attempts)
	}
	// Further passes, and any number of network changes, wait for the persisted retry
	// deadline instead of hammering a Hosted that is already known to be down.
	for range 5 {
		f.service.NetworkChanged()
		if _, e := controlStep(t, f); e != nil {
			t.Fatal("a step inside the retry window attempted a call", e)
		}
	}
	if attempts := scheduleAttempts(t, f); attempts != 1 {
		t.Fatal("the retry deadline was bypassed", attempts)
	}
}
