package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordEnrichmentStagePlans = flag.Bool("record-enrichment-stage-plans", false, "explicitly record staged enrichment plans on the reviewed fixture")

func enrichmentStageDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, group := range [][]stmt.Definition{enrichmentSealScopeDefinitions(), enrichmentStageFactDefinitions(), enrichmentEvidencePinDefinitions(), enrichmentSourceStageDefinitions(), enrichmentCandidateRetirementDefinitions(), enrichmentEvidenceStageEffectDefinitions(), enrichmentSealAdvanceDefinitions(), enrichmentSealReceiptDefinitions(), enrichmentSourceAllocationDefinitions()} {
		defs = append(defs, group...)
	}
	return defs
}
func enrichmentStageFixtureIdentity(t *testing.T) string {
	t.Helper()
	h := sha256.New()
	for _, entry := range []struct{ path, name string }{{"enrichment_effective_begin_facts_test.go", "effectiveBeginNativePopulation"}, {"enrichment_evidence_stage_pin_test.go", "evidenceBuildPinFixture"}, {"enrichment_source_seal_candidates_test.go", "sealEffectFixture"}, {"enrichment_source_seal_stage_test.go", "draftSourceStageFixture"}, {"enrichment_source_seal_receipts_test.go", "sourceSealFixture"}} {
		data, err := os.ReadFile(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, entry.path, data, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == entry.name {
				io.WriteString(h, entry.path+":"+entry.name+"\x00")
				h.Write(data[set.Position(fn.Pos()).Offset:set.Position(fn.End()).Offset])
				found = true
			}
		}
		if !found {
			t.Fatal("missing immutable fixture function", entry.name)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
func TestEnrichmentStageStatementPlans(t *testing.T) {
	// SAME existing owned native frame/connection; these static captures execute
	// no effects, issue no proof, and make no production Work/HOLD qualification.
	w, _, _, _ := sourceSealFixture(t)
	if enrichmentStageFixtureIdentity(t) != "dc343282128349271df192651d674582d74014d712ebd5cd64057f71666ce052" {
		t.Fatal("staged enrichment fixture changed; review before recording")
	}
	defs := enrichmentStageDefinitions()
	var component []stmt.Definition
	for _, d := range defs {
		if d.ID != sealObservedOwner && d.ID != stageBuildFacts && d.ID != stageEvidenceProofFacts {
			component = append(component, d)
		}
	}
	turn := sealEffectTurn(t, w, component)
	conn := turn.transaction.conn
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexes, err := plan.ReadIndexPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: policy, SHA256: enrichmentStageFixtureIdentity(t), Statistics: plan.Unanalyzed, Implementations: "adopted009 on embedded010; fixed inert native source/component fixture; no producer proof or runtime qualification"}, indexes)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("capture changed fixture", before, after, err)
	}
	if len(golden.Manifest) != 32 || len(golden.Plans) != 33 || golden.ForeignKeys == nil || plan.CheckSchemaForeignKeys(golden.ForeignKeys) != nil {
		t.Fatal("incomplete native FK capture")
	}
	data, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "enrichment-stage-010.json.gz")
	if *recordEnrichmentStagePlans {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err := z.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	z, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	err = errors.Join(err, z.Close())
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
	t.Logf("static definitions=%d/cohorts=%d FK=%d; schema=%s; no native effects", len(defs), len(golden.Plans), len(golden.ForeignKeys.Plans), golden.Environment.SchemaSHA256)
}
