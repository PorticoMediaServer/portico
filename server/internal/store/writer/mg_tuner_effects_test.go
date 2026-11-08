package writer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestMGTunerNativeAcquireExactAndRenew(t *testing.T) {
	e, p := mgTunerExecutor(t, mgActorOwner)
	turn, a := mgTunerSession(t, e, p, MGAcquireAllocation)
	r := mgTunerRequestAt(t, e)
	got, err := acquireMGTuner(t.Context(), a, r)
	if err != nil || got != r.allocation {
		t.Fatal(got, err)
	}
	var native int
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `PRAGMA synchronous`).Scan(&native); err != nil || native != int(sqliteconn.Normal) {
		t.Fatal("native NORMAL", native, err)
	}
	if got, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrConflict) || got != (mgTunerAllocation{}) {
		t.Fatal("duplicate resource", got, err)
	}
	mgTunerCommit(t, turn)

	// Renewal is its own purpose on its own frame; the acquired tuple is conserved
	// and only the lease advances.
	renewTurn, renew := mgTunerSession(t, e, p, MGRenewAllocation)
	before := got
	r.allocation.lease += 1000
	if advanced, err := renewMGTuner(t.Context(), renew, before, r); err != nil || advanced != r.allocation {
		t.Fatal(advanced, err)
	}
	if out, err := renewMGTuner(t.Context(), renew, before, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
		t.Fatal("stale before", out, err)
	}
	mgTunerCommit(t, renewTurn)
	var lease int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT lease_until_ms FROM live_allocations WHERE id='alloc'`).Scan(&lease); err != nil || lease != r.allocation.lease {
		t.Fatal("committed lease", lease, err)
	}
}

func TestMGTunerNativeAcquireRefusals(t *testing.T) {
	for _, cell := range []string{"purpose", "guide", "channel", "foreign-account", "authority", "generation", "created", "state", "lease-window", "reservation-forged", "preempt-unbacked", "boundary-short", "cancelled"} {
		t.Run(cell, func(t *testing.T) {
			purpose := MGAcquireAllocation
			if cell == "purpose" {
				purpose = MGRenewAllocation
			}
			e, turn, a := mgTunerFixture(t, purpose, mgActorOwner)
			r := mgTunerRequestAt(t, e)
			ctx := t.Context()
			switch cell {
			case "guide":
				r.guide = "new"
			case "channel":
				r.allocation.channel = "elsewhere"
			case "foreign-account":
				r.allocation.account = "member"
			case "authority":
				r.allocation.authority = "remote"
			case "generation":
				r.allocation.generation = 2
			case "created":
				r.allocation.created--
			case "state":
				r.allocation.state = "preempting"
			case "lease-window":
				r.allocation.lease = r.allocation.created + mgTunerLeaseMS + 1
			case "reservation-forged":
				// A live deadline that no enabled reservation backs is data, not a grant.
				r.allocation.preempt = r.allocation.created + 15000
				r.protectedUntil = r.allocation.preempt
			case "preempt-unbacked":
				// A live deadline must be the same value as the protected boundary.
				r.allocation.preempt = r.allocation.created + 15000
			case "boundary-short":
				r.allocation.preempt = r.allocation.created + 5000
				r.protectedUntil = r.allocation.preempt
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			var changes int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
				t.Fatal(err)
			}
			if out, err := acquireMGTuner(ctx, a, r); err == nil || out != (mgTunerAllocation{}) {
				t.Fatal(out, err)
			}
			var after, rows int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes(),(SELECT count(*) FROM live_allocations)`).Scan(&after, &rows); err != nil {
				t.Fatal(err)
			}
			if rows != 0 {
				t.Fatal("refused acquire inserted", rows)
			}
			if cell != "cancelled" && after != changes {
				t.Fatal("refusal performed SQL effects", changes, after)
			}
		})
	}
}

func TestMGTunerNativeHeldCapacityPrefix(t *testing.T) {
	for _, channels := range [][]string{{"a", "a", "a"}, {"a", "b", "c"}} {
		t.Run(fmt.Sprint(channels), func(t *testing.T) {
			e, p := mgTunerExecutor(t, mgActorOwner)
			distinct := int64(0)
			seen := map[string]bool{}
			for i, ch := range channels {
				if !seen[ch] && distinct < 2 {
					distinct++
				}
				seen[ch] = true
				state := "active"
				if i%2 == 1 {
					state = "preempting"
				}
				if _, err := e.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, mgTunerAllocation{id: fmt.Sprint(i), source: "source", channel: ch, resource: fmt.Sprint(i), kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "token", generation: 1, state: state, lease: 1, created: 0}.values()...); err != nil {
					t.Fatal(err)
				}
			}
			turn, _ := mgTunerSession(t, e, p, MGAcquireAllocation)
			facts, err := readMGTunerCapacity(t.Context(), turn, "source", 2)
			if err != nil || facts.effective != 2 || facts.held != distinct {
				t.Fatal(facts, err)
			}
			// Unknown capacity is never probed.
			if unknown, err := readMGTunerCapacity(t.Context(), turn, "source", 0); err != nil || unknown != (mgTunerCapacity{}) {
				t.Fatal(unknown, err)
			}
			if _, err := readMGTunerCapacity(t.Context(), turn, "source", mgTunerMaxCount+1); !errors.Is(err, ErrInvalid) {
				t.Fatal("oversized capacity", err)
			}
		})
	}
	e, p := mgTunerExecutor(t, mgActorOwner)
	for _, state := range []string{"active", "preempting"} {
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, mgTunerAllocation{id: "legacy-" + state, source: "source", channel: "", resource: "legacy-" + state, kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "token", generation: 1, state: state, lease: 1}.values()...); err != nil {
			t.Fatal(err)
		}
	}
	turn, _ := mgTunerSession(t, e, p, MGAcquireAllocation)
	if facts, err := readMGTunerCapacity(t.Context(), turn, "source", 2); !errors.Is(err, ErrConflict) || facts != (mgTunerCapacity{}) {
		t.Fatal("legacy empty channel key counted as free", facts, err)
	}
}

func TestMGTunerNativeFullSharingAndNoEviction(t *testing.T) {
	for _, cell := range []string{"shared", "shared-expired", "shared-preempt", "shared-preempting", "other-channel", "unknown", "capacity-two"} {
		t.Run(cell, func(t *testing.T) {
			e, p := mgTunerExecutor(t, mgActorOwner)
			now := e.now().UnixMilli()
			r := mgTunerRequest{allocation: mgTunerAllocation{id: "alloc", source: "source", channel: "channel", resource: "resource", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "token", generation: 1, state: "active", lease: now + 10000, created: now}, guide: "old"}
			held := mgTunerAllocation{id: "held", source: "source", channel: "other", resource: "held-resource", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "held-token", generation: 1, state: "active", lease: now + 10000, created: now}
			switch cell {
			case "shared", "shared-expired", "shared-preempt", "shared-preempting":
				held.channel = r.allocation.channel
			}
			switch cell {
			case "shared-expired":
				held.lease = now - 1
			case "shared-preempt":
				held.lease, held.preempt = now+4000, now+5000
			case "shared-preempting":
				held.state = "preempting"
			case "unknown":
				if _, err := e.db.ExecContext(t.Context(), `UPDATE live_sources SET tuner_count=0 WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			case "capacity-two":
				if _, err := e.db.ExecContext(t.Context(), `UPDATE live_sources SET tuner_count=2 WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, held.values()...); err != nil {
				t.Fatal(err)
			}
			_, turn, a := mgTunerFixtureWith(t, e, p)
			got, err := acquireMGTuner(t.Context(), a, r)
			if cell == "shared" || cell == "unknown" || cell == "capacity-two" {
				if err != nil || got != r.allocation {
					t.Fatal(got, err)
				}
				return
			}
			if !errors.Is(err, ErrConflict) || got != (mgTunerAllocation{}) {
				t.Fatal(got, err)
			}
			// No eviction or lease rewrite of an established stream, even in-frame.
			var lease int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT lease_until_ms FROM live_allocations WHERE id='held'`).Scan(&lease); err != nil || lease != held.lease {
				t.Fatal("established stream disturbed", lease, err)
			}
		})
	}
}

func TestMGTunerNativeEffectiveCapacityNotDiscovery(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "discovery-disabled", true: "configured-without-discovery"}[known], func(t *testing.T) {
			e, p := mgTunerExecutor(t, mgActorOwner)
			effective, physical := int64(0), int64(1)
			if known {
				effective, physical = 1, 0
			}
			if _, err := e.db.ExecContext(t.Context(), `UPDATE live_sources SET tuner_count=? WHERE id='source'`, effective); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.ExecContext(t.Context(), `UPDATE live_source_identities SET settings_physical_count=?,settings_owner_limit=1 WHERE id='source'`, physical); err != nil {
				t.Fatal(err)
			}
			now := e.now().UnixMilli()
			if _, err := e.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, mgTunerAllocation{id: "held", source: "source", channel: "other", resource: "held-resource", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "held-token", generation: 1, state: "active", lease: now + 10000, created: now}.values()...); err != nil {
				t.Fatal(err)
			}
			_, turn, a := mgTunerFixtureWith(t, e, p)
			r := mgTunerRequest{allocation: mgTunerAllocation{id: "alloc", source: "source", channel: "channel", resource: "resource", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "token", generation: 1, state: "active", lease: now + 10000, created: now}, guide: "old"}
			out, err := acquireMGTuner(t.Context(), a, r)
			if known {
				if !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
					t.Fatal("configured effective capacity ignored", out, err)
				}
			} else if err != nil || out != r.allocation {
				t.Fatal("discovery metadata invented a disabled hard cap", out, err)
			}
			_ = turn
		})
	}
}

func TestMGTunerNativeReservationWindow(t *testing.T) {
	e, p := mgTunerExecutor(t, mgActorOwner)
	// Two tuners so the reservation boundary, not capacity, is the only gate.
	if _, err := e.db.ExecContext(t.Context(), `UPDATE live_sources SET tuner_count=2 WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	_, turn, a := mgTunerFixtureWith(t, e, p)
	r := mgTunerRequestAt(t, e)
	boundary := r.allocation.created + 15000
	insert := func(enabled, start, end int64) {
		t.Helper()
		if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES(?,?,?,?,?,?)`, fmt.Sprint(start), "source", start, end, 0, enabled); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, boundary, boundary+60000)
	r.allocation.preempt, r.protectedUntil = boundary, boundary
	got, err := acquireMGTuner(t.Context(), a, r)
	if err != nil || got != r.allocation || got.preempt != boundary {
		t.Fatal(got, err)
	}
	// A forged boundary must not mint a live deadline.
	r.allocation.id, r.allocation.resource = "alloc2", "resource2"
	r.allocation.preempt, r.protectedUntil = boundary+1, boundary+1
	if out, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
		t.Fatal("forged live boundary", out, err)
	}
	// A disabled reservation is no longer a protected boundary.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_reservations SET enabled=0 WHERE id=?`, fmt.Sprint(boundary)); err != nil {
		t.Fatal(err)
	}
	r.allocation.id, r.allocation.resource = "alloc3", "resource3"
	r.allocation.preempt, r.protectedUntil = boundary, boundary
	if out, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
		t.Fatal("disabled reservation boundary", out, err)
	}
	// A recording allocation never carries a reservation boundary.
	rec := mgTunerRequestAt(t, e)
	rec.allocation.kind, rec.allocation.id, rec.allocation.resource = "recording", "recording", "recording-resource"
	rec.allocation.preempt, rec.protectedUntil = boundary, boundary
	if out, err := acquireMGTuner(t.Context(), a, rec); !errors.Is(err, ErrInvalid) || out != (mgTunerAllocation{}) {
		t.Fatal("recording reservation boundary", out, err)
	}
}

func TestMGTunerNativeConcurrentSameResource(t *testing.T) {
	e, p := mgTunerExecutor(t, mgActorOwner)
	turn, a := mgTunerSession(t, e, p, MGAcquireAllocation)
	r := mgTunerRequestAt(t, e)
	if got, err := acquireMGTuner(t.Context(), a, r); err != nil || got != r.allocation {
		t.Fatal(got, err)
	}
	mgTunerCommit(t, turn)

	// The loser of a race sees the winning resource, not an empty slot.
	_, _, retryA := mgTunerFixtureWith(t, e, p)
	r.allocation.id, r.allocation.token = "alloc2", "token2"
	if out, err := acquireMGTuner(t.Context(), retryA, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
		t.Fatal("concurrent duplicate resource", out, err)
	}
	// A second viewer on the same physical channel shares the held tuner.
	share := r
	share.allocation.id, share.allocation.resource, share.allocation.token = "alloc3", "resource3", "token3"
	if out, err := acquireMGTuner(t.Context(), retryA, share); err != nil || out != share.allocation {
		t.Fatal("shared physical channel", out, err)
	}
}

func TestMGTunerNativeWholeRollback(t *testing.T) {
	e, turn, a := mgTunerFixture(t, MGAcquireAllocation, mgActorOwner)
	r := mgTunerRequestAt(t, e)
	// Force the independent readback to miss after the INSERT has succeeded.
	step := turn.steps[mgTunerRead]
	delete(turn.steps, mgTunerRead)
	if got, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrInvalid) || got != (mgTunerAllocation{}) {
		t.Fatal(got, err)
	}
	turn.steps[mgTunerRead] = step
	var n int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM live_allocations`).Scan(&n); err != nil || n != 1 {
		t.Fatal("insert prefix missing", n, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_allocations`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed readback escaped rollback", n, err)
	}
}

func TestMGTunerNativeRenewCAS(t *testing.T) {
	for _, cell := range []string{"current", "token", "generation", "expired", "released", "channel", "guide", "revoked", "not-advanced", "too-long", "foreign-account"} {
		t.Run(cell, func(t *testing.T) {
			e, p := mgTunerExecutor(t, mgActorOwner)
			turn, a := mgTunerSession(t, e, p, MGAcquireAllocation)
			r := mgTunerRequestAt(t, e)
			if got, err := acquireMGTuner(t.Context(), a, r); err != nil || got != r.allocation {
				t.Fatal(got, err)
			}
			mgTunerCommit(t, turn)

			renewTurn, renew := mgTunerSession(t, e, p, MGRenewAllocation)
			before := r.allocation
			want := before
			want.lease = before.lease + 1000
			switch cell {
			case "token", "generation", "foreign-account", "channel":
				// Forged native tuple facts are compared against the stored row.
				switch cell {
				case "token":
					before.token, want.token = "other", "other"
				case "generation":
					before.generation, want.generation = 2, 2
				case "foreign-account":
					before.account, want.account = "member", "member"
				case "channel":
					before.channel, want.channel = "elsewhere", "elsewhere"
				}
				want.lease = before.lease + 1000
			case "expired":
				if _, err := renewTurn.transaction.conn.ExecContext(t.Context(), `UPDATE live_allocations SET lease_until_ms=? WHERE id='alloc'`, before.created); err != nil {
					t.Fatal(err)
				}
				before.lease, want.lease = before.created, before.created+1000
			case "released":
				if _, err := renewTurn.transaction.conn.ExecContext(t.Context(), `UPDATE live_allocations SET state='released' WHERE id='alloc'`); err != nil {
					t.Fatal(err)
				}
				before.state, want.state = "released", "released"
			case "guide":
				if _, err := renewTurn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET active_generation='new' WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := renewTurn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1 WHERE id='issuer-family'`); err != nil {
					t.Fatal(err)
				}
			case "not-advanced":
				want.lease = before.lease
			case "too-long":
				want.lease = renewTurn.transaction.writer.now().UnixMilli() + mgTunerLeaseMS + 1
			}
			request := r
			request.allocation = want
			out, err := renewMGTuner(t.Context(), renew, before, request)
			if cell == "current" {
				if err != nil || out != want {
					t.Fatal(out, err)
				}
				return
			}
			// A moved source head makes the acquisition witness itself stale, so
			// the frame refuses before the native tuple is compared.
			denied := cell == "revoked" || cell == "foreign-account" || cell == "guide"
			if denied {
				if !errors.Is(err, ErrDisclosureDenied) || out != (mgTunerAllocation{}) {
					t.Fatal("denied renewal", out, err)
				}
				return
			}
			if !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
				t.Fatal("stale renewal accepted", out, err)
			}
		})
	}
}

func TestMGTunerNativeForbiddenViewer(t *testing.T) {
	for _, kind := range []mgActorKind{mgActorViewer, mgActorAdminViewer} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			e, p := mgTunerExecutor(t, kind)
			if _, err := e.db.ExecContext(t.Context(), `UPDATE live_source_identities SET settings_viewer_access='owner-only'`); err != nil {
				t.Fatal(err)
			}
			turn, a, err := mgTunerSessionWith(t, e, p, MGAcquireAllocation, "owner-only")
			if a != nil || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("owner-only source acquired by viewer", a, err)
			}
			if turn == nil {
				t.Fatal("frame missing")
			}
			r := mgTunerRequestAt(t, e)
			if out, err := acquireMGTuner(t.Context(), nil, r); !errors.Is(err, ErrDisclosureDenied) || out != (mgTunerAllocation{}) {
				t.Fatal("missing acquisition", out, err)
			}
		})
	}
}
