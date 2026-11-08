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

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordWCSecurityPlans = flag.Bool("record-wc-security-plans", false, "deliberately record closed WC security component plans")

// Actual native target births; compile the two frozen statements without
// granting current admin authority or executing a production security command.
func TestWCSecurityStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"wc_008_native_fixture_test.go", "029dda9c571f6361c1914d86ce49d03c98c697312b442eda54e001dd03012adc"},
		{"wc_security_008_native_test.go", "98d9ca617c9d803915f6f6788ce9c02040870a5375a849b6a21d906584998056"},
	}
	hash := sha256.New()
	for _, part := range parts {
		data, err := os.ReadFile(part.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != part.sha {
			t.Fatal("frozen fixture source changed; review before recording", part.name)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(part.name))))
		hash.Write([]byte(part.name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	journal := hex.EncodeToString(hash.Sum(nil))
	if journal != "de21c5377495df9954df9b463f7c522340f15e42edbe7ff58816d0a82785c76d" {
		t.Fatal("fixture journal changed")
	}
	defs := wcSecurityDefinitions()
	if len(defs) != 2 {
		t.Fatalf("definitions %d, want2", len(defs))
	}
	cohorts := 0
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot cohort inventory changed", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		cohorts += len(d.Cohorts)
	}
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

	w := wc008NativeExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
	closeErr := conn.Close()
	if assertErr != nil || closeErr != nil {
		t.Fatal(startup, assertErr, closeErr)
	}
	t.Logf("fixture startup physical settings: %+v", startup)
	wc008NativeSecurity(t, w)
	turn := wc008NativeTurn(t, w, SecurityAccept, defs)
	tx := turn.transaction
	connectionPolicy := w.config
	connectionPolicy.Synchronous = sqliteconn.Full
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, connectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("security FULL acquired physical settings: %+v", settings)
	var before, after int64
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: connectionPolicy, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "WC wc008NativeSecurity original native birth/target fixture; compile-only component custody, not current admin issuance, public enable or resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal("exact full-set Capture receipt", err)
	}
	if len(golden.Manifest) != 2 || len(golden.Plans) != cohorts || golden.ForeignKeys != nil {
		t.Fatal("incomplete statement/destination/FK capture")
	}
	vm, search, inserts := 0, 0, 0
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 {
			t.Fatal("empty VM", p.ID, p.Cohort)
		}
		vm += len(p.Explain.Values)
		search += len(p.QueryPlan.Values)
		if p.Insert != nil {
			inserts++
		}
	}
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture mutated population", before, after, err)
	}
	fk, err := cachedForeignKeys(tx.ctx, tx.conn, connectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CheckSchemaForeignKeys(&fk) != nil {
		t.Fatal("FK coverage changed")
	}
	fkJSON, err := json.MarshalIndent(fk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden.ForeignKeys = nil
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "wc-security.json")
	fkPath := filepath.Join("testdata", "wc-security-fk.json.gz")
	if *recordWCSecurityPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		{
			var compressed bytes.Buffer
			gz := gzip.NewWriter(&compressed)
			if _, err := gz.Write(fkJSON); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	expectedFK, err := io.ReadAll(gz)
	closeErr = gz.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if err := plan.CompareExactJSON(expectedFK, fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("2IDs/%dcohorts/%dVM/%dEQP SEARCH rows/%dINSERT destinations/shared-schema FK audit; exact receipt; no production profile", cohorts, vm, search, inserts)
}
