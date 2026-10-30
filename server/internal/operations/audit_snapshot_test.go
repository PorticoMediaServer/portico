package operations

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"testing"
)

// C74: reading the audit log and downloading a support export each record an
// audit entry. That write used to run inside the read snapshot, so both calls
// always failed. They now read on the snapshot and audit in a separate gated
// write, and the entry is present afterwards.
func TestAuditLogReadAndExportDownloadAreAuditedOutsideTheSnapshot(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	count := func(action string) int {
		t.Helper()
		var n int
		if e := s.DB.QueryRow(`SELECT count(*) FROM console_audit WHERE action=?`, action).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if _, e := s.Records(ctx, p, a, "audit", ""); e != nil {
		t.Fatalf("audit-log read failed: %v", e)
	}
	if count("audit.read") != 1 {
		t.Fatal("audit-log read was not audited")
	}
	created, e := s.CreateExport(ctx, p, a, false, ExportRequest{IdempotencyKey: "c74-export", From: apiwire.Instant(s.now() - 1000), To: apiwire.Instant(s.now()), Components: []string{"runtime", "audit"}})
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Export(ctx, p, a, created.ID)
	if e != nil {
		t.Fatalf("export download failed: %v", e)
	}
	if got.ID != created.ID {
		t.Fatalf("downloaded export %q, want %q", got.ID, created.ID)
	}
	if count("support.download") != 1 {
		t.Fatal("export download was not audited")
	}
}
