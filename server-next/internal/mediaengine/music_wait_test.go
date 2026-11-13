package mediaengine

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A cold start never waits for measurement. A later ready plan can use the result.
func TestColdMusicStartsWhileMeasurementRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m := &measuredMusic{done: make(chan struct{})}
	e := Engine{music: map[string]*measuredMusic{"track\x000\x000": m}}
	got, ok := e.measured(ctx, 1, "track", db.FileOnDisk{})
	if got != m || ok {
		t.Fatal("a pending measurement must be returned without waiting")
	}
	close(m.done)
	got, ok = e.measured(ctx, 1, "track", db.FileOnDisk{})
	if got != m || !ok {
		t.Fatal("the completed result was not available to the next plan")
	}
}
