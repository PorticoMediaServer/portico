package workload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func walControlFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SYNTHETIC.sqlite")
	if err := os.WriteFile(path+"-wal", content, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkloadWALActionEmitsPartialAndStopsOnError(t *testing.T) {
	gate, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 20, ReserveBytes: 1 << 16, SoftDebtBytes: 1 << 18, ActivePressureBytes: 1 << 19, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	path := walControlFile(t, []byte("partial"))
	sinkFailure := errors.New("injected sink failure")
	var raw Observation
	err = WAL(path, gate)(t.Context(), Offer{}, func(o Observation) error { raw = o; return sinkFailure })
	if err == nil || !errors.Is(err, sinkFailure) || raw.Phase != "WAL" || raw.CallError == "" || raw.CallStarted == nil || raw.CallReturned == nil {
		t.Fatal("state/sink error or observation lost", err)
	}
	out, ok := raw.Result.(*WALSnapshot)
	if !ok || out.State != nil || len(out.FileBefore.Header) != 7 || raw.Evidence != nil || raw.HoldNS != nil {
		t.Fatal("WAL observation acquired owner evidence")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = WAL(path, gate)(ctx, Offer{}, func(o Observation) error { raw = o; return nil })
	if !errors.Is(err, context.Canceled) || raw.Result != nil || raw.CallStarted != nil || raw.WallNS != nil {
		t.Fatal("canceled pre-read acquired measurements")
	}
}
