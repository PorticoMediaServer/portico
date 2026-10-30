package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestManualBackupDuringScheduledCopyReturnsRetryableBusy(t *testing.T) {
	f := administrationFixtureNew(t)
	svc := backup.New(t.TempDir(), f.deps.DB, "server", "test", persistence.SchemaVersion())
	f.deps.Backups = svc
	f.handler = New(f.deps)
	entered := make(chan struct{})
	release := make(chan struct{})
	svc.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error { close(entered); <-release; return nil })
	id, err := svc.Start(context.Background(), backup.KindScheduled, "scheduled-http-probe")
	if err != nil {
		t.Fatal(err)
	}
	job, _ := svc.Job(id)
	defer func() { close(release); <-job.Done() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled copy did not start")
	}
	response := f.call("POST", "/v1/admin/backups", f.owner, `{"operationId":"manual-http-probe"}`)
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":"request_in_progress"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/admin/backups", response)
}
