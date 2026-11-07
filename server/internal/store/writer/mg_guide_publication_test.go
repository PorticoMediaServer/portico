package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func mgGuideNativeFixture(t *testing.T, sealed bool, state string) (*executor, mgGuideNative) {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), "DROP TABLE live_sources; DROP TABLE live_operations;"+mgGuidePublicationDraftSQL); err != nil {
		t.Fatal(err)
	}
	p, stages := mgGuideProtocolFixture()
	p.manifest.expected = [3]int64{2, 9, 2}
	for _, d := range stages {
		next, err := planMGGuideStage(p, d)
		if err != nil {
			t.Fatal(err)
		}
		p = next
	}
	if sealed {
		next, err := planMGGuideSeal(p, p.sequence+1)
		if err != nil {
			t.Fatal(err)
		}
		p = next
	}
	before := mgGuideNative{request: "guide-request", input: "input-digest", name: "Guide", createdAt: "2026-11-07T00:00:00.000Z", status: "staging", tuner: 1, progress: p, acknowledged: p.sequence, incarnation: [16]byte{1}, createOperation: [16]byte{2}, birthDigest: [32]byte{3}}
	m := p.manifest
	for _, q := range []string{
		"INSERT INTO live_source_identities(id) VALUES('source')",
		"INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('old','source','Old',1,2,9,'before')",
		"INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('new','source','Guide',1,2,9,'2026-11-07T00:00:00.000Z')",
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), "INSERT INTO live_sources VALUES('source','Old',7,?,1,'old','before')", state); err != nil {
		t.Fatal(err)
	}
	sealedN := int64(0)
	if sealed {
		sealedN = 1
	}
	_, err := w.db.ExecContext(t.Context(), `INSERT INTO live_operations(request_id,input_hash,source_id,generation_id,expected_revision,status,expected_generation,expected_choice_count,command_incarnation,create_operation_id,birth_request_digest,last_command_sequence,acknowledged_command_sequence,stage_revision,staged_channel_count,staged_programme_count,staged_choice_count,staged_bytes,manifest_digest,stage_digest,sealed) VALUES(?,?,?,?,?,'staging',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, before.request, before.input, m.source, m.generation, m.expectedRevision, m.previousGeneration, m.expected[2], before.incarnation[:], before.createOperation[:], before.birthDigest[:], p.sequence, before.acknowledged, p.revision, p.staged[0], p.staged[1], p.staged[2], p.bytes, m.expectedChain[:], p.chain[:], sealedN)
	if err != nil {
		t.Fatal(err)
	}
	return w, before
}
func mgGuideNativeTurn(t *testing.T, w *executor, defs ...[]stmt.Definition) *commandTurn {
	t.Helper()
	definitions := mgGuidePublicationDefinitions()
	if len(defs) > 0 {
		definitions = defs[0]
	}
	r, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), MGGuidePublish, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range definitions {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func mgGuideIdentityAfter(before mgGuideNative) CommandIdentity {
	return CommandIdentity{Incarnation: before.incarnation, Sequence: before.progress.sequence + 1, OperationID: [16]byte{9}}
}
func TestMGGuideNativeSealAndPublish(t *testing.T) {
	for _, state := range []string{"active", "disabled"} {
		t.Run(state, func(t *testing.T) {
			w, before := mgGuideNativeFixture(t, false, state)
			turn := mgGuideNativeTurn(t, w)
			actual, err := mgReadGuideNative(t.Context(), turn, before.request)
			if err != nil || actual != before {
				t.Fatal("actual staged facts", actual, err)
			}
			sealed, err := mgSealGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before))
			if err != nil || !sealed.operation.progress.sealed {
				t.Fatal("seal", sealed, err)
			}
			// The real driver saves and ACKs the exact typed receipt before a new sequence.
			// This component supplies only that native ACK watermark, not replay authority.
			if _, err := turn.transaction.conn.ExecContext(t.Context(), "UPDATE live_operations SET acknowledged_command_sequence=last_command_sequence WHERE request_id=?", before.request); err != nil {
				t.Fatal(err)
			}
			next := sealed.operation
			next.acknowledged = next.progress.sequence
			result, err := mgPublishGuideNative(t.Context(), turn, next, mgGuideIdentityAfter(next), true)
			if err != nil || result.head != (mgGuideHead{"source", "new", state, 7, false}) || result.operation.status != "published" || result.operation.publishedAt == "" {
				t.Fatal("publication", result, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var old string
			if err := w.db.QueryRowContext(t.Context(), "SELECT name FROM live_generations WHERE id='old'").Scan(&old); err != nil || old != "Old" {
				t.Fatal("old immutable generation lost", old, err)
			}
		})
	}
}
func TestMGGuideNativeFencesAndZeroFailures(t *testing.T) {
	for _, name := range []string{"unsealed", "unacked", "foreign-incarnation", "same-revision-head", "removed"} {
		t.Run(name, func(t *testing.T) {
			w, before := mgGuideNativeFixture(t, name != "unsealed", "active")
			switch name {
			case "unacked":
				before.acknowledged--
				if _, err := w.db.ExecContext(t.Context(), "UPDATE live_operations SET acknowledged_command_sequence=?", before.acknowledged); err != nil {
					t.Fatal(err)
				}
			case "same-revision-head":
				if _, err := w.db.ExecContext(t.Context(), "UPDATE live_sources SET active_generation='new'"); err != nil {
					t.Fatal(err)
				}
			case "removed":
				if _, err := w.db.ExecContext(t.Context(), "UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=8,removal_removed_at='now'"); err != nil {
					t.Fatal(err)
				}
			}
			turn := mgGuideNativeTurn(t, w)
			identity := mgGuideIdentityAfter(before)
			if name == "foreign-incarnation" {
				identity.Incarnation[0] = 2
			}
			result, err := mgPublishGuideNative(t.Context(), turn, before, identity, true)
			if err == nil || result != (mgGuideNativeResult{}) {
				t.Fatal("bad publication/nonzero failure", name, result, err)
			}
		})
	}
}
func TestMGGuideNativeLateCASRollsBackHead(t *testing.T) {
	w, before := mgGuideNativeFixture(t, true, "active")
	defs := mgGuidePublicationDefinitions()
	for i := range defs {
		if defs[i].ID == mgGuidePublishOperation {
			defs[i].SQL = strings.Replace(defs[i].SQL, "request_id=?", "request_id=? AND result_revision=-1", 1)
		}
	}
	turn := mgGuideNativeTurn(t, w, defs)
	got, err := mgPublishGuideNative(t.Context(), turn, before, mgGuideIdentityAfter(before), true)
	if !errors.Is(err, ErrConflict) || got != (mgGuideNativeResult{}) {
		t.Fatal("late CAS failure", got, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var head string
	if err := w.db.QueryRowContext(t.Context(), "SELECT active_generation FROM live_sources WHERE id='source'").Scan(&head); err != nil || head != "old" {
		t.Fatal("partial head escaped rollback", head, err)
	}
}
func TestMGGuideNativeReadCapture(t *testing.T) {
	w, _ := mgGuideNativeFixture(t, false, "active")
	var defs []stmt.Definition
	for _, d := range mgGuidePublicationDefinitions() {
		if d.Kind == stmt.Read {
			defs = append(defs, d)
		}
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(mgGuidePublicationDraftSQL))
	cfg := w.config
	g, err := plan.Capture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "exact MG-001 guide operation/source empty overlay; native read-only Capture; not authority, native DML profile or qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 4 {
		t.Fatal("closed read cohorts", len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if _, err := sqliteconn.Assert(context.Background(), c, cfg); err != nil {
		t.Fatal(err)
	}
}
