package workload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Fabricated gate/read protocol controls only. These tests do not manufacture
// SQLite writes, owner proof, a reset classifier or a sustained measurement.
type walControlGate struct {
	token, resets  uint64
	event          governor.WALRestartObservation
	order          []string
	duringMetadata func()
}

func (g *walControlGate) Observation() uint64 { g.order = append(g.order, "token"); return g.token }
func (g *walControlGate) ResetCount() uint64 {
	g.order = append(g.order, "resets")
	if g.duringMetadata != nil {
		g.duringMetadata()
	}
	return g.resets
}
func (g *walControlGate) LastRestartObservation() governor.WALRestartObservation {
	g.order = append(g.order, "event")
	return g.event
}

func walControlFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SYNTHETIC.sqlite")
	if err := os.WriteFile(path+"-wal", content, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkloadWALSnapshotBracketsMetadataAndKeepsZero(t *testing.T) {
	path := walControlFile(t, bytes.Repeat([]byte{'a'}, 128))
	now := time.Now()
	gate := &walControlGate{event: governor.WALRestartObservation{Sequence: 1, InitialHeader: true, ObservedAt: now}}
	gate.duringMetadata = func() { gate.token = 2 }
	state := governor.WALState{PhysicalBytes: 128, PageSize: 4096} // explicitly fabricated protocol value
	out, err := observeWAL(path, gate, func(string) (governor.WALState, error) {
		gate.order = append(gate.order, "state")
		return state, nil
	}, time.Now)
	if err != nil || !reflect.DeepEqual(gate.order, []string{"token", "state", "resets", "event", "token"}) {
		t.Fatal(err, gate.order)
	}
	if out.TokenBefore == nil || *out.TokenBefore != 0 || *out.TokenAfter != 2 || *out.TokensEqual || out.ResetCount == nil || *out.ResetCount != 0 {
		t.Fatal("zero observation or metadata race lost")
	}
	if out.LastRestart == nil || !out.LastRestart.InitialHeader || out.LastRestart.Sequence != 1 || out.State == nil || *out.State != state {
		t.Fatal("actual raw metadata was changed")
	}
	if out.ReadReturned.Before(out.ReadStarted) || out.ReadNS != out.ReadReturned.Sub(out.ReadStarted) || out.MetadataNS != out.MetadataReturned.Sub(out.MetadataStarted) {
		t.Fatal("observer durations lost")
	}
	if out.SameFile == nil || !*out.SameFile || out.HeaderEqual == nil || !*out.HeaderEqual || len(out.FileBefore.Header) != 32 {
		t.Fatal("bounded same-file/header observation missing")
	}
}

func TestWorkloadWALSnapshotRetainsPartialHeaderAndNullState(t *testing.T) {
	for _, header := range [][]byte{{}, []byte("partial")} {
		t.Run(string(header), func(t *testing.T) {
			path := walControlFile(t, header)
			out, err := observeWAL(path, &walControlGate{}, governor.ReadWAL, time.Now)
			if err == nil || out.State != nil || out.StateError == "" || out.FileBefore.HeaderError == "" || !bytes.Equal(out.FileBefore.Header, header) {
				t.Fatal("partial read was replaced with zero state")
			}
			if out.FileBefore.Bytes == nil || *out.FileBefore.Bytes != int64(len(header)) || out.HeaderEqual != nil {
				t.Fatal("observed size or incomplete-header status lost")
			}
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["State"] != nil || fields["TokenBefore"] != float64(0) || fields["ResetCount"] != float64(0) {
				t.Fatal("JSON lost null-vs-observed-zero", string(raw))
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	out, err := observeWAL(path, &walControlGate{}, governor.ReadWAL, time.Now)
	if err == nil || out.FileBefore.OpenError == "" || out.FileBefore.Bytes != nil || out.FileBefore.Header != nil || out.SameFile != nil {
		t.Fatal("missing file became observed zero")
	}
}

func TestWorkloadWALSnapshotFileReplacementIsRaw(t *testing.T) {
	path := walControlFile(t, bytes.Repeat([]byte{'a'}, 128))
	out, err := observeWAL(path, &walControlGate{}, func(string) (governor.WALState, error) {
		// Replace a real file at the deterministic parser boundary. Equal gate
		// tokens cannot certify this pair of path reads belongs to one WAL file.
		if err := os.WriteFile(path+".replacement", bytes.Repeat([]byte{'b'}, 64), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".replacement", path+"-wal"); err != nil {
			t.Fatal(err)
		}
		return governor.WALState{}, nil // fabricated; not a parser/authority assertion
	}, time.Now)
	if err != nil || !*out.TokensEqual || out.SameFile == nil || *out.SameFile || out.HeaderEqual == nil || *out.HeaderEqual {
		t.Fatal("file replacement hidden by equal gate tokens")
	}
	if *out.FileBefore.Bytes != 128 || *out.FileAfter.Bytes != 64 || out.LastRestart.Sequence != 0 {
		t.Fatal("replacement was changed into reset evidence")
	}
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
