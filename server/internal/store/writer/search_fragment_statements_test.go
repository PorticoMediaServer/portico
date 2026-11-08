package writer

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSearchFragmentStatementsCaptureAndBindClosedEffects(t *testing.T) {
	w := searchImageStagePlanFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := searchFragmentStageDefinitions()
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
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: schema.SHA256, Statistics: plan.Unanalyzed,
		Implementations: "fragment statement component only; shared image fixture; no fresh source/resource/accepting or native Work qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture executed staged effects", before, after, err)
	}
	if len(defs) != 14 || len(g.Plans) != 17 || g.ForeignKeys == nil {
		t.Fatal("wrong closed component inventory", len(defs), len(g.Plans))
	}
	ordinary, native := 0, 0
	for _, p := range g.Plans {
		if p.Insert != nil {
			ordinary++
		}
		if p.NativeInsert != nil {
			native++
		}
	}
	if ordinary != 2 || native != 6 {
		t.Fatal("missing ordinary/native per-column audits", ordinary, native)
	}
	r, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	base, rows := searchFragmentStageSnapshotFixture(t)
	for _, nullable := range []struct{ head, due bool }{{false, false}, {true, true}, {false, true}, {true, false}} {
		s := base
		if !nullable.head {
			s.job.publishedImage, s.job.publishedGeneration = sql.NullInt64{}, 0
		}
		if !nullable.due {
			s.job.nextAttempt = sql.NullInt64{}
		}
		c, err := newSearchFragmentStageCommand(CommandIdentity{s.job.incarnation, s.job.lastSequence + 1, [16]byte{71}}, searchFragmentStageRequest{"fragment_stage", s, rows[0].row})
		if err != nil {
			t.Fatal(err)
		}
		next, err := planSearchFragmentStage(s, c.payload)
		if err != nil {
			t.Fatal(err)
		}
		id, values := searchFragmentJobValues(c, 501, next)
		statement, err := r.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := statement.Bind(values...); err != nil {
			t.Fatal("closed nullable CAS has wrong bindings", id, err)
		}
		d, _ := statement.Definition()
		for n, param := range d.Params {
			switch param.Name {
			case "next_revision":
				if values[n] != s.job.revision+1 {
					t.Fatal("wrong owner revision")
				}
			case "lease_owner":
				if values[n] != s.job.leaseOwner {
					t.Fatal("lost captured lease")
				}
			case "ack_sequence":
				if values[n] != s.job.acknowledgedSequence {
					t.Fatal("lost unacknowledged-proof fence")
				}
			}
		}
	}
	for _, d := range defs {
		if d.ID == searchFragmentSealTotals {
			set := strings.Split(strings.Split(d.SQL, " SET ")[1], "\nWHERE")[0]
			if set != "staged_fragments=?,staged_bytes=?" {
				t.Fatal("fragment staging mutated seal verification", set)
			}
		}
	}
}
