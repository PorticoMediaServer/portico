package workload

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

func TestForegroundEvidenceMissingNativeFactsAreNull(t *testing.T) {
	r := read.BrowseReadResult{Refusal: &governor.Refusal{Code: governor.Capacity}, LookupWindows: []governor.SnapshotResult{{Refusal: &governor.Refusal{Code: governor.Capacity}, NativeReservedBytes: 8 << 20}}}
	e := readEvidence(r)
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"Lookup":null`, `"Publication":null`, `"Settings":null`, `"Started":null`, `"Ended":null`, `"SQLDeadline":null`, `"GoReservedBytes":null`} {
		if !strings.Contains(string(data), field) {
			t.Fatal("missing fact became a value", field, string(data))
		}
	}
	// Preserve the owner-provided known reservation/refusal despite no acquisition.
	w := e.LookupWindows[0]
	if w.Refusal.Code != governor.Capacity || *w.NativeReservedBytes != 8<<20 {
		t.Fatal("lost refusal/reservation")
	}
	now := time.Now()
	w = readWindow(governor.SnapshotResult{Started: now, Ended: now})
	if w.Started == nil || w.Ended == nil || !w.Ended.Equal(*w.Started) {
		t.Fatal("observed zero interval lost")
	}
}
