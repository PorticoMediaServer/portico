package governor

import (
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Opt-in native timing diagnostic, never a default functional/CI gate. Run in a
// scheduled quiet window or retain shared-host variance explicitly. This tiny
// fixture cannot qualify the sustained-command WAL/reset/service matrix.
func TestDiagnosticPassiveNativeDeadline(t *testing.T) {
	if os.Getenv("PORTICO_GV_NATIVE_TIMING") != "1" {
		t.Skip("opt-in physical timing: PORTICO_GV_NATIVE_TIMING=1")
	}
	path, _ := snapshotFixture(t)
	g, a, _ := nativeCheckpointGovernor(t)
	g.(*ledger).config.Now = time.Now
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := NewCheckpointer(connector, cfg, g, a, CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cp.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 2; i++ {
		out, err := cp.Step(t.Context(), nativeCheckpointRequest(g))
		t.Logf("sample=%d physical=%s native=%s result=%+v err=%v", i, out.PhysicalReturned.Sub(out.Started), out.NativeEnded.Sub(out.NativeStarted), out, err)
		if err != nil || out.Refusal != nil || !out.PhysicalSafe {
			t.Fatal("native timing diagnostic failed; retain this sample", err, out.Refusal)
		}
		if i == 0 {
			time.Sleep(max(0, time.Until(out.Requested.Add(out.EligibleInterval))))
		}
	}
}
