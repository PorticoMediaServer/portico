package dbwork

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A statement count says a route is expensive. It does not say which statement.
//
// When a route's cost is dominated by one query over a whole projection, the
// count is small and the latency is large, and the only way to name the offender
// is to attribute time to statement text. That is what this does: an opt-in,
// process-wide profile keyed on the statement's shape, holding how many times it
// ran and how long it took in total.
//
// It is off unless a harness turns it on, and it never reaches a metric, a log
// or the diagnostics endpoint — the same rule `TraceStatements` follows, for the
// same reason: query text is not telemetry.

// StatementProfileEntry is one statement shape's accumulated cost.
type StatementProfileEntry struct {
	// Shape is the statement with its whitespace collapsed and its bound
	// arguments left as placeholders, truncated. It identifies the query, not the
	// values it ran with.
	Shape string `json:"shape"`
	Count int64  `json:"count"`
	Nanos int64  `json:"nanos"`
}

// Millis is the total time this shape accounted for.
func (e StatementProfileEntry) Millis() float64 { return float64(e.Nanos) / float64(time.Millisecond) }

// MeanMicros is the average cost of one execution.
func (e StatementProfileEntry) MeanMicros() float64 {
	if e.Count == 0 {
		return 0
	}
	return float64(e.Nanos) / float64(e.Count) / 1000
}

// statementProfileShapes bounds the table. A server issues a few hundred
// distinct statement shapes; a thousand is room to spare and a hard ceiling.
const statementProfileShapes = 2048

// statementShapeLength is how much of a statement identifies it. Long statements
// in this server differ early — the projection they read is the first clause —
// so a generous prefix separates them.
const statementShapeLength = 240

type statementProfileTable struct {
	mu      sync.Mutex
	entries map[string]*StatementProfileEntry
}

var statementProfileOn atomic.Bool
var statementProfile = statementProfileTable{entries: map[string]*StatementProfileEntry{}}

// ProfileStatements turns the profile on or off. Turning it on empties it, so a
// harness measures the window it opened rather than the life of the process.
func ProfileStatements(on bool) {
	statementProfile.mu.Lock()
	statementProfile.entries = map[string]*StatementProfileEntry{}
	statementProfile.mu.Unlock()
	statementProfileOn.Store(on)
}

func profileStatement(query string, elapsed time.Duration) {
	if !statementProfileOn.Load() || query == "" {
		return
	}
	shape := strings.Join(strings.Fields(query), " ")
	if len(shape) > statementShapeLength {
		shape = shape[:statementShapeLength]
	}
	statementProfile.mu.Lock()
	defer statementProfile.mu.Unlock()
	entry := statementProfile.entries[shape]
	if entry == nil {
		if len(statementProfile.entries) >= statementProfileShapes {
			return
		}
		entry = &StatementProfileEntry{Shape: shape}
		statementProfile.entries[shape] = entry
	}
	entry.Count++
	entry.Nanos += int64(elapsed)
}

// StatementProfile reports the statement shapes seen since the profile was
// turned on, most expensive in total first. That ordering is the one that
// answers "where did the time go", which is a different question from "what ran
// most often".
func StatementProfile() []StatementProfileEntry {
	statementProfile.mu.Lock()
	defer statementProfile.mu.Unlock()
	out := make([]StatementProfileEntry, 0, len(statementProfile.entries))
	for _, entry := range statementProfile.entries {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nanos > out[j].Nanos })
	return out
}
