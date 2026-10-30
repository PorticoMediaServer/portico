package operations

import (
	"sort"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A job has two independent properties and they must not be conflated.
//
// Its work class is semantic: it says where the job's writes sit in the
// priority ladder, and it is what the write gate orders by.
//
// Its resource lane is physical: it says which scarce thing the job consumes —
// the single SQLite writer, a metadata provider's rate limit, CPU for analysis,
// an encoder. CPU, disk, provider quota and database write capacity are not
// interchangeable, so one number cannot govern all of them.
//
// Two jobs can therefore share a class and compete for nothing, or share a lane
// and be ordered by class inside it.
const (
	LaneWriteHeavy  = "write-heavy"
	LaneMetadata    = "metadata"
	LaneAnalysis    = "analysis"
	LaneOptimized   = "optimized"
	LaneMaintenance = "maintenance"
	LaneBackground  = "background"
)

// laneCapacity is how many jobs of a lane may run at once. Every lane that
// competes for the single writer is 1: a second concurrent scanner does not
// scan twice as fast, it only doubles the queue in front of playback. Optimized
// encodes are 4 because they are CPU-bound work that barely touches SQLite.
func laneCapacity(lane string) int {
	switch lane {
	case LaneOptimized:
		return 4
	case LaneWriteHeavy, LaneMetadata, LaneAnalysis, LaneMaintenance, LaneBackground:
		return 1
	default:
		return 1
	}
}

// Lanes returns the declared lanes in a stable order.
func Lanes() []string {
	return []string{LaneWriteHeavy, LaneMetadata, LaneAnalysis, LaneOptimized, LaneBackground, LaneMaintenance}
}

// resourceLane maps an adapter to its physical lane. The adapter may declare one
// directly; otherwise its semantic class picks a sensible default so an existing
// registration keeps working.
func resourceLane(a Adapter) string {
	if a.Resource != "" {
		return a.Resource
	}
	if a.Lane == "maintenance" {
		return LaneMaintenance
	}
	return LaneBackground
}

// workClass maps an adapter's declared lane name onto the canonical ladder.
func workClass(a Adapter) dbwork.Class {
	if a.Lane == "maintenance" {
		return dbwork.ClassMaintenance
	}
	return dbwork.ClassBackgroundMedia
}

// laneSet is the set of lane semaphores. Capacity is fixed by the table above,
// so the set is built once and never resized under load.
type laneSet struct {
	mu     sync.Mutex
	inUse  map[string]int
	holder map[string]string
}

func newLaneSet() *laneSet {
	return &laneSet{inUse: map[string]int{}, holder: map[string]string{}}
}

// tryAcquire takes a slot in lane for job, or reports that the lane is full.
// It never blocks: a scheduler tick that cannot start a job simply tries again
// on the next tick, where the fair claim will have re-read the queue.
func (l *laneSet) tryAcquire(lane, job string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.holder[job] != "" {
		return false
	}
	if l.inUse[lane] >= laneCapacity(lane) {
		return false
	}
	l.inUse[lane]++
	l.holder[job] = lane
	return true
}

func (l *laneSet) release(job string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lane := l.holder[job]
	if lane == "" {
		return
	}
	delete(l.holder, job)
	if l.inUse[lane] > 0 {
		l.inUse[lane]--
	}
}

func (l *laneSet) holds(job string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.holder[job] != ""
}

// LaneState reports one lane's occupancy for diagnostics.
type LaneState struct {
	Lane     string `json:"lane"`
	Active   int    `json:"active"`
	Capacity int    `json:"capacity"`
	Backlog  int    `json:"backlog"`
}

func (l *laneSet) snapshot() []LaneState {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []LaneState{}
	for _, lane := range Lanes() {
		out = append(out, LaneState{Lane: lane, Active: l.inUse[lane], Capacity: laneCapacity(lane)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lane < out[j].Lane })
	return out
}
