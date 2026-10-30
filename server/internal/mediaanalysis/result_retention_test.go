package mediaanalysis

import (
	"context"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestResultSummaryRetentionIsBoundedAndWaitsForReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insert := func(id string, retired int) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms,retired_ms) VALUES(?,?,'a','r','i',1,1,'probe','v','e','t',1000,'{}',0,?)`, id, id, retired)
		if err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 40; n++ {
		insert(fmt.Sprintf("old%02d", n), 1)
	}
	insert("live", 0)
	insert("recent", 100)
	insert("headed", 1)
	insert("artifact", 1)
	if _, err = db.Exec(`INSERT INTO analysis_heads VALUES('headed','r','probe','headed')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO analysis_artifacts(id,result_id,digest,size,kind,mime,ordinal,start_us,end_us) VALUES('object','artifact','digest',1,'waveform','application/json',0,0,1)`); err != nil {
		t.Fatal(err)
	}
	s := &Service{db: db}
	count, err := s.pruneResultSummaries(context.Background(), 50)
	if err != nil || count != 32 {
		t.Fatalf("batch %d: %v", count, err)
	}
	count, err = s.pruneResultSummaries(context.Background(), 50)
	if err != nil || count != 8 {
		t.Fatalf("remainder %d: %v", count, err)
	}
	var retained int
	if err = db.QueryRow(`SELECT count(*) FROM analysis_results`).Scan(&retained); err != nil || retained != 4 {
		t.Fatalf("retained %d: %v", retained, err)
	}
}
