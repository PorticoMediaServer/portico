package diagnostics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "diagnostics.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	_, e = db.Exec(`CREATE TABLE libraries(id TEXT,name TEXT,kind TEXT,root TEXT);CREATE TABLE jobs(id TEXT,library_id TEXT,status TEXT,processed INTEGER,error TEXT,created_at TEXT);CREATE TABLE job_observations(job_id TEXT,updated_at TEXT,finished_at TEXT);CREATE INDEX jobs_status_directory ON jobs(status,created_at DESC,id DESC);CREATE TABLE managed_mounts(name TEXT,observed TEXT,mount_path TEXT,error TEXT,remote TEXT);INSERT INTO libraries(id,name,kind,root) VALUES('private-id','PRIVATE MEDIA TITLE','movie','/private/SECRET-PATH');INSERT INTO managed_mounts VALUES('SECRET MOUNT','failed','/private/MOUNT','credential=SECRET-TOKEN','https://SECRET-HOST');`)
	if e != nil {
		t.Fatal(e)
	}
	return db
}
func TestSupportReportBoundsAndNeverSelectsSensitiveFields(t *testing.T) {
	db := fixture(t)
	for n := 0; n < 43; n++ {
		code := "raw SECRET-TOKEN /private/SECRET-PATH signed_url"
		if n == 42 {
			code = "scan_source_unavailable"
		}
		at := time.Date(2026, 9, 5, 0, n, 0, 0, time.UTC).Format(time.RFC3339)
		if _, e := db.Exec(`INSERT INTO jobs VALUES(?,?,?,?,?,?)`, fmt.Sprint(n), "private-id", "failed", n, code, at); e != nil {
			t.Fatal(e)
		}
	}
	report, e := Read(context.Background(), db, false)
	if e != nil {
		t.Fatal(e)
	}
	if report.Scans.Total != 43 || report.Libraries.Total != 1 || report.Mounts.Counts.Total != 1 || len(report.RecentScanFailures.Items) != 20 || !report.RecentScanFailures.HasMore || report.RecentScanFailures.Items[0].Code != "source_unavailable" {
		t.Fatal(report)
	}
	raw, e := json.Marshal(report)
	if e != nil || len(raw) > MaxReportBytes {
		t.Fatal(e, len(raw))
	}
	for _, secret := range []string{"SECRET", "PRIVATE MEDIA", "private-id", "signed_url", "/private/", "https://", "accountId", "viewerFence", "mount_path", "remote", "accessToken"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("sensitive report value", secret)
		}
	}
}
func TestSupportReportUnknownAndMissingValuesAreExplicit(t *testing.T) {
	db := fixture(t)
	_, e := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('other','Private','secret-kind','secret-path');INSERT INTO jobs VALUES('bad','other','secret-status',-3,'private error','invalid SECRET date');INSERT INTO jobs VALUES('failed','other','failed',-4,'private error','invalid SECRET date');DROP TABLE managed_mounts;`)
	if e != nil {
		t.Fatal(e)
	}
	report, e := Read(context.Background(), db, true)
	if e != nil {
		t.Fatal(e)
	}
	if report.Libraries.ByState["unknown"] != 1 || report.Scans.ByState["unknown"] != 1 || report.Mounts.Available || report.Mounts.Counts != nil || report.RecentScanFailures.Items[0].CreatedAt != nil || report.RecentScanFailures.Items[0].Processed != nil {
		t.Fatal(report)
	}
	if _, e = time.Parse(time.RFC3339Nano, report.ObservedAt); e != nil {
		t.Fatal(e)
	}
	if token("https://private/path") != nil || timestamp("SECRET") != nil {
		t.Fatal("unsafe build/timestamp passed")
	}
}
func TestSupportReportFailureAndCancellationNeverReturnSuccess(t *testing.T) {
	db := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Read(ctx, db, false); e == nil {
		t.Fatal("cancelled read succeeded")
	}
	if _, e := db.Exec(`DROP TABLE jobs`); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(context.Background(), db, false); e != ErrUnavailable {
		t.Fatal(e)
	}
	if _, e := Read(context.Background(), nil, false); e != ErrUnavailable {
		t.Fatal(e)
	}
}
