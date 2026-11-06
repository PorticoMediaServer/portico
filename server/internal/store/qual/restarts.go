package qual

import (
	"fmt"
	"sort"
	"time"
)

// RestartCell identifies ONE manifest-bound host/database/population. The
// collector, not this reducer, proves the manifest, policy and WAL observations.
type RestartCell struct {
	ID, HostID, Profile, DatabaseID, ManifestSHA256 string
	CacheState, CadenceMode                         string
}

type WALCommitPosition string

const (
	WALRestartFirst WALCommitPosition = "restart_first"
	WALAppend       WALCommitPosition = "append"
	WALNoFrames     WALCommitPosition = "no_frames"
	WALCreated      WALCommitPosition = "created_or_recreated"
	WALUnknown      WALCommitPosition = "unknown"
)

// RestartSample is a joined COMPLETE physically returned writer attempt. WAL
// position comes from actual generation/header/frame evidence, never checkpoint
// completion or unchanged allocation. A failed first write can incur the sync;
// do not move its event/cost onto a later successful commit. Before-custody
// refusals are counted separately. Keep the original raw ledger externally.
// Hold and Commit, like all report durations, encode integer nanoseconds in JSON.
type RestartSample struct {
	CellID, UnitID, Recipe, Class, Durability string
	Ordinal                                   uint64 // contiguous attempt ordinal, starting at one in this cell
	Outcome                                   string // committed, rolled_back, unknown
	Position                                  WALCommitPosition
	Hold, Commit                              time.Duration
	IntendedClass                             string // empty if placement was not requested
	EvidenceRef, UnknownReason, OutcomeReason string
	Fenced                                    bool
}

// RestartDistribution is descriptive, not a qualification verdict. An empty
// population is null; p99.9 is null below the phase-1 minimum sample count.
type RestartDistribution struct {
	Count                int
	P50, P95, P99, Max   time.Duration
	P999                 *time.Duration
	Above10MS, Above25MS int
}

type RestartLatency struct {
	Holds, Commits *RestartDistribution
}

type RestartStrata struct {
	AllAttempts, Committed, PostRestart, Rest, Unclassified, Failed RestartLatency
}

type RestartClassReport struct {
	Recipe, Class, Durability   string
	Restarts, Creations, Fences int
	Strata                      RestartStrata
}

type RestartReport struct {
	Kind                                                   string
	Cell                                                   RestartCell
	Attempts, Commits, FrameCommits, NoFrameCommits        int
	Restarts, Creations, UnknownPositions, UnknownOutcomes int
	Refusals, Fences                                       int
	ClassificationComplete                                 bool
	RestartsPerCommit, RestartsPer1000Commits              *float64
	Strata                                                 RestartStrata
	ByClass                                                []RestartClassReport
	// Restart, creation, unknown, failed and fenced events retain actual and
	// intended class and evidence references. Unfenced ordinary appends stay raw.
	Events []RestartSample
}

func restartClass(c string) bool {
	return c == "Background" || c == "ScanMinimum" || c == "Foreground" || c == "Recovery"
}

// SummarizeRestarts does not discover/verify a WAL restart, infer header-sync
// causality, enforce placement or issue a pass. It reduces one joined evidence
// cell, keeping post-restart tails in BOTH overall and per-recipe/class holds.
// Unknown classification/outcomes withhold cadence ratios; observed counters
// remain visible. Never pool hosts, cache states or accelerated/realistic cells.
func SummarizeRestarts(cell RestartCell, samples []RestartSample, refusals int) (RestartReport, error) {
	r := RestartReport{Kind: "restart-cadence-descriptive-only", Cell: cell, Refusals: refusals, ClassificationComplete: true}
	if cell.ID == "" || cell.HostID == "" || cell.Profile == "" || cell.DatabaseID == "" ||
		len(cell.ManifestSHA256) != 64 || (cell.CacheState != "cold" && cell.CacheState != "warm") ||
		(cell.CadenceMode != "realistic" && cell.CadenceMode != "accelerated") || refusals < 0 || len(samples) == 0 {
		return RestartReport{}, fmt.Errorf("invalid or empty restart cell")
	}
	for _, b := range cell.ManifestSHA256 {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return RestartReport{}, fmt.Errorf("invalid manifest SHA256")
		}
	}
	type key struct{ recipe, class, durability string }
	groups := map[key][]RestartSample{}
	seen := map[string]bool{}
	var previous uint64
	for i, s := range samples {
		if s.CellID != cell.ID || s.UnitID == "" || seen[s.UnitID] || s.Ordinal == 0 || s.Ordinal != previous+1 ||
			s.Recipe == "" || !restartClass(s.Class) || s.IntendedClass != "" && !restartClass(s.IntendedClass) ||
			(s.Durability != "NORMAL" && s.Durability != "FULL") || s.Hold <= 0 || s.Commit < 0 || s.Commit > s.Hold ||
			s.EvidenceRef == "" || (s.Outcome != "committed" && s.Outcome != "rolled_back" && s.Outcome != "unknown") ||
			s.Outcome == "committed" && s.Commit == 0 || s.Outcome != "committed" && s.OutcomeReason == "" {
			return RestartReport{}, fmt.Errorf("invalid restart sample %d", i)
		}
		switch s.Position {
		case WALRestartFirst, WALAppend, WALNoFrames, WALCreated:
		case WALUnknown:
			if s.UnknownReason == "" {
				return RestartReport{}, fmt.Errorf("missing classification reason at %d", i)
			}
			r.UnknownPositions++
			r.ClassificationComplete = false
		default:
			return RestartReport{}, fmt.Errorf("invalid WAL position at %d", i)
		}
		seen[s.UnitID], previous = true, s.Ordinal
		r.Attempts++
		if s.Fenced {
			r.Fences++
		}
		if s.Outcome == "unknown" {
			r.UnknownOutcomes++
			r.ClassificationComplete = false
		}
		if s.Outcome == "committed" {
			r.Commits++
			switch s.Position {
			case WALRestartFirst, WALAppend, WALCreated:
				r.FrameCommits++
			case WALNoFrames:
				r.NoFrameCommits++
			}
		}
		if s.Position == WALRestartFirst {
			r.Restarts++
		}
		if s.Position == WALCreated {
			r.Creations++
		}
		if s.Fenced || s.Position == WALRestartFirst || s.Position == WALCreated || s.Position == WALUnknown || s.Outcome != "committed" {
			r.Events = append(r.Events, s)
		}
		k := key{s.Recipe, s.Class, s.Durability}
		groups[k] = append(groups[k], s)
	}
	if r.ClassificationComplete && r.Commits > 0 {
		rate := float64(r.Restarts) / float64(r.Commits)
		per1000 := rate * 1000
		r.RestartsPerCommit, r.RestartsPer1000Commits = &rate, &per1000
	}
	r.Strata = restartStrata(samples)
	for k, ss := range groups {
		g := RestartClassReport{Recipe: k.recipe, Class: k.class, Durability: k.durability, Strata: restartStrata(ss)}
		for _, s := range ss {
			if s.Fenced {
				g.Fences++
			}
			if s.Position == WALRestartFirst {
				g.Restarts++
			}
			if s.Position == WALCreated {
				g.Creations++
			}
		}
		r.ByClass = append(r.ByClass, g)
	}
	sort.Slice(r.ByClass, func(i, j int) bool {
		a, b := r.ByClass[i], r.ByClass[j]
		if a.Recipe != b.Recipe {
			return a.Recipe < b.Recipe
		}
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		return a.Durability < b.Durability
	})
	return r, nil
}

func restartStrata(samples []RestartSample) RestartStrata {
	var all, committed, first, rest, unknown, failed []RestartSample
	for _, s := range samples {
		all = append(all, s)
		if s.Outcome != "committed" {
			failed = append(failed, s)
			continue
		}
		committed = append(committed, s)
		switch s.Position {
		case WALRestartFirst:
			first = append(first, s)
		case WALUnknown:
			unknown = append(unknown, s)
		default:
			rest = append(rest, s)
		}
	}
	return RestartStrata{restartLatency(all), restartLatency(committed), restartLatency(first), restartLatency(rest), restartLatency(unknown), restartLatency(failed)}
}

func restartLatency(samples []RestartSample) RestartLatency {
	var holds, commits []time.Duration
	for _, s := range samples {
		holds = append(holds, s.Hold)
		if s.Commit > 0 {
			commits = append(commits, s.Commit)
		}
	}
	return RestartLatency{restartDistribution(holds), restartDistribution(commits)}
}

func restartDistribution(samples []time.Duration) *RestartDistribution {
	if len(samples) == 0 {
		return nil
	}
	d, _ := Summarize(samples) // caller validated positive durations
	r := &RestartDistribution{Count: d.Count, P50: d.P50, P95: d.P95, P99: d.P99, Max: d.Max}
	if len(samples) >= Phase1HoldsPerHost {
		r.P999 = &d.P999
	}
	for _, s := range samples {
		if s > WriterP99Budget {
			r.Above10MS++
		}
		if s > QuietMaxBudget {
			r.Above25MS++
		}
	}
	return r
}
