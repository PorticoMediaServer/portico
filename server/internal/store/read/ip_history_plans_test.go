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
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordIPHistoryPlans = flag.Bool("record-ip-history-plans", false, "record IP owner/history native plan golden")

func ipHistoryPlanFixtureHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+"|ip-history|"+fmt.Sprint(len(ipHistorySeed)))))
}

func TestIPHistoryPlans(t *testing.T) {
	f := ipHistorySource(t)
	definitions := IPHistoryHarnessDefinitions()
	if len(definitions) != 12 {
		t.Fatal("unexpected IP history statement inventory", len(definitions))
	}
	for _, definition := range definitions {
		for _, cohort := range definition.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), definition.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (cohort.Name == "present") {
				t.Fatal("mislabeled cohort", definition.ID, cohort.Name, err)
			}
		}
	}
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, ipHistoryPlanFixtureHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("native Capture failed to authorize exact definitions", err)
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/ip-history-plans.json.gz"
	if *recordIPHistoryPlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
