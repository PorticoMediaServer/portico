package writer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestCanonicalWCErrorObservationKeepsPublicZeroAndCallBinding(t *testing.T) {
	origin := time.Now()
	entered, returned := origin.Add(time.Nanosecond), origin.Add(2*time.Nanosecond)
	view := ownerWindowObservation{ownerWindow: ownerWindow{commandID: SecurityAccept, setupEnteredAt: entered, terminalReturnedAt: returned}}
	e := &executor{}
	e.ownerObservation.Store(&view)
	gate, err := governor.NewWALGate(canonicalN1Maintenance().policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "error.ndjson")
	file, err := openFirstLightOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	trace := newN1Trace(origin, false)
	c := &CanonicalWCComponent{Writer: &Writer{effective: &effectiveRuntime{executor: e, path: filepath.Join(t.TempDir(), "absent.db"), wal: gate}}, trace: trace, stream: &n1Stream{file: file, origin: origin, base: map[string]any{}}, currentID: "original1", purpose: SecurityAccept}
	request := ReceiptReference{Identity: CommandIdentity{Sequence: 7}}
	start, end := origin, returned.Add(time.Nanosecond)
	if err := c.Observe(CanonicalWCObservation{Phase: "ACCEPT", Started: &start, Returned: &end, Requested: &request, Error: "unknown native outcome"}); err == nil {
		t.Fatal("error became passing")
	}
	// A later call must not borrow the old failed frame, even for the same purpose.
	start = end.Add(time.Nanosecond)
	end = start.Add(time.Nanosecond)
	if err := c.Observe(CanonicalWCObservation{Phase: "RESOLVE", Started: &start, Returned: &end, Requested: &request, Error: "still unknown"}); err == nil {
		t.Fatal("partial became passing")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatal(string(raw))
	}
	for index, line := range lines {
		var v struct {
			Evidence   *ExecutionEvidence `json:"public_evidence"`
			Owner      *n1OwnerWindow     `json:"owner_window"`
			Diagnostic map[string]any     `json:"private_diagnostic"`
			Requested  *ReceiptReference  `json:"requested_reference"`
			Clock      n1Clock            `json:"owner_clock"`
			Ordinal    *int               `json:"actual_physical_attempt_ordinal"`
		}
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatal(err)
		}
		if v.Evidence != nil || v.Clock.HoldNS != nil || v.Clock.CommitNS != nil || v.Ordinal != nil || v.Requested == nil || *v.Requested != request {
			t.Fatal("manufactured physical evidence/reference", line)
		}
		if index == 0 {
			if v.Owner == nil || !v.Owner.Bound || v.Diagnostic == nil || v.Diagnostic["policy_verified"] != nil || v.Diagnostic["actual_settings"] != nil {
				t.Fatal("lost private partial / invented policy", line)
			}
		} else if v.Owner != nil || v.Diagnostic != nil {
			t.Fatal("stale frame borrowed by Resolve", line)
		}
	}
}
