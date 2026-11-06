package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type n1ComponentRestart struct {
	Close                          n1Interval `json:"joined_executor_close"`
	Reopen                         n1Interval `json:"canonical_opening"`
	SameFile                       *bool      `json:"same_native_file"`
	OldNativeJoined                bool       `json:"old_native_joined"`
	NewBindingChecked              bool       `json:"new_native_file_binding_checked"`
	OpeningConnectionsOutsideTrace int        `json:"opening_connections_outside_measured_trace"`
	Complete                       bool       `json:"complete"`
	Error                          string     `json:"error"`
	Scope                          string     `json:"scope"`
}

// Existing W opening and ONE factored owner builder only. This is a COMPONENT
// restart of a synthetic fixture, not production lease/issuer re-establishment.
// The old executor is joined and stays closed; no flag or SQL state is reset.
func n1RestartComponent(t *testing.T, f *inventoryProgressiveFixture, trace *n1Trace, origin time.Time) *n1ComponentRestart {
	t.Helper()
	out := &n1ComponentRestart{Scope: "component-only canonical close/reopen; original reference/governor/recipe retained, no production fresh lease authority"}
	old := f.owner.executor
	before, err := os.Stat(f.owner.path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if err = f.committed(f.turn.transaction); err != nil {
		out.Error = err.Error()
		return out
	}
	start := time.Now()
	err = old.close()
	returned := time.Now()
	out.Close = n1IntervalBetween(n1NS(origin, start), n1NS(origin, returned), "actual old executor close; join independently checked")
	stats := old.db.Stats()
	out.OldNativeJoined = err == nil && old.closed && stats.InUse == 0 && stats.OpenConnections == 0 && trace.connections == trace.closed && f.owner.authority.Usage() == ([4]governor.Amounts{})
	if !out.OldNativeJoined {
		out.Error = n1Join("old native join incomplete", err)
		return out
	}
	owner := scanOpeningOwner(t, f.owner.path)
	start = time.Now()
	opening := openScanFixture(t, f.owner.path, owner) // actual existing-file W constructor/binding
	returned = time.Now()
	out.Reopen = n1IntervalBetween(n1NS(origin, start), n1NS(origin, returned), "actual W canonical opening; setup native connection is outside measured decorator trace")
	after, err := os.Stat(f.owner.path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	same := os.SameFile(before, after)
	out.SameFile = &same
	if !same {
		out.Error = "restart replaced canonical file"
		return out
	}
	w := opening.executor
	if err = w.fileBinding.check(); err != nil {
		out.Error = err.Error()
		return out
	}
	out.NewBindingChecked = true
	// Initial W opening is unchanged. Reuse the existing test-only openPool
	// decorator seam only AFTER joining that unobserved setup connection.
	out.OpeningConnectionsOutsideTrace = w.db.Stats().OpenConnections
	if err = w.db.Close(); err != nil {
		out.Error = err.Error()
		return out
	}
	stats = w.db.Stats()
	w.physicalFaults.mu.Lock()
	failed := w.physicalFaults.failed
	w.physicalFaults.mu.Unlock()
	if stats.InUse != 0 || stats.OpenConnections != 0 || failed {
		out.Error = "initial opening pool not joined or sticky native close fault"
		return out
	}
	connector, err := sqliteconn.Connector(f.owner.path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	w.openPool(connector, trace.decorate)
	w.now = old.now
	f.owner.executor = w
	registry := nativeInventoryFixtureRegistry(t, w, inventoryCommandDefinitions(), "scan_fresh_test.go")
	previous, _ := json.Marshal(f.owner.registry.Manifest())
	current, _ := json.Marshal(registry.Manifest())
	a, b := sha256.Sum256(previous), sha256.Sum256(current)
	if a != b {
		out.Error = "restart registry changed: " + hex.EncodeToString(b[:])
		return out
	}
	f.owner.registry = registry
	if err = errors.Join(w.fileBinding.check(), f.joined()); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Complete = true
	return out
}
