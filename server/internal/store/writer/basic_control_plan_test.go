package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordBasicControlPlans = flag.Bool("record-basic-control-plans", false, "deliberately record frozen Basic control component plans")

func TestBasicControlStatementPlans(t *testing.T) {
	actual, fk := captureBasicControlPlans(t)
	path := filepath.Join("testdata", "scan-basic-control.json")
	fkPath := filepath.Join("testdata", "scan-basic-control-fk.json.gz")
	if *recordBasicControlPlans {
		if err := os.WriteFile(path, actual, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, fk, 0644); err != nil {
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
	if expected, err := os.ReadFile(fkPath); err != nil || !bytes.Equal(expected, fk) {
		t.Fatal("raw FK reference parity", err)
	}
}

// Closed native SQL component populations only. The immutable owner supplies
// every seed and state transition; this is not Basic command/admission proof.
func captureBasicControlPlans(t *testing.T) ([]byte, []byte) {
	t.Helper()
	parts := []struct{ name, sha string }{
		{"basic_control.go", "57181bc88298c11f3fa924b986b6a03f9ed8c60172e0bc79775823ad58db62d5"},
		{"basic_control_statements.go", "cae20d889dc5a37925dedf58142fa872ab6108ac38745cd0b6320c340eb70b4a"},
		{"basic_control_test.go", "3025acbc66b43e25982fa7761664b3a8eb2f24bb77f0cd461befd2e180148a3b"},
		{"basic_original.go", "5015f3cc2fafad60e60b004129e72a89a77037d1168076f02071a8f94812c3da"},
		{"basic_stage_test.go", "8e8da49da22747d1d709405398198f8c3fd46400bc63c9fb434758c553a17966"},
		{"basic_stage.go", "b2adbf700ce9928f1a2b388322124455b564780b85d71a6026861342aed52b23"},
	}
	h := sha256.New()
	for _, p := range parts {
		b, err := os.ReadFile(p.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != p.sha {
			t.Fatal("frozen df0 Basic source/seed changed", p.name)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(p.name))))
		h.Write([]byte(p.name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
		h.Write(b)
	}
	journal := hex.EncodeToString(h.Sum(nil))
	defs := basicControlDefinitions()
	var fresh, post, receipt []stmt.Definition
	reads, writes := 0, 0
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("closed hot unwaived definition", d.ID)
		}
		switch d.Kind {
		case stmt.Read:
			reads++
		case stmt.Write:
			writes++
		default:
			t.Fatal("unexpected statement kind", d.ID)
		}
		switch d.ID {
		case basicControlPostState:
			post = append(post, d)
		case basicControlReceipt:
			receipt = append(receipt, d)
		default:
			fresh = append(fresh, d)
		}
	}
	if len(defs) != 11 || reads != 9 || writes != 2 || len(fresh) != 9 || len(post) != 1 || len(receipt) != 1 {
		t.Fatal("11-definition inventory changed")
	}
	w, source, original, identity := basicControlFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fullAssert, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
	closeErr := conn.Close()
	if assertErr != nil || closeErr != nil || fullAssert.Synchronous != 2 {
		t.Fatal("FULL startup boundary", fullAssert, assertErr, closeErr)
	}
	t.Logf("Basic control FULL startup: %+v", fullAssert)
	turn := basicControlTurn(t, w)
	tx := turn.transaction
	config := w.config
	config.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, config)
	if err != nil || settings.Synchronous != 1 {
		t.Fatal("NORMAL acquired owner turn", settings, err)
	}
	t.Logf("Basic control NORMAL capture boundary: %+v", settings)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	// Execute an independent complete audit on the actual acquired owner
	// connection. Never substitute a checked-in FK object for an acquisition.
	fk, err := plan.ForeignKeys(tx.ctx, tx.conn, config)
	if err != nil || len(fk.Plans) != 686 {
		t.Fatal("independent complete FK acquisition", len(fk.Plans), err)
	}
	fkJSON, err := json.MarshalIndent(fk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fkPath := filepath.Join("testdata", "scan-inventory-accept-fk.json.gz")
	if err := plan.CompareExactJSON(readScanFreshFK(t, fkPath), fkJSON); err != nil {
		t.Fatal("independent audit differs from merged 686FK/2058-probe reference", err)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write(fkJSON); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	// Preserve merged raw bytes as well as acquired decompressed content.
	// Toolchain gzip variation is not silently adopted as a new global audit.
	referenceRaw, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("acquired FK gzip byte parity with merged reference: %t", bytes.Equal(referenceRaw, compressed.Bytes()))
	type population struct {
		Name   string      `json:"name"`
		Golden plan.Golden `json:"golden"`
	}
	captures := []population{}
	vm, search, destinations := 0, 0, 0
	var facts basicFreshFacts
	for n, group := range [][]stmt.Definition{fresh, post, receipt} {
		name := "fresh_native_scope_and_eligible_run_cas"
		if n == 1 {
			facts, err = readBasicFreshFacts(tx.ctx, turn, identity, original, source)
			if err != nil || facts != (basicFreshFacts{41, 59, 43, 47, 67}) {
				t.Fatal("owner fresh hit path", facts, err)
			}
			if got, err := stageBasicStreamFact(tx.ctx, turn, source); err != nil || got != (basicFamilyProgress{51, 7, 1, 0}) {
				t.Fatal("owner native stage population", got, err)
			}
			name = "native_continuation_after_owner_stage"
		}
		if n == 2 {
			if got, err := readBasicControlPostState(tx.ctx, turn, source); err != nil || got != (BasicContinuation{}) {
				t.Fatal("owner native continuation hit", got, err)
			}
			if got, err := advanceBasicControlRun(tx.ctx, turn, identity, original, facts); err != nil || got != 42 {
				t.Fatal("owner actual run CAS", got, err)
			}
			name = "fresh_receipt_destination_after_owner_run_cas"
		}
		catalogue, err := stmt.Describe(group)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed,
			Implementations: "frozen df0 basicControlFixture/basicControlTurn; " + name + "; owner native populations; synthetic component forecasts, no command/resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(group, g.Receipt()); err != nil {
			t.Fatal("actual exact population receipt", err)
		}
		if len(g.Manifest) != len(group) || len(g.Plans) != len(group) {
			t.Fatal("incomplete population", name)
		}
		for _, p := range g.Plans {
			if len(p.Explain.Values) == 0 {
				t.Fatal("missing actual VM", p.ID)
			}
			vm += len(p.Explain.Values)
			if p.Insert != nil {
				if p.ID != basicControlReceipt || p.Insert.Table != "scan_command_receipts" || p.Insert.Conflict != nil {
					t.Fatal("unexpected receipt destination", p.ID)
				}
				destinations++
			} else if len(p.QueryPlan.Values) == 0 {
				t.Fatal("missing native point/CAS path", p.ID)
			}
			for _, row := range p.QueryPlan.Values {
				if len(row) != 4 || !strings.HasPrefix(row[3].(string), "SEARCH ") {
					t.Fatal("non-SEARCH control path", p.ID, row)
				}
				search++
			}
			t.Logf("%s/%s population=%s VM=%d SEARCH=%d destination=%t", p.ID, p.Cohort, name, len(p.Explain.Values), len(p.QueryPlan.Values), p.Insert != nil)
		}
		if n == 2 {
			if g.ForeignKeys == nil {
				t.Fatal("receipt Capture omitted global FK audit")
			}
			capturedFK, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.CompareExactJSON(fkJSON, capturedFK); err != nil {
				t.Fatal("receipt audit differs from independent acquisition", err)
			}
		}
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
			t.Fatal("Capture changed owner population", before, after, err)
		}
		g.ForeignKeys = nil
		captures = append(captures, population{name, g})
	}
	if destinations != 1 {
		t.Fatal("receipt destination inventory", destinations)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("11IDs/11cohorts/3owner populations/%dVM/%dSEARCH/%dINSERT/686FK2058probes; source/seed journal %s", vm, search, destinations, journal)
	return append(actual, '\n'), referenceRaw
}
