package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"os"
	"testing"
)

var recordMGLCControlPlans = flag.Bool("record-mg-lc-control-plans", false, "deliberately record LC control plans")

func TestMGLCControlNativeStatementCapture(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	defs := mgLCControlDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("lc-control-canonical"))
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Canonical LC control component; parent owns authority, receipts, generation birth, COMMIT and qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 8 {
		t.Fatal("source plans", len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(g.Plans, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-lc-control-plans.json"
	if *recordMGLCControlPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
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
}
func TestMGLCControlNative(t *testing.T) {
	for _, mode := range []string{"save", "stale", "retarget", "normal", "cached-full", "mismatch", "refs", "retire", "new-collision", "pending-revision", "normal-retire", "cached-full-retire"} {
		t.Run(mode, func(t *testing.T) {
			w, _ := testCanonicalExecutor(t)
			_, err := w.db.ExecContext(t.Context(), `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state) VALUES('channel',1,'old',1,0,'Old','oldhead','ready'); INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,lease_token,lease_until_ms) VALUES('pending','channel',1,'old','f','s','pending','candidates','oldhead',1,100,1,1,1,'keep',999)`)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "mismatch" {
				_, err = w.db.ExecContext(t.Context(), `CREATE TRIGGER mismatch AFTER UPDATE ON lc_channels BEGIN UPDATE lc_channels SET name='wrong' WHERE id=new.id; END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			purpose := MGSetSourceEnabled
			if mode == "normal" || mode == "cached-full" || mode == "normal-retire" || mode == "cached-full-retire" {
				purpose = MGConfigureTuner
			}
			turn := mgSourceTestTurn(t, w, purpose, mgLCControlDefinitions())
			turn.transaction.descriptor.ID = MGSaveLCConfiguration
			b, err := mgLCControlReadChannel(t.Context(), turn, "channel")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cached-full" || mode == "cached-full-retire" {
				turn.transaction.descriptor.Durability = Full
				turn.transaction.settings.Synchronous = int(sqliteconn.Full)
			}
			if mode == "stale" {
				b.revision++
			}
			if mode == "retarget" {
				b.head.String = "other"
			}
			if mode == "refs" {
				_, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO lc_playback_refs VALUES('p','channel','oldhead','o',1,0)`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "new-collision" {
				b = mgLCControlChannel{id: "channel"}
			}
			if mode == "pending-revision" {
				if _, e := turn.transaction.conn.ExecContext(t.Context(), `UPDATE lc_generations SET config_revision=2 WHERE id='pending'`); e != nil {
					t.Fatal(e)
				}
			}
			var out mgLCControlResult
			if mode == "refs" || mode == "retire" || mode == "normal-retire" || mode == "cached-full-retire" {
				turn.transaction.descriptor.ID = MGRetireLCChannel
				out, err = mgRetireLCChannel(t.Context(), turn, b)
			} else {
				out, err = mgSaveLCConfiguration(t.Context(), turn, b, mgLCControlConfig{"new", "New", 0, 2})
			}
			if mode == "save" || mode == "retire" {
				if err != nil || out.channel.head != b.head || out.channel.revision != 2 {
					t.Fatal(out, err)
				}
			} else {
				if err == nil || out != (mgLCControlResult{}) {
					t.Fatal(out, err)
				}
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var status, lease, name string
			if err := w.db.QueryRowContext(t.Context(), `SELECT status,lease_token FROM lc_generations WHERE id='pending'`).Scan(&status, &lease); err != nil || status != "pending" || lease != "keep" {
				t.Fatal(status, lease, err)
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT name FROM lc_channels WHERE id='channel'`).Scan(&name); err != nil || name != "Old" {
				t.Fatal(name, err)
			}
		})
	}
}
