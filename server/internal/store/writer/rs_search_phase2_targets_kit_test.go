package writer

// RS search phase-2 qualification targets: the class-target kit extension for
// packet RS-21.
//
// This is the ONE place the RS search targets are registered through the merged
// canonical class-target kit (classTargetEntry / classTargetStartGate). It does
// not edit the shared canonicalHeldClasses() registry or ci/qual_class.py
// TARGETS: both are hard-coded inventories owned by the desk/QL, so the
// declarations live here and the handoff is documented.
//
// Registration is NOT measurement. Every class is declared foreground_read with
// no runner, an honest UNMEASURED reason, the adopted
// governor.Phase1aWALPolicy() and no override, so the kit start gate refuses it
// with ErrClassTargetUnbound instead of running degraded. A read never owns a
// writer HOLD, so it needs no keeper; the keeper recovery hook remains derived
// from the kind and is proven still required for a held class.
//
// Handoff: when the owner composition lands, append these names once to
// canonicalHeldClasses() in canonical_held_targets_test.go and to TARGETS in
// ci/qual_class.py, then attach the compiled runner. The target cells and their
// evidence bindings live in read/rs_search_phase2_targets_test.go; a _test.go
// declaration cannot cross packages, so the class names are duplicated here.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// rsSearchPhase2Unmeasured is the honest state of every registered RS search
// target. It names the missing composition rather than an inferred zero.
const rsSearchPhase2Unmeasured = "UNMEASURED: canonical 1M paired search bank, actual native/domain inventory Capture, one-native-call/poll budget, ConsumeForeground timeline and per-component resource cells are not attached; never a qualification claim"

// rsSearchPhase2ClassNames mirrors read/rs_search_phase2_targets_test.go. Keep
// both lists in sync until the shared registry handoff lands.
var rsSearchPhase2ClassNames = []string{
	"rs.search.rss",
	"rs.search.bank_cold_open",
	"rs.search.people_ordinary",
	"rs.search.people_rare",
	"rs.search.people_dense",
	"rs.search.people_ubiquitous",
	"rs.search.people_restricted",
	"rs.search.poll_preview_budget",
	"rs.search.group_traversals",
	"rs.search.reading_density",
	"rs.search.field_64kib",
	"rs.search.entropy_latin",
	"rs.search.entropy_cjk",
	"rs.search.entropy_name",
	"rs.search.fragment_logical_subject",
}

// rsSearchPhase2Classes declares the closed RS search target set through the
// kit. A declaration that cannot register stays invalid and refuses.
func rsSearchPhase2Classes() []canonicalHeldClass {
	out := make([]canonicalHeldClass, 0, len(rsSearchPhase2ClassNames))
	for _, name := range rsSearchPhase2ClassNames {
		out = append(out, classTargetEntry(classTargetSpec{
			Name:   name,
			Owner:  "P2-RS",
			Kind:   "foreground_read",
			Reason: rsSearchPhase2Unmeasured,
		}))
	}
	return out
}

func rsSearchPhase2ClassFor(name string) (canonicalHeldClass, error) {
	for _, class := range rsSearchPhase2Classes() {
		if class.name == name {
			if class.invalid != nil {
				return canonicalHeldClass{}, class.invalid
			}
			return class, nil
		}
	}
	return canonicalHeldClass{}, fmt.Errorf("%w: %q is not a registered RS search phase-2 target", ErrClassTargetUnbound, name)
}

// beginRSSearchPhase2Target starts a class through the kit's ONE refusing gate.
// The gate runs first, so a missing registry entry, WAL policy or (for a held
// class) keeper/writer binding refuses before this function considers the
// runner. Only then does the unmeasured state refuse: a registered target with
// no compiled runner never starts.
func beginRSSearchPhase2Target(t *testing.T, class string, start classTargetStart) (classTargetRuntime, error) {
	t.Helper()
	definition, err := rsSearchPhase2ClassFor(class)
	if err != nil {
		return classTargetRuntime{}, err
	}
	wal, err := classTargetStartGate(definition, start)
	if err != nil {
		return classTargetRuntime{}, err
	}
	if definition.run == nil {
		return classTargetRuntime{}, fmt.Errorf("%w: %s is registered unmeasured with no compiled runner; %s", ErrClassTargetUnbound, class, definition.reason)
	}
	return classTargetRuntime{Class: definition, Writer: start.Writer, WAL: wal}, nil
}

func TestRSSearchPhase2ClassesRegisterThroughTheKit(t *testing.T) {
	classes := rsSearchPhase2Classes()
	if len(classes) != len(rsSearchPhase2ClassNames) {
		t.Fatalf("registered %d classes for %d names", len(classes), len(rsSearchPhase2ClassNames))
	}
	seen := make(map[string]bool)
	for _, class := range classes {
		if class.invalid != nil {
			t.Fatalf("%s did not register: %v", class.name, class.invalid)
		}
		if class.name == "" || seen[class.name] {
			t.Fatalf("duplicate or empty class %q", class.name)
		}
		seen[class.name] = true
		if class.owner != "P2-RS" || class.kind != "foreground_read" {
			t.Fatalf("%s is not an RS foreground read: %+v", class.name, class)
		}
		if class.purpose != nil {
			t.Fatalf("%s owns a writer command purpose", class.name)
		}
		if class.target == nil {
			t.Fatalf("%s was not registered through classTargetEntry", class.name)
		}
		if class.wal.Explicit || class.wal.Policy != governor.Phase1aWALPolicy() {
			t.Fatalf("%s did not adopt the phase 1a WAL policy", class.name)
		}
		if class.wal.Policy.HardBytes <= 0 {
			t.Fatalf("%s has no WAL policy bytes", class.name)
		}
		if class.run != nil {
			t.Fatalf("%s claims a compiled runner", class.name)
		}
		if !strings.Contains(class.reason, "UNMEASURED") {
			t.Fatalf("%s does not declare its unmeasured state", class.name)
		}
		if class.target.requiresKeeper() {
			t.Fatalf("%s is a read that requires the writer keeper", class.name)
		}
	}
	if len(seen) != len(rsSearchPhase2ClassNames) {
		t.Fatalf("closed set incomplete: %d", len(seen))
	}
	if _, err := rsSearchPhase2ClassFor("rs.search.not_registered"); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatalf("unregistered target looked up: %v", err)
	}
}

func TestRSSearchPhase2UnmeasuredTargetsRefuseToStart(t *testing.T) {
	for _, class := range rsSearchPhase2Classes() {
		t.Run(class.name, func(t *testing.T) {
			runtime, err := beginRSSearchPhase2Target(t, class.name, classTargetStart{})
			if !errors.Is(err, ErrClassTargetUnbound) {
				t.Fatalf("unmeasured target started: %+v %v", runtime, err)
			}
			if runtime.Class.name != "" || runtime.Writer != nil || runtime.Keeper != nil || runtime.WAL.Policy.HardBytes != 0 {
				t.Fatalf("refused start returned a partial runtime: %+v", runtime)
			}
		})
	}
	if _, err := beginRSSearchPhase2Target(t, "rs.search.not_registered", classTargetStart{}); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatalf("unknown target refused with the wrong cause: %v", err)
	}
}

func TestRSSearchPhase2StartGateKeepsRefusingBindings(t *testing.T) {
	read := rsSearchPhase2Classes()[0]
	// A read class only needs the adopted WAL policy; it is admitted by the gate
	// and then refused for its unmeasured runner, never for a writer HOLD.
	if _, err := classTargetStartGate(read, classTargetStart{}); err != nil {
		t.Fatalf("registered read refused for a missing writer: %v", err)
	}
	// An unregistered class cannot borrow the kit.
	if _, err := classTargetStartGate(canonicalHeldClass{name: "rs.search.unregistered"}, classTargetStart{}); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatalf("unregistered class passed the gate: %v", err)
	}
	// A class without a WAL policy is refused.
	noWAL := read
	noWAL.wal = classTargetWAL{}
	if _, err := classTargetStartGate(noWAL, classTargetStart{}); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatalf("class without a WAL policy passed the gate: %v", err)
	}
	// The keeper recovery hook is still derived, not declared: a held class
	// without its writer and funded keeper refuses.
	purpose := InventoryAccept
	held := classTargetEntry(classTargetSpec{Name: "rs.search.control_hold", Owner: "P2-RS", Kind: "normal_write", Purpose: &purpose, Reason: "control only: proves the keeper recovery hook is still required"})
	if held.invalid != nil {
		t.Fatalf("held control did not register: %v", held.invalid)
	}
	if !held.target.requiresKeeper() {
		t.Fatal("a held class no longer requires the keeper recovery hook")
	}
	if _, err := classTargetStartGate(held, classTargetStart{}); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatalf("held class without a keeper/writer passed the gate: %v", err)
	}
}
