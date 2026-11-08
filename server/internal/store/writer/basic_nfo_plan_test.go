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

var recordBasicNFOPlans = flag.Bool("record-basic-nfo-plans", false, "deliberately record closed Basic NFO native component plans")

// Native NFO effect-fact populations only. No complete Basic command, receipt,
// source authority, production admission or physical budget is qualified here.
// The journal freezes the two Basic NFO provider files plus the exact NFO seed
// and the base stream/registry fixture spans this population actually uses.
func TestBasicNFOStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"basic_nfo.go", "0a93739a5bfc24e94043ae7fb076473c440e32d6e32884638ba33c95e220e3c7"},
		{"basic_nfo_statements.go", "a69cc4fe9f54fcc9f206e68acea09582bd80efd32b67582d60966fce877b1ab0"},
		{"basic_nfo_test.go", "72a565e95656084ad96f883ab247f3be716a35567922b56d4573cfbbad380621"},
		{"basic_stage_test.go", "8e8da49da22747d1d709405398198f8c3fd46400bc63c9fb434758c553a17966"},
		{"basic_probe_test.go", "77de9ba74cce92bed2665241dc27c0352c62785532de5888b853b4569d643c97"},
		{"basic_media_stage_test.go", "22e19f73b2d0bf0fca6f6a7040a71314bc9f0657d5f3ae884abcbd02d3acf1a6"},
		{"native_registry_test.go", "27c84869c377c70f0332fbaedc6c621cb0a07a8a768012f7bb10cca457d19e50"},
	}
	h := sha256.New()
	for _, p := range parts {
		b, err := os.ReadFile(p.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != p.sha {
			t.Fatal("frozen Basic NFO source changed; review before recording", p.name)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(p.name))))
		h.Write([]byte(p.name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
		h.Write(b)
	}
	journal := hex.EncodeToString(h.Sum(nil))
	defs := basicNFODefinitions()
	wantIDs := []stmt.ID{basicNFOLibrary, basicNFORows, basicNFOStatusRead, basicNFODelete, basicNFOInsert, basicNFOStatusInsert, basicNFOStatusCAS}
	if len(defs) != len(wantIDs) {
		t.Fatal("closed NFO inventory", len(defs))
	}
	for n, d := range defs {
		if d.ID != wantIDs[n] || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot literal cohort", n, d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w, _ := basicStreamFixture(t)
	// Execute the exact seeded before row/status; the literal delete cohort and
	// the complete before-set read must bind to this real row, not a fabricated
	// EXPLAIN-only population.
	seeded := seedBasicNFO(t, w)
	if seeded != (basicNFORow{"locator-1", "movie", "[null,null,null]", "digest-1", `{"title":"Old"}`, 42, -11, "old-time"}) {
		t.Fatal("seeded NFO row drift", seeded)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
	closeErr := conn.Close()
	if assertErr != nil || closeErr != nil || startup.Synchronous != 2 {
		t.Fatal(startup, assertErr, closeErr)
	}
	t.Logf("Basic NFO FULL fixture startup: %+v", startup)
	// Existing owner helper selects NORMAL on W's private synthetic component
	// turn and already registers its complete stream-plus-seven-ID set.
	turn := basicProbeTestTurn(t, w, basicNFOTestDefs())
	tx := turn.transaction
	config := w.config
	config.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Basic NFO NORMAL capture boundary: %+v", settings)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	type population struct {
		Name   string      `json:"name"`
		Golden plan.Golden `json:"golden"`
	}
	captures := []population{}
	var fkJSON []byte
	vm, search, inserts := 0, 0, 0
	for _, group := range [][]stmt.Definition{defs} {
		name := "original_nfo_vector"
		catalogue, err := stmt.Describe(group)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "frozen seedBasicNFO and base stream/registry spans; " + name + "; synthetic W component turn/forecasts; no command or resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(group, g.Receipt()); err != nil {
			t.Fatal("exact population Capture receipt", err)
		}
		// Independently execute the full destination/FK audit and require exact
		// equality with the Capture-attached image. INSERT attaches automatically.
		independent, err := cachedForeignKeys(tx.ctx, tx.conn, config)
		if err != nil {
			t.Fatal(err)
		}
		probes := len(independent.Plans) * 3
		if len(independent.Plans) != 686 || probes != 2058 {
			t.Fatal("incomplete independent FK audit", len(independent.Plans), probes)
		}
		if g.ForeignKeys == nil {
			g.ForeignKeys = &independent
		} else {
			captured, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			audited, err := json.MarshalIndent(independent, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.CompareExactJSON(audited, captured); err != nil {
				t.Fatal("Capture FK differs from independent audit", err)
			}
		}
		if len(g.Manifest) != len(group) || len(g.Plans) != len(group) || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
			t.Fatal("incomplete Basic NFO statement/destination/FK audit")
		}
		for _, p := range g.Plans {
			if len(p.Explain.Values) == 0 {
				t.Fatal("missing actual VM", p.ID)
			}
			vm += len(p.Explain.Values)
			if p.Insert != nil {
				// A plain INSERT has no destination SEARCH in native EQP. Retain
				// only its compiled destination evidence; do not invent a seek.
				switch p.ID {
				case basicNFOInsert:
					if p.Insert.Table != "video_nfo_evidence" {
						t.Fatal("unexpected INSERT destination", p.ID, p.Insert.Table)
					}
				case basicNFOStatusInsert:
					if p.Insert.Table != "video_nfo_status" {
						t.Fatal("unexpected INSERT destination", p.ID, p.Insert.Table)
					}
				default:
					t.Fatal("unexpected INSERT plan", p.ID)
				}
				if len(p.QueryPlan.Values) != 0 {
					t.Fatal("plain INSERT must not claim a seek", p.ID, p.QueryPlan.Values)
				}
				inserts++
				continue
			}
			if len(p.QueryPlan.Values) == 0 {
				t.Fatal("missing native plan", p.ID)
			}
			for _, row := range p.QueryPlan.Values {
				if len(row) != 4 || !strings.HasPrefix(row[3].(string), "SEARCH ") {
					t.Fatal("unexpected native plan", p.ID, row)
				}
				search++
			}
		}
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
			t.Fatal("Capture changed population", before, after, err)
		}
		fk, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if fkJSON == nil {
			fkJSON = fk
		} else if err := plan.CompareExactJSON(fkJSON, fk); err != nil {
			t.Fatal("population changed global FK audit", err)
		}
		g.ForeignKeys = nil
		captures = append(captures, population{name, g})
	}
	if inserts != 2 {
		t.Fatal("INSERT destination inventory", inserts)
	}
	reference := readScanFreshFK(t, filepath.Join("testdata", "scan-basic-media-fk.json.gz"))
	if err := plan.CompareExactJSON(reference, fkJSON); err != nil {
		t.Fatal("Basic NFO changed existing complete686-FK image", err)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-basic-nfo.json")
	fkPath := filepath.Join("testdata", "scan-basic-nfo-fk.json.gz")
	if *recordBasicNFOPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err := z.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, b.Bytes(), 0644); err != nil {
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
	if err := plan.CompareExactJSON(readScanFreshFK(t, fkPath), fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("7IDs/7cohorts/one actual NFO population/%dVM/%dSEARCH/2INSERT/686FK2058probes; seed journal%s; no Basic admission/authority/HOLD qualification", vm, search, journal)
}
