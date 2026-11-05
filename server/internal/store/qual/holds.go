package qual

import (
	"fmt"
	"time"
)

const (
	// Phase1HoldsPerHost applies separately to each recipe and cold/warm cell.
	Phase1HoldsPerHost = 10_000
	HostsPerProfile    = 3
	WriterP99Budget    = 10 * time.Millisecond
	QuietMaxBudget     = 25 * time.Millisecond
)

type Verdict string

const (
	Pass         Verdict = "pass"
	Fail         Verdict = "fail"
	Inconclusive Verdict = "inconclusive"
	Invalid      Verdict = "invalid"
)

// HostHolds is one independent VM's complete writer-custody population for one
// pinned recipe/profile/cache-state cell. CompleteHolds includes successful
// accepted commands only, from acquisition through COMMIT/physical return.
// Queue time is separate. Failed commands' holds and reasons remain raw evidence
// and must be summarized separately with Summarize; failures cannot earn a pass.
// Refusals before acquisition are counted, never converted into short holds.
// ProtocolErrors carries wrong pins/engine, missing telemetry/witnesses,
// incomplete/crashed/OOM cells and failed correctness checks, with raw artifacts
// retained. Slowness, GC, FTS and checkpoint costs are not protocol exclusions.
type HostHolds struct {
	HostID         string          `json:"host_id"`
	CompleteHolds  []time.Duration `json:"complete_holds_ns"`
	FailedCommands int             `json:"failed_commands"`
	Refusals       int             `json:"refusals"`
	ProtocolErrors []string        `json:"protocol_errors,omitempty"`
}

type HostReport struct {
	HostID         string        `json:"host_id"`
	Holds          *Distribution `json:"holds"`
	FailedCommands int           `json:"failed_commands"`
	Refusals       int           `json:"refusals"`
	Verdict        Verdict       `json:"verdict"`
	Reasons        []string      `json:"reasons,omitempty"`
}

// HoldReport is ONLY the hosted command-hold statistical gate. It does not
// qualify quiet maximum, correct hardware/engine/pins, foreground protection,
// WAL, receipts or memory. Those gates need independent measured evidence.
// Every host remains in the report; samples are never pooled across hosts.
type HoldReport struct {
	Verdict     Verdict      `json:"verdict"`
	Hosts       []HostReport `json:"hosts"`
	MedianRatio float64      `json:"median_ratio"`
	Split       bool         `json:"split"`
	Reasons     []string     `json:"reasons,omitempty"`
}

// EvaluateHostedHolds applies the phase-1 O-08 counts, per-host p99 bound, and
// mandatory fourfold-median split rule. Call separately for each pinned recipe,
// hardware profile and cold/warm population. Host identity and provenance must
// be verified by the runner before calling: different process IDs or repetition
// labels do not establish independent VMs.
func EvaluateHostedHolds(hosts []HostHolds) HoldReport {
	r := HoldReport{Verdict: Pass, Hosts: make([]HostReport, 0, len(hosts))}
	seen := make(map[string]bool, len(hosts))
	var minMedian, maxMedian time.Duration
	invalid, failed, incomplete := false, false, len(hosts) < HostsPerProfile
	for _, host := range hosts {
		h := HostReport{
			HostID: host.HostID, FailedCommands: host.FailedCommands,
			Refusals: host.Refusals, Verdict: Pass,
			Reasons: append([]string(nil), host.ProtocolErrors...),
		}
		if host.HostID == "" || seen[host.HostID] {
			h.Reasons = append(h.Reasons, "missing or duplicate independent host identity")
		}
		seen[host.HostID] = true
		if host.FailedCommands < 0 || host.Refusals < 0 {
			h.Reasons = append(h.Reasons, "negative command count")
		}
		if len(host.CompleteHolds) > 0 {
			d, err := Summarize(host.CompleteHolds)
			if err != nil {
				h.Reasons = append(h.Reasons, err.Error())
			} else {
				h.Holds = &d
				if minMedian == 0 || d.P50 < minMedian {
					minMedian = d.P50
				}
				maxMedian = max(maxMedian, d.P50)
			}
		}
		if len(h.Reasons) > 0 {
			h.Verdict = Invalid
			invalid = true
		} else if host.FailedCommands > 0 {
			h.Verdict = Fail
			failed = true
			h.Reasons = append(h.Reasons, "failed commands retained in raw evidence")
		} else if len(host.CompleteHolds) < Phase1HoldsPerHost {
			h.Verdict = Inconclusive
			incomplete = true
			h.Reasons = append(h.Reasons, fmt.Sprintf("need %d complete accepted holds per host", Phase1HoldsPerHost))
		} else if h.Holds.P99Upper95 == nil || h.Holds.P99Upper95.Value > WriterP99Budget {
			h.Verdict = Fail
			failed = true
			h.Reasons = append(h.Reasons, "per-host upper 95% p99 bound exceeds 10 ms or is unavailable")
		}
		r.Hosts = append(r.Hosts, h)
	}
	if minMedian > 0 {
		r.MedianRatio = float64(maxMedian) / float64(minMedian)
		r.Split = r.MedianRatio >= 4
	}
	if len(hosts) < HostsPerProfile {
		r.Reasons = append(r.Reasons, "need at least three independent VMs per profile")
	}
	if r.Split {
		r.Reasons = append(r.Reasons, "fourfold median split: retain cohort and rerun on at least three fresh VMs")
	}
	// Invalid cells never disappear. Split forces an inconclusive cohort even
	// if individual hosts fail; their individual failures remain in Hosts.
	switch {
	case invalid:
		r.Verdict = Invalid
	case r.Split:
		r.Verdict = Inconclusive
	case failed:
		r.Verdict = Fail
	case incomplete:
		r.Verdict = Inconclusive
	}
	return r
}
