package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

// One already-native row with one field perturbed proves each exact-equality
// guard by value, not by an error-only fault.
type mgTunerRetireMismatchConn struct {
	writerDriverConn
	returning bool
	readAt    int
	reads     int
	hit       *bool
}

func (c *mgTunerRetireMismatchConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err != nil {
		return rows, err
	}
	corrupt := c.returning && strings.HasPrefix(q, `UPDATE live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 SET state='released'`)
	if strings.Contains(q, mgTunerReadSQLMarker) {
		c.reads++
		corrupt = corrupt || c.readAt != 0 && c.reads == c.readAt
	}
	if corrupt {
		return &mgTunerRetireMismatchRows{Rows: rows, hit: c.hit}, nil
	}
	return rows, nil
}

type mgTunerRetireMismatchRows struct {
	driver.Rows
	hit *bool
}

func (r *mgTunerRetireMismatchRows) Next(values []driver.Value) error {
	if err := r.Rows.Next(values); err != nil {
		return err
	}
	// state is the eleventh returned column; every read is a full tuple.
	values[10] = "preempting"
	*r.hit = true
	return nil
}

func TestMGTunerRetireNativeValueMismatch(t *testing.T) {
	for _, cell := range []string{"returning", "before-read", "after-read"} {
		t.Run(cell, func(t *testing.T) {
			hit := new(bool)
			mismatch := &mgTunerRetireMismatchConn{hit: hit}
			e, _ := testCanonicalExecutor(t, func(c driver.Conn) driver.Conn {
				mismatch.writerDriverConn = c.(writerDriverConn)
				return mismatch
			})
			now := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			e.now = func() time.Time { return now }
			p := &mgActorProof{native: *usOwnerAuthorityPopulation(t, e, now), kind: mgActorOwner}
			for _, q := range []string{mgSourceFixtureSQL, mgTunerFixtureSQL} {
				if _, err := e.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			s, o, key := mgTunerRetirePhysical(t, e, "mg-media", "mg-execution")
			a := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
			mgTunerInsertCommitted(t, e, a)
			mismatch.returning = cell == "returning"
			switch cell {
			case "before-read":
				mismatch.readAt = 1
			case "after-read":
				mismatch.readAt = 2
			}
			turn, m, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if out, err := retireMGTuner(t.Context(), m, s, mgTunerRetireRequest{allocation: a, key: key, observation: o}); !errors.Is(err, ErrConflict) || out != (mgTunerRetireResult{}) {
				t.Fatal("mismatched native value released", out, err)
			}
			if !*hit {
				t.Fatal("control missed the retire readback")
			}
			mgTunerRetireTurnDone(t, turn)
			if state := mgTunerNativeRow(t, e, "alloc").state; state != "active" {
				t.Fatal("mismatched retire escaped rollback", state)
			}
		})
	}
}

func mgTunerReconcileEmpty(r mgTunerReconcileResult) bool {
	return len(r.debt) == 0 && r.cursorChannel == "" && r.cursorID == "" && !r.more && r.expired == 0 && r.applied == 0
}

func mgTunerRetireTurnDone(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil && !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestMGTunerRetireExactNativeReleaseAndReplay(t *testing.T) {
	e, path, p := mgTunerRetireExecutor(t, mgActorOwner)
	s, o, key := mgTunerRetirePhysical(t, e, "mg-media", "mg-execution")
	a := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
	mgTunerInsertCommitted(t, e, a)

	turn, m, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := retireMGTuner(t.Context(), m, s, mgTunerRetireRequest{allocation: a, key: key, observation: o})
	want := a
	want.state = "released"
	if err != nil || got.allocation != want || got.release == nil {
		t.Fatal(got, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	// The bridge refund is withheld until the exact turn is physically committed.
	if err := releaseMPPhysicalAfterCommit(turn, got.release); !errors.Is(err, ErrConflict) {
		t.Fatal("pre-commit refund", err)
	}
	if e2, err := s.start(mpPhysicalRoot{key.media, "mg-early", key.generation}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil); e2 != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("second physical claimant before commit", e2, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err := releaseMPPhysicalAfterCommit(turn, got.release); err != nil {
		t.Fatal(err)
	}
	if state := mgTunerFreshState(t, path); state != "released" {
		t.Fatal("committed state", state)
	}
	successor, err := s.start(mpPhysicalRoot{key.media, "mg-successor", key.generation}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal("capacity did not return after committed exit", err)
	}
	if _, err := successor.join(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Retired replay: the same released tuple is the saved original, no effects
	// and no second physical refund.
	replayTurn, replay, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := mgTunerTurnChanges(t, replayTurn)
	out, err := retireMGTuner(t.Context(), replay, s, mgTunerRetireRequest{allocation: want, key: key})
	if err != nil || out.allocation != want || out.release != nil {
		t.Fatal(out, err)
	}
	if after := mgTunerTurnChanges(t, replayTurn); after != before {
		t.Fatal("replay performed SQL effects", before, after)
	}
	// A stale held tuple never relaunches the retired row and never refunds it.
	if _, err := retireMGTuner(t.Context(), replay, s, mgTunerRetireRequest{allocation: a, key: key, observation: o}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale held tuple re-retired", err)
	}
	mgTunerRetireTurnDone(t, replayTurn)
	if state := mgTunerNativeRow(t, e, "alloc").state; state != "released" {
		t.Fatal("stale refusal changed the row", state)
	}
}

func TestMGTunerRetireRefusals(t *testing.T) {
	for _, cell := range []string{"foreign-account", "foreign-source", "revoked", "cancelled-context", "cancelled-lifetime", "wrong-purpose", "full-durability", "drifted-lease", "released-stale", "stale-observation", "stale-revision"} {
		t.Run(cell, func(t *testing.T) {
			e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
			s, o, key := mgTunerRetirePhysical(t, e, "mg-media", "mg-execution")
			orig := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
			a := orig
			mgTunerInsertCommitted(t, e, a)
			if cell == "drifted-lease" {
				if _, err := e.db.ExecContext(t.Context(), `UPDATE live_allocations SET lease_until_ms=lease_until_ms+1 WHERE id='alloc'`); err != nil {
					t.Fatal(err)
				}
				// A refusal must restore this already-drifted native row, not the
				// pre-drift fixture tuple.
				orig.lease++
			}
			if cell == "released-stale" {
				if _, err := e.db.ExecContext(t.Context(), `UPDATE live_allocations SET state='released' WHERE id='alloc'`); err != nil {
					t.Fatal(err)
				}
			}
			if cell == "stale-revision" {
				if _, err := e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_revision=3 WHERE id='mg-media'`); err != nil {
					t.Fatal(err)
				}
			}
			var life context.Context = t.Context()
			var cancel context.CancelFunc
			if cell == "cancelled-lifetime" {
				life, cancel = context.WithCancel(t.Context())
			}
			purpose := MGTunerRetireAllocation
			if cell == "wrong-purpose" {
				purpose = MGTunerReconcileAllocations
			}
			turn, m, err := mgTunerRetireSession(t, e, p, purpose, "source", life)
			if err != nil {
				t.Fatal(err)
			}
			if cell == "revoked" {
				if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1 WHERE id='issuer-family'`); err != nil {
					t.Fatal(err)
				}
			}
			if cell == "foreign-account" {
				a.account = "member"
			}
			if cell == "foreign-source" {
				a.source = "elsewhere"
			}
			if cell == "stale-observation" {
				// An observation for an older execution cannot retire the row that
				// now carries a different captured token.
				key.execution = "mg-old"
			}
			if cell == "full-durability" {
				turn.transaction.descriptor.Durability = Full
			}
			if cell == "cancelled-lifetime" {
				cancel()
			}
			ctx := t.Context()
			if cell == "cancelled-context" {
				cancelled, c := context.WithCancel(t.Context())
				c()
				ctx = cancelled
			}
			out, err := retireMGTuner(ctx, m, s, mgTunerRetireRequest{allocation: a, key: key, observation: o})
			if err == nil || out != (mgTunerRetireResult{}) {
				t.Fatal("refused retire released a result", out, err)
			}
			mgTunerRetireTurnDone(t, turn)
			native := mgTunerNativeRow(t, e, "alloc")
			if cell == "released-stale" {
				if native.state != "released" {
					t.Fatal("refusal changed the native row", native)
				}
				return
			}
			if native != orig {
				t.Fatal("refusal escaped rollback", native)
			}
		})
	}
}

// A lease deadline is not an exit. The physical owner is still running, so no
// observation exists, capacity stays held and a second claimant is refused.
func TestMGTunerRetireRequiresJoinedPhysicalExit(t *testing.T) {
	e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
	owner := &Writer{effective: &effectiveRuntime{executor: e}}
	s, err := newMPPhysicalSupervisor(owner, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	cmd, ready, release := mpPhysicalBlockedCommand(t)
	probe, err := s.start(mpPhysicalRoot{"mg-media", "mg-execution", 2}, &mpPhysicalTestSupervisor{}, cmd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mpPhysicalAwaitFile(t, ready)
	// The lease is already expired, yet the process still holds the resource.
	a := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
	a.lease = a.created - 1
	mgTunerInsertCommitted(t, e, a)
	key := mpPhysicalKey{media: "mg-media", execution: "mg-execution", request: "mg-media-stop", generation: 2, revision: 2}
	turn, m, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before := mgTunerTurnChanges(t, turn)
	if out, err := retireMGTuner(t.Context(), m, s, mgTunerRetireRequest{allocation: a, key: key}); !errors.Is(err, ErrDisclosureDenied) || out != (mgTunerRetireResult{}) {
		t.Fatal("expired lease retired a live process", out, err)
	}
	if after := mgTunerTurnChanges(t, turn); after != before {
		t.Fatal("refusal performed SQL effects", before, after)
	}
	if e2, err := s.start(mpPhysicalRoot{"mg-media", "mg-second", 2}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil); e2 != nil || !errors.Is(err, ErrConflict) {
		t.Fatal("second physical claimant admitted", e2, err)
	}
	mgTunerRetireTurnDone(t, turn)
	if state := mgTunerNativeRow(t, e, "alloc").state; state != "active" {
		t.Fatal("held debt released", state)
	}
	release()
	if _, err := probe.join(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A joined process is still not an exit: the native stop/exit fields must prove
// it before a release token can be sealed. The trusted bridge refuses the
// unproven release as a denied disclosure; the update prefix rolls back.
func TestMGTunerRetireRequiresNativeExitFields(t *testing.T) {
	e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
	s, o, key := mgTunerRetirePhysical(t, e, "mg-media", "mg-execution")
	a := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
	mgTunerInsertCommitted(t, e, a)
	if _, err := e.db.ExecContext(t.Context(), `UPDATE playback_sessions SET mp_exited_generation=0,mp_exit_request_id='',mp_exit_supervisor_incarnation=NULL,mp_exited_ms=0,mp_exit_digest='',mp_cleanup_complete=0 WHERE id='mg-media'`); err != nil {
		t.Fatal(err)
	}
	turn, m, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := retireMGTuner(t.Context(), m, s, mgTunerRetireRequest{allocation: a, key: key, observation: o}); !errors.Is(err, ErrDisclosureDenied) || out != (mgTunerRetireResult{}) {
		t.Fatal("unproven native exit released", out, err)
	}
	mgTunerRetireTurnDone(t, turn)
	if state := mgTunerNativeRow(t, e, "alloc").state; state != "active" {
		t.Fatal("unproven exit escaped rollback", state)
	}
}

// A removed source keeps its retained allocation debt retirable: the captured
// owner and the joined physical exit are the authority, not source freshness.
func TestMGTunerRetireAfterSourceRemoval(t *testing.T) {
	e, path, p := mgTunerRetireExecutor(t, mgActorOwner)
	s, o, key := mgTunerRetirePhysical(t, e, "mg-media", "mg-execution")
	a := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
	mgTunerInsertCommitted(t, e, a)
	if _, err := e.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='gone',removal_revision=8,removal_removed_at='now' WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	turn, m, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := a
	want.state = "released"
	got, err := retireMGTuner(t.Context(), m, s, mgTunerRetireRequest{allocation: a, key: key, observation: o})
	if err != nil || got.allocation != want || got.release == nil {
		t.Fatal(got, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err := releaseMPPhysicalAfterCommit(turn, got.release); err != nil {
		t.Fatal(err)
	}
	if state := mgTunerFreshState(t, path); state != "released" {
		t.Fatal("removed source debt not retired", state)
	}
}

// An uncertain COMMIT is never success. The durable row is observed through a
// fresh read-only connection, and the physical refund stays withheld.
func TestMGTunerRetireUnknownCommitNeverRefunds(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			fault := &mgTunerCommitFault{after: after, hit: new(bool)}
			e, path := testCanonicalExecutor(t, func(c driver.Conn) driver.Conn {
				fault.writerDriverConn = c.(writerDriverConn)
				return fault
			})
			now := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			e.now = func() time.Time { return now }
			p := &mgActorProof{native: *usOwnerAuthorityPopulation(t, e, now), kind: mgActorOwner}
			for _, q := range []string{mgSourceFixtureSQL, mgTunerFixtureSQL} {
				if _, err := e.db.ExecContext(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}
			s, o, key := mgTunerRetirePhysical(t, e, "mg-media", "mg-execution")
			a := mgTunerRetireAllocationAt(e, "mg-media", "mg-execution")
			mgTunerInsertCommitted(t, e, a)
			turn, m, err := mgTunerRetireSession(t, e, p, MGTunerRetireAllocation, "source", t.Context())
			if err != nil {
				t.Fatal(err)
			}
			out, err := retireMGTuner(t.Context(), m, s, mgTunerRetireRequest{allocation: a, key: key, observation: o})
			if err != nil || out.release == nil {
				t.Fatal(out, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.commit(); !errors.Is(err, ErrResolveRequired) || !*fault.hit {
				t.Fatal("lost COMMIT reported success", err, *fault.hit)
			}
			if err = releaseMPPhysicalAfterCommit(turn, out.release); !errors.Is(err, ErrConflict) {
				t.Fatal("uncertain commit refunded", err)
			}
			if e2, err := s.start(mpPhysicalRoot{key.media, "mg-claimant", key.generation}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil); e2 != nil || !errors.Is(err, ErrConflict) {
				t.Fatal("uncertain commit freed the physical slot", e2, err)
			}
			want := "active"
			if after {
				want = "released"
			}
			if state := mgTunerFreshState(t, path); state != want {
				t.Fatal("fresh durable state", state, want)
			}
		})
	}
}

type mgTunerCommitFault struct {
	writerDriverConn
	after bool
	hit   *bool
}

var errMGTunerLostCommit = errors.New("mg tuner test: lost COMMIT reply")

func (c *mgTunerCommitFault) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if q == "COMMIT" {
		*c.hit = true
		if c.after {
			res, err := c.writerDriverConn.ExecContext(ctx, q, args)
			return res, errors.Join(err, errMGTunerLostCommit)
		}
		return nil, errMGTunerLostCommit
	}
	return c.writerDriverConn.ExecContext(ctx, q, args)
}

func mgTunerReconcileRows(t *testing.T, e *executor) []mgTunerAllocation {
	t.Helper()
	now := e.now().UnixMilli()
	rows := []mgTunerAllocation{
		{id: "a1", source: "source", channel: "c1", resource: "r1", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "t1", generation: 1, state: "active", lease: now + 1000, created: now},
		{id: "a2", source: "source", channel: "c1", resource: "r2", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "t2", generation: 1, state: "preempting", lease: now - 1, created: now},
		{id: "a3", source: "source", channel: "c2", resource: "r3", kind: "recording", authority: "local", account: "issuer", profile: "issuer-profile", token: "t3", generation: 1, state: "active", lease: now - 5, created: now},
	}
	for _, a := range rows {
		mgTunerInsertCommitted(t, e, a)
	}
	return rows
}

func TestMGTunerReconcileBoundedDebtAndCursor(t *testing.T) {
	e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
	mgTunerReconcileRows(t, e)
	changes := mgTunerTotalChanges(t, e)

	turn, m, err := mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", limit: 2})
	if err != nil || len(page.debt) != 2 || page.debt[0].id != "a1" || page.debt[1].id != "a2" || page.cursorChannel != "c1" || page.cursorID != "a2" || !page.more || page.expired != 1 {
		t.Fatal(page, err)
	}
	mgTunerRetireTurnDone(t, turn)

	// A full page is a truthful lower bound; resume from the exact cursor.
	turn, m, err = mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", afterChannel: "c1", afterID: "a2", limit: 2})
	if err != nil || len(next.debt) != 1 || next.debt[0].id != "a3" || next.cursorChannel != "c2" || next.cursorID != "a3" || next.more || next.expired != 1 {
		t.Fatal(next, err)
	}
	mgTunerRetireTurnDone(t, turn)

	// Reopen from the same request: identical page, and the debt rows are still
	// held (expired lease is never a release).
	turn, m, err = mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	again, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", limit: 2})
	if err != nil || len(again.debt) != 2 || again.debt[0] != page.debt[0] || again.debt[1] != page.debt[1] || again.cursorID != page.cursorID {
		t.Fatal(again, err)
	}
	mgTunerRetireTurnDone(t, turn)

	// A dropped wake past the last row changes nothing.
	turn, m, err = mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", afterChannel: "c2", afterID: "a3", limit: 2})
	if err != nil || len(empty.debt) != 0 || empty.cursorChannel != "c2" || empty.cursorID != "a3" || empty.more || empty.expired != 0 {
		t.Fatal(empty, err)
	}
	mgTunerRetireTurnDone(t, turn)
	if after := mgTunerTotalChanges(t, e); after != changes {
		t.Fatal("reconcile performed SQL effects", changes, after)
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if state := mgTunerNativeRow(t, e, id).state; state == "released" {
			t.Fatal("reconcile released debt", id)
		}
	}

	// A removed source retains its live debt and stays reconcilable.
	if _, err := e.db.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='gone',removal_revision=8,removal_removed_at='now' WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	turn, m, err = mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", limit: 8}); err != nil || len(removed.debt) != 3 {
		t.Fatal("removed source lost its debt", removed, err)
	}
	mgTunerRetireTurnDone(t, turn)
}

func TestMGTunerReconcileRefusals(t *testing.T) {
	t.Run("viewer", func(t *testing.T) {
		e, _, p := mgTunerRetireExecutor(t, mgActorViewer)
		mgTunerReconcileRows(t, e)
		turn, m, err := mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if out, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", limit: 2}); !errors.Is(err, ErrDisclosureDenied) || !mgTunerReconcileEmpty(out) {
			t.Fatal("viewer swept owners", out, err)
		}
		mgTunerRetireTurnDone(t, turn)
	})
	for _, limit := range []struct {
		name  string
		limit int64
	}{{"zero", 0}, {"ninth", mgTunerReconcileLimit + 1}} {
		t.Run("limit-"+limit.name, func(t *testing.T) {
			e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
			turn, m, err := mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if out, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", limit: limit.limit}); !errors.Is(err, ErrInvalid) || !mgTunerReconcileEmpty(out) {
				t.Fatal("unbounded page", out, err)
			}
			mgTunerRetireTurnDone(t, turn)
		})
	}
	t.Run("legacy-empty-channel", func(t *testing.T) {
		e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
		now := e.now().UnixMilli()
		mgTunerInsertCommitted(t, e, mgTunerAllocation{id: "legacy", source: "source", channel: "", resource: "legacy-resource", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "legacy-token", generation: 1, state: "active", lease: now + 1000, created: now})
		turn, m, err := mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if out, err := reconcileMGTuners(t.Context(), m, mgTunerReconcileRequest{source: "source", limit: 2}); !errors.Is(err, ErrConflict) || !mgTunerReconcileEmpty(out) {
			t.Fatal("legacy empty channel skipped", out, err)
		}
		mgTunerRetireTurnDone(t, turn)
	})
	t.Run("cancelled", func(t *testing.T) {
		e, _, p := mgTunerRetireExecutor(t, mgActorOwner)
		mgTunerReconcileRows(t, e)
		turn, m, err := mgTunerRetireSession(t, e, p, MGTunerReconcileAllocations, "source", t.Context())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if out, err := reconcileMGTuners(ctx, m, mgTunerReconcileRequest{source: "source", limit: 2}); err == nil || !mgTunerReconcileEmpty(out) {
			t.Fatal("cancelled reconcile released debt", out, err)
		}
		mgTunerRetireTurnDone(t, turn)
	})
}
