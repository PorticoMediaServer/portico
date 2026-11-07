package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func rsPeoplePageCaptures(t *testing.T) []byte {
	t.Helper()
	f := newRSPeoplePageSource(t)
	definitions := append(RSPeoplePageDefinitions(), RSPeopleDirectoryDefinitions()...)
	for _, d := range definitions {
		for _, c := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (c.Name == "present") {
				t.Fatal("incorrect cohort population", d.ID, c.Name, err)
			}
		}
	}
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, rsPeoplePageHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal(err)
		}
		if len(golden.Manifest) != 6 || len(golden.Plans) != 12 {
			t.Fatal("incomplete own statement/cohort inventory")
		}
		captures = append(captures, capture{string(stats), golden})
	}
	data, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Explicit fixture capture for the lane runner. It writes only bounded evidence
// to the runner's artifact directory; it never modifies a baseline or source.
func TestRSPeoplePageCaptureDraft(t *testing.T) {
	data := rsPeoplePageCaptures(t)
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, err := z.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rs-people-page-plans.json.gz"), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRSPeoplePagePlans(t *testing.T) {
	actual := rsPeoplePageCaptures(t)
	data, err := os.ReadFile("testdata/rs-people-page-plans.json.gz")
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
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
