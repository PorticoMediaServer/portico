package writer

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// One immutable source head ('old'), its own published guide metadata and the
// channel row a valid live viewer may acquire. No allocation or reservation is
// implied by the fixture; each cell creates exactly what it needs.
const mgTunerFixtureSQL = `INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('old','channel','provider','Channel','1','',0,x'01','','');`

// The native head keeps the MG-02 source fixture facts; this fixture adds only
// the current generation's channel. Discovery settings stay at their originals
// so the published effective tuner count is the only capacity source.
func mgTunerExecutor(t *testing.T, kind mgActorKind) (*executor, *mgActorProof) {
	t.Helper()
	e, _, p := usOwnerAuthorityFixture(t)
	for _, q := range []string{mgSourceFixtureSQL, mgTunerFixtureSQL} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	switch kind {
	case mgActorViewer:
		if _, err := e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET role='member'; UPDATE live_source_identities SET settings_viewer_access='server-members'`); err != nil {
			t.Fatal(err)
		}
	case mgActorAdminViewer:
		if _, err := e.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET role='admin'; UPDATE direct_memberships SET role='admin'; UPDATE live_source_identities SET settings_viewer_access='server-members'`); err != nil {
			t.Fatal(err)
		}
	}
	return e, &mgActorProof{native: *p, kind: kind}
}

func mgTunerDefinitionsUnion() []stmt.Definition {
	defs := mgAuthorityDefinitions()
	defs = append(defs, mgSourceDefinitions()...)
	return append(defs, mgTunerDefinitions()...)
}

// 814/815 have no runtime recipe yet, so the native frame opens as the existing
// NORMAL configure purpose and the descriptor is retargeted afterwards, exactly
// like the MG-02 authority fixture. This is not a qualified accepting profile.
func mgTunerSessionWith(t *testing.T, e *executor, p *mgActorProof, purpose CommandID, access string) (*commandTurn, *mgSourceAcquisition, error) {
	t.Helper()
	defs := mgTunerDefinitionsUnion()
	r, err := stmttest.New(t, defs)
	if err != nil {
		return nil, nil, err
	}
	recipe := testRecipe()
	recipe.limit = Work{100000, 20_000_000, 100000, 64 << 20, 20 * time.Millisecond}
	tx, err := e.begin(t.Context(), MGConfigureTuner, recipe, &testCustody{})
	if err != nil {
		return nil, nil, err
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 500, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		return nil, nil, err
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	turn.transaction.descriptor.ID = purpose
	b := mgAuthorityBudget{memory: 4 << 20, fixture: &mgAuthorityFixturePermit{frame: tx}}
	f, err := openMGSourceFrame(t.Context(), turn, p, t.Context(), "source", b)
	if err != nil {
		return turn, nil, err
	}
	a, err := authorizeMGSourceAcquisition(t.Context(), f, mgSourceHeadWitness{"source", "old", access, 7})
	if err != nil {
		return turn, nil, err
	}
	return turn, a, nil
}

func mgTunerSession(t *testing.T, e *executor, p *mgActorProof, purpose CommandID) (*commandTurn, *mgSourceAcquisition) {
	t.Helper()
	access := "owner-only"
	if p.kind != mgActorOwner {
		access = "server-members"
	}
	turn, a, err := mgTunerSessionWith(t, e, p, purpose, access)
	if err != nil {
		t.Fatal(a, err)
	}
	return turn, a
}

func mgTunerFixture(t *testing.T, purpose CommandID, kind mgActorKind) (*executor, *commandTurn, *mgSourceAcquisition) {
	t.Helper()
	e, p := mgTunerExecutor(t, kind)
	return mgTunerFixtureWith(t, e, p, purpose)
}

func mgTunerFixtureWith(t *testing.T, e *executor, p *mgActorProof, purpose ...CommandID) (*executor, *commandTurn, *mgSourceAcquisition) {
	t.Helper()
	id := MGAcquireAllocation
	if len(purpose) == 1 {
		id = purpose[0]
	}
	turn, a := mgTunerSession(t, e, p, id)
	return e, turn, a
}

// A request whose only mutable field is the lease: the allocation tuple is
// conserved exactly at insert.
func mgTunerRequestAt(t *testing.T, e *executor) mgTunerRequest {
	t.Helper()
	now := e.now().UnixMilli()
	return mgTunerRequest{allocation: mgTunerAllocation{id: "alloc", source: "source", channel: "channel", resource: "resource", kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: "token", generation: 1, state: "active", lease: now + 10000, created: now, preempt: 0}, guide: "old"}
}

func mgTunerInsertHeld(t *testing.T, turn *commandTurn, a mgTunerAllocation) {
	t.Helper()
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.values()...); err != nil {
		t.Fatal(err)
	}
}

func mgTunerCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
