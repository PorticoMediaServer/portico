package writer

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed guide retirement definitions: the staging/begin/receipt components this
// task builds on plus its own bounded child pages and cursor effects.
func mgGuideReclaimFixtureDefinitions() []stmt.Definition {
	defs := mgGuideDefinitions()
	return append(defs, mgGuideReclaimDefinitions()...)
}

func mgReclaimWitness(revision int64, head string, removed bool) mgGuideReclaimWitness {
	return mgGuideReclaimWitness{source: "source", head: head, revision: revision, removed: removed}
}

func mgReclaimTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	return mgGuideTestTurn(t, w, purpose, mgGuideReclaimFixtureDefinitions())
}

// Deterministic frozen stages for one generation. G folds programme metadata
// into live_programmes; the inherited metadata columns still carry real bytes.
func mgReclaimStages(channels, programmes, choices int) ([]mgGuideFrozenStage, error) {
	if channels <= 0 || programmes <= 0 || choices <= 0 {
		return nil, ErrInvalid
	}
	var out []mgGuideFrozenStage
	for start := 0; start < channels; start += int(mgGuideReclaimRows) {
		n := channels - start
		if n > int(mgGuideReclaimRows) {
			n = int(mgGuideReclaimRows)
		}
		rows := make([]mgGuideChannel, n)
		for j := 0; j < n; j++ {
			idx := start + j
			rows[j] = mgGuideChannel{id: fmt.Sprintf("channel-%02d", idx), provider: fmt.Sprintf("provider-%02d", idx), name: "Channel", position: int64(idx), locator: []byte("sealed locator"), availableStart: "2026-11-07T00:00:00Z", availableEnd: "2026-11-08T00:00:00Z"}
		}
		stage, err := freezeMGGuideChannels(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, stage)
	}
	for start := 0; start < programmes; start += int(mgGuideReclaimRows) {
		n := programmes - start
		if n > int(mgGuideReclaimRows) {
			n = int(mgGuideReclaimRows)
		}
		rows := make([]mgGuideProgramme, n)
		for j := 0; j < n; j++ {
			idx := start + j
			rows[j] = mgGuideProgramme{
				id: fmt.Sprintf("programme-%02d", idx), channel: fmt.Sprintf("channel-%02d", idx%channels),
				provider: fmt.Sprintf("programme-provider-%02d", idx), title: "Programme",
				start: "2026-11-07T00:00:00Z", end: "2026-11-07T01:00:00Z", lineage: "lineage",
				metadata: &mgGuideProgrammeMetadata{series: "series", episode: "episode", newEvidence: "new", description: "description", facts: "{}"},
			}
		}
		stage, err := freezeMGGuideProgrammes(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, stage)
	}
	for start := 0; start < choices; start += int(mgGuideReclaimRows) {
		n := choices - start
		if n > int(mgGuideReclaimRows) {
			n = int(mgGuideReclaimRows)
		}
		rows := make([]mgGuideChoice, n)
		for j := 0; j < n; j++ {
			rows[j] = mgGuideChoice{key: fmt.Sprintf("choice-%02d", start+j), name: "Channel", group: "Group"}
		}
		stage, err := freezeMGGuideChoices(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, stage)
	}
	return out, nil
}

// mgReclaimStagedFixture seeds the served generation "old" at revision 7 and
// stages a complete "new" generation through the closed begin/stage components.
func mgReclaimStagedFixture(t *testing.T, channels, programmes, choices int) (*executor, string, mgGuideNative) {
	t.Helper()
	w, path := testCanonicalExecutor(t)
	stages, err := mgReclaimStages(channels, programmes, choices)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO live_source_identities(id) VALUES('source')`,
		`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('old','source','Old',1,1,1,'before')`,
		`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('source','Old',7,'active',1,'old','before')`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	m := mgGuideManifest{source: "source", generation: "new", expected: [3]int64{int64(channels), int64(programmes), int64(choices)}, expectedRevision: 7, previousGeneration: "old"}
	m.expectedChain = mgGuideChainFor(m, stages, 2)
	b := mgGuideBirth{request: "guide-request", input: "guide-input", name: "Guide", tuners: 1, manifest: m,
		identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, requestDigest: RequestDigest{3}}
	before := mgGuideBeginCommitted(t, w, b)
	for _, stage := range stages {
		_, before, _ = mgGuideStageCommitted(t, w, before, stage)
	}
	return w, path, before
}

// A second staging operation on the same served head, used for cleanup ordering.
func mgReclaimSecondOperation(t *testing.T, w *executor) mgGuideNative {
	t.Helper()
	m := mgGuideManifest{source: "source", generation: "new2", expected: [3]int64{2, 10, 1}, expectedRevision: 7, previousGeneration: "old"}
	stages, err := mgReclaimStages(2, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.expectedChain = mgGuideChainFor(m, stages, 2)
	b := mgGuideBirth{request: "guide-request-2", input: "guide-input-2", name: "Guide", tuners: 1, manifest: m,
		identity: CommandIdentity{Incarnation: [16]byte{4}, Sequence: 1, OperationID: [16]byte{5}}, requestDigest: RequestDigest{6}}
	return mgGuideBeginCommitted(t, w, b)
}

// mgReclaimDiscard commits the logical withdrawal and then simulates the
// receipt ACK that MG-26's closed adapter supplies. Without it the operation has
// one receipt above the acknowledged floor and no retirement slice is admitted.
func mgReclaimDiscard(t *testing.T, w *executor, op mgGuideNative, witness mgGuideReclaimWitness) (mgGuideNativeResult, mgGuideNative) {
	t.Helper()
	turn := mgReclaimTurn(t, w, MGGuideDiscard)
	out, err := mgDiscardGuideNative(t.Context(), turn, mgGuideDiscardInput{request: op.request, identity: mgGuideIdentityAfter(op), witness: witness})
	if err != nil {
		t.Fatal(err)
	}
	mgGuideCommit(t, turn)
	return out, mgReclaimACK(t, w, op.request)
}

// mgReclaimACK advances the acknowledged floor exactly as the saved original
// result acknowledgement does, then rereads the native row on a fresh turn.
func mgReclaimACK(t *testing.T, w *executor, request string) mgGuideNative {
	t.Helper()
	var sequence int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_command_sequence FROM live_operations WHERE request_id=?`, request).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	execMG(t, w, `UPDATE live_operations SET acknowledged_command_sequence=? WHERE request_id=? AND last_command_sequence=?`, []any{sequence, request, sequence})
	turn := mgReclaimTurn(t, w, MGGuideReclaim)
	after, err := mgReadGuideNative(t.Context(), turn, request)
	if err != nil {
		t.Fatal(err)
	}
	mgGuideRollback(t, turn)
	return after
}

// mgReclaimSlice runs exactly one bounded retirement step, commits it and ACKs
// its saved result before the next slice is admitted.
func mgReclaimSlice(t *testing.T, w *executor, op mgGuideNative, witness mgGuideReclaimWitness) (mgGuideReclaimResult, mgGuideNative) {
	t.Helper()
	turn := mgReclaimTurn(t, w, MGGuideReclaim)
	out, err := mgReclaimGuideNative(t.Context(), turn, mgGuideReclaimInput{request: op.request, identity: mgGuideIdentityAfter(op), witness: witness})
	if err != nil {
		t.Fatal(err)
	}
	mgGuideCommit(t, turn)
	if out.blocked != "" {
		return out, op
	}
	return out, mgReclaimACK(t, w, op.request)
}

// A fresh read-only connection to the committed file. Never the writer pool and
// never the quarantined connection: lost/unknown COMMIT is read back here.
func mgReclaimReader(t *testing.T, path string) *sql.DB {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 64}}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type mgReclaimDurable struct {
	status, family, cursor           string
	sequence, acknowledged, revision int64
	programmes, channels, choices    int64
	generations, sourceHead          int64
}

func mgReclaimReadDurable(t *testing.T, db *sql.DB, request string) mgReclaimDurable {
	t.Helper()
	var out mgReclaimDurable
	if err := db.QueryRowContext(context.Background(), `SELECT status,last_command_sequence,acknowledged_command_sequence,stage_revision,reclaim_family,reclaim_cursor FROM live_operations WHERE request_id=?`, request).
		Scan(&out.status, &out.sequence, &out.acknowledged, &out.revision, &out.family, &out.cursor); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		query string
		into  *int64
	}{
		{`SELECT count(*) FROM live_programmes WHERE generation_id='new'`, &out.programmes},
		{`SELECT count(*) FROM live_channel_versions WHERE generation_id='new'`, &out.channels},
		{`SELECT count(*) FROM live_channel_choices WHERE generation_id='new'`, &out.choices},
		{`SELECT count(*) FROM live_generations WHERE id='new'`, &out.generations},
		{`SELECT count(*) FROM live_sources INDEXED BY live_sources_generation_head WHERE active_generation='new'`, &out.sourceHead},
	} {
		if err := db.QueryRowContext(context.Background(), q.query).Scan(q.into); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func mgReclaimTurnChanges(t *testing.T, turn *commandTurn) int64 {
	t.Helper()
	var changes int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

// Direct fixture seed for pins/floors/head flips owned by other commands.
func execMG(t *testing.T, w *executor, query string, args []any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
