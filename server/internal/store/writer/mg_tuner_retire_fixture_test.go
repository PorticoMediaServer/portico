package writer

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The ordinary US owner population plus the exact MG source/channel head. The
// clock is frozen so no test depends on wall time.
func mgTunerRetireExecutor(t *testing.T, kind mgActorKind) (*executor, string, *mgActorProof) {
	t.Helper()
	e, path := testCanonicalExecutor(t)
	now := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	e.now = func() time.Time { return now }
	proof := usOwnerAuthorityPopulation(t, e, now)
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
	return e, path, &mgActorProof{native: *proof, kind: kind}
}

// The adopted full F media schema plus the FK-valid
// catalogue/playback fixture one native exit proof needs. The MP bridge is real:
// a child process starts, joins and is only then a physical observation.
func mgTunerRetirePhysical(t *testing.T, e *executor, media, execution string) (*mpPhysicalSupervisor, *mpPhysicalObservation, mpPhysicalKey) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(`INSERT OR IGNORE INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,1,1,1,0)`)
	exec(`INSERT INTO catalog_libraries(library_id,root) VALUES('mg-retire-library','/srv/mg-retire')`)
	var library int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT id FROM catalog_libraries WHERE library_id='mg-retire-library'`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(9901,randomblob(16),?,1)`, library)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(9901,'mg-retire-asset','mg-retire.mkv',1,1,'matroska','h264','aac',1,1,1,?)`, library)

	owner := &Writer{effective: &effectiveRuntime{executor: e}}
	s, err := newMPPhysicalSupervisor(owner, t.Context(), "server")
	if err != nil {
		t.Fatal(err)
	}
	probe, err := s.start(mpPhysicalRoot{media, execution, 2}, &mpPhysicalTestSupervisor{}, mpPhysicalCommand(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	o, err := probe.join(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	key := mpPhysicalKey{media: media, execution: execution, request: media + "-stop", generation: 2, revision: 2}
	digest := mpPhysicalRetirementDigest(o, key.request)
	exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id,mp_retired_generation,mp_retire_request_id,mp_exited_generation,mp_exit_supervisor_incarnation,mp_exited_ms,mp_exit_request_id,mp_exit_digest,mp_cleanup_complete,mp_cleanup_cursor,mp_revision,mp_grant_retired) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		media, "hash-"+media, "issuer", "issuer-profile", 9901, "mg-retire-asset", 2, "stopped", "grant-"+media, "opaque", "2099-01-01T00:00:00Z", media+"-start", 1, execution, 2, key.request, 2, o.incarnation[:], 101, key.request, hex.EncodeToString(digest[:]), 1, 0, 2, 1)
	return s, o, key
}

func mgTunerRetireAllocationAt(e *executor, media, execution string) mgTunerAllocation {
	now := e.now().UnixMilli()
	return mgTunerAllocation{id: "alloc", source: "source", channel: "channel", resource: media, kind: "live", authority: "local", account: "issuer", profile: "issuer-profile", token: execution, generation: 1, state: "active", lease: now + 10000, created: now}
}

func mgTunerInsertCommitted(t *testing.T, e *executor, a mgTunerAllocation) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.values()...); err != nil {
		t.Fatal(err)
	}
}

func mgTunerRetireDefinitionsUnion() []stmt.Definition {
	defs := mgAuthorityDefinitions()
	defs = append(defs, mgSourceDefinitions()...)
	defs = append(defs, mgTunerDefinitions()...)
	defs = append(defs, mgTunerRetireDefinitions()...)
	return append(defs, mpPhysicalDefinitions()...)
}

// 816/817 have no runtime recipe yet, so the native frame opens as the existing
// NORMAL configure purpose and is retargeted afterwards. The maintenance budget
// is the only way these purposes open the US actor witness. This is not a
// qualified accepting profile.
func mgTunerRetireSession(t *testing.T, e *executor, p *mgActorProof, purpose CommandID, source string, life context.Context) (*commandTurn, *mgTunerMaintenance, error) {
	t.Helper()
	defs := mgTunerRetireDefinitionsUnion()
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
	b := mgAuthorityBudget{memory: 4 << 20, fixture: &mgAuthorityFixturePermit{frame: tx}, maintenance: true}
	m, err := openMGTunerMaintenance(t.Context(), turn, p, life, source, b)
	if err != nil {
		return turn, nil, err
	}
	return turn, m, nil
}

func mgTunerNativeRow(t *testing.T, e *executor, id string) mgTunerAllocation {
	t.Helper()
	var a mgTunerAllocation
	if err := e.db.QueryRowContext(t.Context(), `SELECT `+mgTunerColumns+` FROM live_allocations WHERE id=?`, id).Scan(&a.id, &a.source, &a.channel, &a.resource, &a.kind, &a.authority, &a.account, &a.profile, &a.token, &a.generation, &a.state, &a.lease, &a.created, &a.preempt); err != nil {
		t.Fatal(err)
	}
	return a
}

// Fresh read-only native connection: never the writer that may be ambiguous.
func mgTunerFreshState(t *testing.T, path string) string {
	t.Helper()
	reader, _, err := sqliteconn.Open(t.Context(), path, sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var state string
	if err := reader.QueryRowContext(t.Context(), `SELECT state FROM live_allocations WHERE id='alloc'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func mgTunerTotalChanges(t *testing.T, e *executor) int64 {
	t.Helper()
	var changes int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

// Total native changes on the turn's own writer connection. The fixture pool
// holds exactly one connection, so a same-turn refusal proof must never wait on
// a second one.
func mgTunerTurnChanges(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}
