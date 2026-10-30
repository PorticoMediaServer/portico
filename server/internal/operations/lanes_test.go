package operations

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestLaneCapacitiesProtectTheSingleWriter(t *testing.T) {
	// Every lane that competes for SQLite's one writer is capacity 1. A second
	// concurrent scanner does not scan twice as fast; it doubles the queue in
	// front of playback.
	for _, lane := range []string{LaneWriteHeavy, LaneMetadata, LaneAnalysis, LaneMaintenance, LaneBackground} {
		if laneCapacity(lane) != 1 {
			t.Fatalf("lane %s has capacity %d, want 1", lane, laneCapacity(lane))
		}
	}
	if laneCapacity(LaneOptimized) != 4 {
		t.Fatalf("optimized encodes should run 4 wide, got %d", laneCapacity(LaneOptimized))
	}
	if laneCapacity("unknown-lane") != 1 {
		t.Fatal("an unrecognised lane must default to the safe capacity")
	}
}

func TestLaneAdmissionIsBoundedAndReleasable(t *testing.T) {
	lanes := newLaneSet()
	if !lanes.tryAcquire(LaneWriteHeavy, "scan-a") {
		t.Fatal("the first job could not take an empty lane")
	}
	if lanes.tryAcquire(LaneWriteHeavy, "scan-b") {
		t.Fatal("a full lane admitted a second job")
	}
	// A different lane is unaffected: the lanes govern different resources.
	if !lanes.tryAcquire(LaneMetadata, "refresh-a") {
		t.Fatal("an unrelated lane was blocked by a busy one")
	}
	for i := 0; i < 4; i++ {
		if !lanes.tryAcquire(LaneOptimized, "encode-"+string(rune('a'+i))) {
			t.Fatalf("optimized lane refused encode %d", i)
		}
	}
	if lanes.tryAcquire(LaneOptimized, "encode-overflow") {
		t.Fatal("the optimized lane exceeded its capacity")
	}
	lanes.release("scan-a")
	if !lanes.tryAcquire(LaneWriteHeavy, "scan-b") {
		t.Fatal("a released slot was not reusable")
	}
	// Releasing a job that holds nothing is harmless, which is what lets the
	// scheduler release defensively on every terminal path.
	lanes.release("never-started")
	lanes.release("scan-b")
	if lanes.holds("scan-b") {
		t.Fatal("a released job still held its lane")
	}
}

func TestAdapterClassAndLaneAreIndependent(t *testing.T) {
	scan := Adapter{Kind: "library-scan", Lane: "background-media", Resource: LaneWriteHeavy}
	refresh := Adapter{Kind: "metadata-refresh", Lane: "background-media", Resource: LaneMetadata}
	cleanup := Adapter{Kind: "trash-cleanup", Lane: "maintenance"}
	// Same semantic class, different physical lanes: they are ordered together at
	// the write gate but do not compete for the same resource.
	if workClass(scan) != workClass(refresh) {
		t.Fatal("two background jobs disagreed about their class")
	}
	if resourceLane(scan) == resourceLane(refresh) {
		t.Fatal("scanning and metadata refresh must not share a physical lane")
	}
	if workClass(cleanup) != dbwork.ClassMaintenance {
		t.Fatal("a maintenance adapter did not map to the maintenance class")
	}
	// An adapter that declares no physical lane still gets a safe default.
	if resourceLane(cleanup) != LaneMaintenance {
		t.Fatalf("maintenance defaulted to %s", resourceLane(cleanup))
	}
	if resourceLane(Adapter{Kind: "other", Lane: "background-media"}) != LaneBackground {
		t.Fatal("an undeclared background adapter did not default to the background lane")
	}
}

func TestLaneSnapshotReportsOccupancy(t *testing.T) {
	lanes := newLaneSet()
	lanes.tryAcquire(LaneAnalysis, "probe")
	found := false
	for _, state := range lanes.snapshot() {
		if state.Lane == LaneAnalysis {
			found = true
			if state.Active != 1 || state.Capacity != 1 {
				t.Fatalf("analysis lane reported %#v", state)
			}
		}
	}
	if !found {
		t.Fatal("the analysis lane was missing from diagnostics")
	}
}
