package httpapi

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestCurrentWriterCallersMatchLiveAdmissionAndPreserveEnablementGates(t *testing.T) {
	r, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	entries := CurrentWriterCallers(r)
	want := 0
	for _, d := range r.Definitions() {
		if d.Method != "GET" && d.Method != "HEAD" && d.Method != "OPTIONS" {
			want++
		}
	}
	if len(entries) != want || want == 0 {
		t.Fatalf("%d callers for %d changing routes", len(entries), want)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		id := e.Metadata.ID
		if id == "" || seen[id] {
			t.Fatalf("duplicate/missing operation %q", id)
		}
		seen[id] = true
		lane, ok := routeLanes[e.Metadata.Method+" "+e.Metadata.Path]
		if !ok || !e.ExplicitLane {
			t.Errorf("%s relies on a guessed lane", id)
			continue
		}
		spec, ok := laneSpecs[lane]
		if !ok {
			t.Errorf("%s has missing lane %s", id, lane)
			continue
		}
		if e.AdmissionClass != spec.class.String() || e.QueueWait != spec.queueWait || e.ResponseBudget != spec.budget {
			t.Errorf("%s differs from actual admission", id)
		}
		if !slices.Contains(e.Metadata.Errors, "writer_capacity") || !slices.Contains(e.Metadata.Errors, "database_busy") {
			t.Errorf("%s lost typed retry outcomes", id)
		}
		if e.RequestType == "" || e.ResponseType == "" || e.ReceiptAdapter == "" || e.AcceptanceProof == "" {
			t.Errorf("%s has incomplete wire/acceptance evidence", id)
		}
	}
	if len(CurrentCallerBlockers()) == 0 {
		t.Fatal("generic result/ACK and mounted-store gaps hidden")
	}
	// Optional source-review artifact. This serializes only static operation
	// descriptors, never a request, actor, SQL parameter or authentication fact.
	if path := os.Getenv("PORTICO_WRITER_CALLER_REPORT"); path != "" {
		data, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
