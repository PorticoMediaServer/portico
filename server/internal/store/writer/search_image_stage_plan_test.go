package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordSearchImagePlans = flag.Bool("record-search-image-plans", false, "deliberately record image-BEGIN native plans")

// Setup states for the entire closed set, NOT accepting/publication permission.
// The bank is deliberately well-formed/stale, so this fixture supplies no native
// normalization eligibility. Read/identity cohorts retain distinct real states.
func searchImageStagePlanFixture(t *testing.T) *executor {
	t.Helper()
	w := searchPublisherHistorySeed(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	inc := [16]byte{17}
	source := [16]byte{23}
	for job := int64(35); job <= 37; job++ {
		entity := job + 12
		public := [16]byte{byte(entity)}
		exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,3,1)`, entity, public[:])
		exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,revision)
VALUES(?,?,3,1,'Owner','owner','O','owner','O',0,?,zeroblob(16),11)`, entity, public[:], source[:])
		exec(`INSERT INTO search_publish_jobs(id,entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,command_incarnation,last_command_sequence,acknowledged_command_sequence)
VALUES(?,?,7,5,?,11,?,'capture',?,7,7)`, job, entity, source[:], searchpolicy.RecipeV1, inc[:])
	}
	exec(`UPDATE catalog_entities SET revision=11 WHERE id=41`)
	for _, pair := range [][2]int64{{13, 41}, {14, 49}} {
		exec(`INSERT INTO search_publications(id,entity_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens)
VALUES(?,?,7,3,1,3,?,11,?,'owner','','')`, pair[0], pair[1], source[:], searchpolicy.RecipeV1)
	}
	exec(`UPDATE search_publish_jobs SET job_revision=19,input_generation=5,source_revision=11,job_lease_owner=1,job_lease_generation=2,job_lease_expires_ms=2100000000000 WHERE id=29`)
	exec(`UPDATE search_publish_jobs SET job_revision=19,job_lease_owner=1,job_lease_generation=2,job_lease_expires_ms=2100000000000 WHERE id=?`, int64(35))
	exec(`UPDATE search_publish_jobs SET job_revision=19,job_lease_owner=1,job_lease_generation=2,job_lease_expires_ms=2100000000000 WHERE id=?`, int64(36))
	exec(`UPDATE search_publish_jobs SET job_revision=19,job_lease_owner=1,job_lease_generation=2,job_lease_expires_ms=2100000000000 WHERE id=?`, int64(37))
	exec(`UPDATE search_publish_jobs SET published_generation=3,published_document_id=13,job_next_attempt_ms=1000 WHERE id=29`)
	exec(`UPDATE search_publish_jobs SET job_next_attempt_ms=1000 WHERE id=36`)
	exec(`UPDATE search_publish_jobs SET published_generation=3,published_document_id=14 WHERE id=37`)
	return w
}

func TestSearchImageBeginStatementPlans(t *testing.T) {
	w := searchImageStagePlanFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Complete structural Capture on the actual FULL startup connection,
	// BEFORE W's admitted BEGIN. Component execution separately asserts the
	// actual NORMAL policy selected by W's shared searchResourcesFixture.
	startup, err := sqliteconn.Assert(t.Context(), conn, w.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FULL pre-BEGIN capture settings: %+v", startup)
	config := w.config
	defs := append(searchPublisherReceiptDefinitions(), searchImageBeginDefinitions()...)
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
	for _, name := range []string{"search_publish_history_test.go", "search_image_stage_plan_test.go", "search_publish_receipts.go", "search_image_stage_statements.go"} {
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
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed,
		Implementations: "image-BEGIN native SQL/receipt compile only; distinct history states/four nullable owner vectors; fixed stale bank; no live engine or resource/disclosure authority"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture executed fixture effects", before, after, err)
	}
	registry, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Manifest()) != 18 || len(g.Plans) != 39 || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
		t.Fatal("closed capture inventory", len(registry.Manifest()), len(g.Plans))
	}
	fk := g.ForeignKeys
	g.ForeignKeys = nil
	actual, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fkJSON, err := json.MarshalIndent(fk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "search-image-begin.json")
	fkPath := filepath.Join("testdata", "search-image-begin-fk.json.gz")
	if *recordSearchImagePlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if err := plan.CompareJSON(expected, fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Log("18 definitions/39 cohorts/4 INSERT destination audits/686 FK/2058 probes; FULL pre-BEGIN Capture; NORMAL execution separately asserted in W shared fixture; no accepting or native eligibility claim")
}
