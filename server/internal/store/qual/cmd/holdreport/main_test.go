package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

func TestHoldReportRetainsNonpassingCohortAndIdentity(t *testing.T) {
	c := cohort{Source: strings.Repeat("a", 40), Profile: "standard", Recipe: "synthetic-control", Population: "steady"}
	for _, name := range []string{"synthetic-1", "synthetic-2", "synthetic-3"} {
		h := qual.HostHolds{HostID: name, CompleteHolds: make([]time.Duration, 10000)}
		for i := range h.CompleteHolds {
			h.CompleteHolds[i] = time.Millisecond
		}
		c.Hosts = append(c.Hosts, h)
	}
	path := filepath.Join(t.TempDir(), "cohort.json")
	for _, refused := range []int{0, 1} {
		c.Hosts[1].Refusals = refused
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err = run([]string{"-input", path}, &output)
		if (err != nil) != (refused > 0) {
			t.Fatal(err)
		}
		var result struct {
			Source string          `json:"source_sha"`
			Report qual.HoldReport `json:"hosted_hold_gate"`
		}
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Source != c.Source || len(result.Report.Hosts) != 3 || result.Report.Hosts[1].Refusals != refused {
			t.Fatal(result)
		}
		if refused > 0 && result.Report.Verdict != qual.Fail {
			t.Fatal(result)
		}
	}
}

func TestHoldReportRejectsUnidentifiedOrAmbiguousInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	for _, data := range []string{`{}`, `{"hosts":[],"unexpected":1}`, `{} {}`, `{"source_sha":false}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := run([]string{"-input", path}, &output); err == nil || output.Len() != 0 {
			t.Fatalf("accepted %q: %v %s", data, err, output.String())
		}
	}
	duplicate := `{"source_sha":"` + strings.Repeat("a", 40) + `","profile":"standard","recipe":"synthetic","population":"steady","hosts":[{"host_id":"synthetic","refusals":1,"refusals":0}]}`
	if err := os.WriteFile(path, []byte(duplicate), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"-input", path}, &output); err == nil || output.Len() != 0 {
		t.Fatalf("duplicate refusal overwritten: %v %s", err, output.String())
	}
	if err := run(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("missing input accepted")
	}
}
