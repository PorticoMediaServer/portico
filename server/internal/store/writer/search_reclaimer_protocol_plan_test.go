package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordSearchReclaimerProtocol = flag.Bool("record-search-reclaimer-protocol", false, "deliberately record independent reclaimer ACK plans")

func TestSearchReclaimerProtocolStatementPlans(t *testing.T) {
	e, c := searchReclaimNativeSeed(t)
	// Actual post-retirement unresolved result, seeded before FULL Capture.
	f := c.request.snapshot.fragment
	for _, x := range []struct {
		q    string
		text string
	}{
		{`INSERT INTO search_fragment_prefix(search_fragment_prefix,rowid,prefix_tokens) VALUES('delete',?,?)`, f.row.Prefix},
		{`INSERT INTO search_fragment_lexical(search_fragment_lexical,rowid,lexical_tokens) VALUES('delete',?,?)`, f.row.Lexical},
		{`INSERT INTO search_fragment_grams(search_fragment_grams,rowid,normalized) VALUES('delete',?,?)`, f.row.Normalized},
	} {
		if _, err := e.db.ExecContext(t.Context(), x.q, f.id, x.text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.ExecContext(t.Context(), `DELETE FROM search_fragments WHERE id=601`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE search_reclaim_jobs SET phase='reclaim',after_fragment_id=601,job_revision=3,last_command_sequence=1 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO search_reclaim_receipts(reclaim_job_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,publication_id,fragment_id,after_fragment_id,complete) VALUES(2,?,1,?,?,'fragment_reclaim',3,1000,13,601,601,0)`, c.identity.Incarnation[:], c.identity.OperationID[:], c.digest[:]); err != nil {
		t.Fatal(err)
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := searchReclaimerProtocolDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, name := range []string{"search_reclaimer_protocol_plan_test.go", "search_reclaimer_protocol.go", "search_reclaim_native_test.go", "search_reclaim_statements.go", "search_head_test.go", "search_head_transition_test.go", "search_fragment_stage_test.go", "search_fragment_transition_test.go", "search_image_stage_plan_test.go", "search_publish_history_test.go", "search_image_begin_command_test.go", "search_image_stage_statements.go", "search_publish_receipts.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		h.Write([]byte(name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		h.Write(data)
	}
	var before, after int64
	if err = conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: e.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "independent reclaimer ACK; actual post-retirement unresolved result and stale-floor bind; FULL read-only Capture; no grant or physical qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture executed effects", before, after, err)
	}
	if len(g.Manifest) != 1 || len(g.Plans) != 2 || g.ForeignKeys != nil {
		t.Fatal("incomplete reclaimer ACK capture")
	}
	if _, err = stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "search-reclaimer-protocol.json")
	if *recordSearchReclaimerProtocol {
		if err = os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}

	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	t.Log("one independent ACK ID/two exact cohorts; ordinary UPDATE uses existing Capture DML checks, no INSERT/native FK census; no publisher-owner floor write")
}
