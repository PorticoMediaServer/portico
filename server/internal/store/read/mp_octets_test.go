package read

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func mpOctetSource(t *testing.T) *browseFixture {
	t.Helper()
	f := mpQueueSource(t)
	f.exec(t, mpSessionSeed)
	f.exec(t, mpPreparedSeed)
	return f
}
func TestMPOctetNativeWidthsAndClosedPoints(t *testing.T) {
	f := mpOctetSource(t)
	f.exec(t, "UPDATE queues_v1 SET current_entry_id=? WHERE id='mp-queue'", "é\x00next")
	f.exec(t, "UPDATE playback_v1_sessions SET end_reason=? WHERE id='mp-session'", "漢字\x00end")
	f.exec(t, "UPDATE prepared_media_jobs SET phase=? WHERE id='mp-job'", "Émission\x00encoding")
	defs := MPByteDefinitions()
	for n, d := range defs {
		var wanted string
		column := 1
		switch n {
		case 0:
			wanted = "é\x00next"
		case 1:
			wanted = "漢字\x00end"
			column = 8
		case 2:
			wanted = "Émission\x00encoding"
			column = 2
		}
		rows, err := f.conn.QueryContext(t.Context(), d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for k := range dest {
			dest[k] = &values[k]
		}
		if !rows.Next() {
			t.Fatal("no native length row")
		}
		if err = rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if values[column] != int64(len(wanted)) {
			t.Fatal("not native byte width", values[column], len(wanted))
		}
	}
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*stmt.Definition){
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(current_entry_id)", "octet_length(request)", 1)
		},
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, " WHERE id=?", "", 1) },
	} {
		d := defs[0]
		mutation(&d)
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			continue
		}
		if _, err = plan.Capture(t.Context(), conn, r, browsePlanFixture(cfg, foregroundFixtureHash(t), plan.Unanalyzed), policy); err == nil {
			t.Fatal("nonclosed octet point certified")
		}
	}
}

var recordMPOctetPlans = flag.Bool("record-mp-octet-plans", false, "record MP queue native plans")

func TestMPOctetPlans(t *testing.T) {
	f := mpOctetSource(t)
	defs := MPByteDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (c.Name == "present") {
				t.Fatal("cohort", d.ID, c.Name, err)
			}
		}
	}
	catalog, err := stmt.Describe(defs)
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
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+mpQueueSeed)))
	var captures []plan.Golden
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if statistics == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalog, browsePlanFixture(cfg, hash, statistics), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(defs, golden.Receipt()); err != nil {
			t.Fatal(err)
		}
		if len(golden.Manifest) != 3 || len(golden.Plans) != 6 {
			t.Fatal("incomplete capture")
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mp-octet-plans.json.gz"
	if *recordMPOctetPlans {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
