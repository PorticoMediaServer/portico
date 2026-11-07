package writer

import (
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// Unpublished draft-only fixture. Remove the overlay when INT embeds IP-001;
// ordinary CI cannot mistake a skipped overlay for adopted-schema evidence.
func ipDraftExecutor(t *testing.T) *executor {
	t.Helper()
	path := os.Getenv("PORTICO_IP_DDL_DRAFT")
	if path == "" {
		t.Skip("IP DDL is a local draft; set PORTICO_IP_DDL_DRAFT for component probes")
	}
	if schema.SHA256 != "da7841e26dd347f65554466139dd8eae0f181c9250715dc26d462e0e33b3f1c4" {
		t.Fatal("reassess IP draft against changed canonical schema")
	}
	ddl, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _ := testExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal("draft delta load:", err)
	}
	return w
}

func TestIPDraftDeltaLoadsAndForeignKeys(t *testing.T) {
	w := ipDraftExecutor(t)
	rows, err := w.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("draft foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	for _, query := range []string{
		`EXPLAIN QUERY PLAN SELECT id FROM metadata_person_operations INDEXED BY metadata_person_operation_due WHERE job_due_ms IS NOT NULL AND job_due_ms<=17 ORDER BY job_due_ms,id LIMIT 8`,
		`EXPLAIN QUERY PLAN SELECT id FROM metadata_edit_owners INDEXED BY metadata_edit_owner_reclaim WHERE receipt_reclaim_due_ms IS NOT NULL AND receipt_reclaim_due_ms<=17 ORDER BY receipt_reclaim_due_ms,id LIMIT 8`,
		`EXPLAIN QUERY PLAN SELECT ordinal FROM artwork_discovery_candidates WHERE stage_id=1 AND ordinal>7 ORDER BY ordinal LIMIT 16`,
		`EXPLAIN QUERY PLAN SELECT command_sequence FROM metadata_edit_receipts WHERE owner_id=1 AND command_incarnation=zeroblob(16) AND command_sequence=1`,
		`EXPLAIN QUERY PLAN SELECT entity_id FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider='tmdb' AND purpose='metadata' AND entity_id>0 ORDER BY entity_id LIMIT 16`,
	} {
		plan, err := w.db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for plan.Next() {
			var id, parent, unused int
			var detail string
			if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatal("unbounded draft path:", detail)
			}
			found = found || strings.Contains(detail, "SEARCH ")
		}
		if err := plan.Err(); err != nil {
			t.Fatal(err)
		}
		if err := plan.Close(); err != nil || !found {
			t.Fatal("missing SEARCH", err)
		}
	}
}
