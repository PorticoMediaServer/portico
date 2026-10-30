package playback

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func deliveryDiagnosticsFixture(t *testing.T) (*Service, *HLS, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	db, err := persistence.Open(filepath.Join(dir, "diagnostics.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Constructors resolve these paths only; no binary or helper is executed.
	h, err := NewHLS(ctx, db, filepath.Join(dir, "private-output"), binary)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db)
	s.ConfigureHLS(h)
	return s, h, cancel
}

func assertRecentFailuresArray(t *testing.T, d DeliveryDiagnostics) {
	t.Helper()
	if d.RecentFailures == nil {
		t.Fatal("successful diagnostics left recentFailures nil")
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"recentFailures":[]`) {
		t.Fatalf("successful diagnostics did not encode an empty failure array: %s", encoded)
	}
}

func TestDeliveryDiagnosticsConfigurationAndOwnership(t *testing.T) {
	var absent *Service
	d, err := absent.DeliveryDiagnostics(context.Background())
	if err != nil || d.DirectConfigured || d.HLSConfigured || d.ActiveConversionSessions != nil || d.ConversionSessionLimit != nil {
		t.Fatal("absent service fabricated availability", d, err)
	}
	assertRecentFailuresArray(t, d)
	s, h, cancel := deliveryDiagnosticsFixture(t)
	direct := New(s.db)
	d, err = direct.DeliveryDiagnostics(context.Background())
	if err != nil || !d.DirectConfigured || d.HLSConfigured || d.ConversionSessionLimit != nil {
		t.Fatal("direct-only service", d, err)
	}
	assertRecentFailuresArray(t, d)
	h.mu.Lock()
	h.active["private-source-secret"] = func() {}
	if h.windows == nil {
		h.windows = map[string]*hlsWindow{}
	}
	h.windows["private-source-secret"] = &hlsWindow{videoConversion: true}
	h.mu.Unlock()
	d, err = s.DeliveryDiagnostics(context.Background())
	if err != nil || !d.HLSConfigured || d.FiniteHLSConfigured || d.Lifecycle != "running" || d.ActiveConversionSessions == nil || *d.ActiveConversionSessions != 1 || d.ConversionSessionLimit != nil || d.ConversionLimitSource != "owner_policy" {
		t.Fatal(d, err)
	}
	assertRecentFailuresArray(t, d)
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{h.binary, h.root, "private-source-secret"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("diagnostic leaked private runtime identity")
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = h.EnableFinite(binary); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeliveryDiagnostics(context.Background())
	if err != nil || !d.FiniteHLSConfigured {
		t.Fatal(d, err)
	}
	assertRecentFailuresArray(t, d)
	cancel()
	d, err = s.DeliveryDiagnostics(context.Background())
	if err != nil || d.Lifecycle != "stopping" || d.ActiveConversionSessions == nil || *d.ActiveConversionSessions != 1 {
		t.Fatal("cancellation fabricated worker retirement", d, err)
	}
	assertRecentFailuresArray(t, d)
	// Snapshot values are copies, not writable aliases into admission accounting.
	*d.ActiveConversionSessions = 99
	next, err := s.DeliveryDiagnostics(context.Background())
	if err != nil || *next.ActiveConversionSessions != 1 {
		t.Fatal("snapshot mutated runtime")
	}
}

func TestDeliveryDiagnosticsContentionAndCancellation(t *testing.T) {
	s, h, _ := deliveryDiagnosticsFixture(t)
	h.mu.Lock()
	d, err := s.DeliveryDiagnostics(context.Background())
	h.mu.Unlock()
	if !errors.Is(err, ErrDeliveryDiagnosticsBusy) || d.HLSConfigured || d.ActiveConversionSessions != nil {
		t.Fatal("contended read claimed freshness", d, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.DeliveryDiagnostics(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled observation proceeded", err)
	}
}
