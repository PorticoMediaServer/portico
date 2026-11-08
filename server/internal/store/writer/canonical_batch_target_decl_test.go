package writer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

type canonicalBatchTargetDeclaration struct {
	Target          string `json:"target"`
	Flag            *bool  `json:"target_flag"`
	Reason          string `json:"reason"`
	Gate            string `json:"gate"`
	MinHoldsPerHost int    `json:"min_holds_per_host"`
	Synthetic       bool   `json:"synthetic"`
}

// The owner declares only its own complete, measured cell. The roll-up still
// checks independent host identities and the three-VM median-split gate.
// History roles remain separate from fresh ACCEPT effects; warmup never enters.
func canonicalBatchOwnerTarget(complete bool, host string, holds map[string][]time.Duration) canonicalBatchTargetDeclaration {
	pass := true
	declaration := canonicalBatchTargetDeclaration{
		Target: "per-host p99 U95 <= 10 ms for each measured ACCEPT/RESOLVE/ACK role",
		Flag:   &pass, Gate: "hosted_p99_u95_10ms", MinHoldsPerHost: qual.Phase1HoldsPerHost,
	}
	failed, short := !complete, false
	reasons := []string{}
	if !complete {
		reasons = append(reasons, "owner execution or joined cleanup incomplete")
	}
	for role := range holds {
		if role != "ACCEPT" && role != "RESOLVE" && role != "ACK" {
			failed = true
			reasons = append(reasons, "unknown measured role")
		}
	}
	for _, role := range []string{"ACCEPT", "RESOLVE", "ACK"} {
		samples := holds[role]
		// The aggregate verdict is inconclusive with one host; its per-host
		// verdict applies the unchanged count and p99 rule to THIS cell.
		report := qual.EvaluateHostedHolds([]qual.HostHolds{{HostID: host, CompleteHolds: samples}}).Hosts[0]
		switch report.Verdict {
		case qual.Pass:
		case qual.Inconclusive:
			short = true
		default:
			failed = true
		}
		if len(samples) > qual.Phase1HoldsPerHost {
			failed = true
			report.Reasons = append(report.Reasons, "extra measured holds for the fixed 10000-original recipe")
		}
		reasons = append(reasons, fmt.Sprintf("%s: %s (%d holds) %s", role, report.Verdict, len(samples), strings.Join(report.Reasons, "; ")))
	}
	if failed {
		pass = false
	} else if short {
		declaration.Flag = nil
	}
	declaration.Reason = strings.Join(reasons, "; ")
	return declaration
}

func TestCanonicalBatchOwnerTargetDeclaration(t *testing.T) {
	// Fabricated duration vectors test the declaration rule, not the machine.
	for _, tc := range []struct {
		name     string
		complete bool
		count    int
		duration time.Duration
		want     string
	}{
		{"at_budget", true, 10000, 10 * time.Millisecond, "true"},
		{"over_budget", true, 10000, 10*time.Millisecond + time.Nanosecond, "false"},
		{"short", true, 9999, time.Millisecond, "null"},
		{"missing", true, 0, time.Millisecond, "null"},
		{"extra", true, 10001, time.Millisecond, "false"},
		{"incomplete", false, 10000, time.Millisecond, "false"},
		{"invalid", true, 10000, 0, "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holds := map[string][]time.Duration{}
			for _, role := range []string{"ACCEPT", "RESOLVE", "ACK"} {
				holds[role] = make([]time.Duration, 10000)
				for i := range holds[role] {
					holds[role][i] = time.Millisecond
				}
			}
			holds["RESOLVE"] = make([]time.Duration, tc.count)
			for i := range holds["RESOLVE"] {
				holds["RESOLVE"][i] = tc.duration
			}
			d := canonicalBatchOwnerTarget(tc.complete, "fabricated-host", holds)
			got := "null"
			if d.Flag != nil {
				got = fmt.Sprint(*d.Flag)
			}
			if got != tc.want || d.Synthetic || d.MinHoldsPerHost != 10000 || d.Gate != "hosted_p99_u95_10ms" {
				t.Fatal(got, d)
			}
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err = json.Unmarshal(raw, &wire); err != nil || len(wire) != 6 {
				t.Fatal("owner declaration wire keys", string(raw), err)
			}
			for _, key := range []string{"target", "target_flag", "reason", "gate", "min_holds_per_host", "synthetic"} {
				if _, ok := wire[key]; !ok {
					t.Fatal("missing owner declaration field", key)
				}
			}
		})
	}
}
