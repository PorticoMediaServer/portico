package httpapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func TestDeliveryAcceptingReadUsesStoredTransactionNotDiagnosticCache(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adapter := &RegistryDeliverySettings{Console: f.deps.Console, loaded: true, loadedAt: time.Now(), snapshot: telemetry.TranscodeSettings{HardwareBackend: "cached-hardware"}}
	checks := 0
	adapter.Authorize = func(check context.Context, tx *sql.Tx, _ string) error {
		checks++
		if dbwork.Snapshot(check) != tx {
			t.Error("settings authority detached from accepting transaction")
		}
		return nil
	}
	validations := 0
	prior := f.deps.Console.FFmpegValidation
	f.deps.Console.FFmpegValidation = func(context.Context, string) operations.FFmpegStatus {
		validations++
		return operations.FFmpegStatus{}
	}
	t.Cleanup(func() { f.deps.Console.FFmpegValidation = prior })
	writer, err := dbwork.Begin(ctx, f.deps.DB, dbwork.ClassEstablishedPlayback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = writer.Tx().ExecContext(writer.Context(), `INSERT INTO server_settings(singleton,"hardwareBackend") VALUES(1,'software') ON CONFLICT(singleton) DO UPDATE SET "hardwareBackend"='software'`); err != nil {
		t.Fatal(err)
	}
	actual, err := adapter.DeliveryTx(writer.Context(), writer.Tx())
	if err != nil || actual.HardwareBackend != "software" || checks != 1 || validations != 0 {
		t.Fatal(actual, err, checks, validations)
	}
	if adapter.snapshot.HardwareBackend != "cached-hardware" {
		t.Fatal("acceptance rewrote diagnostics")
	}
}
