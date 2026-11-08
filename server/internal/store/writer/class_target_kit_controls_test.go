package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// A class that needs the keeper recovery hook must not start without one.
func TestClassTargetKitRefusesStartWithoutRequiredBindings(t *testing.T) {
	purpose := ProgressAccept
	write := classTargetEntry(classTargetSpec{Name: "kit.write", Owner: "KIT", Kind: "normal_write", Purpose: &purpose, Reason: "control only"})
	gate, err := governor.NewWALGate(classTargetWALDefault().Policy)
	if err != nil {
		t.Fatal(err)
	}
	maintenance := &checkpointMaintenance{}
	full := &Writer{effective: &effectiveRuntime{executor: &executor{}, wal: gate, maintenance: maintenance}}
	bare := &canonicalN1MaintenanceOwner{}
	for name, start := range map[string]classTargetStart{
		"no writer":             {Keeper: bare},
		"no keeper":             {Writer: full},
		"keeper without a hook": {Writer: full, Keeper: bare},
		"no maintenance hook":   {Writer: &Writer{effective: &effectiveRuntime{executor: &executor{}, wal: gate}}, Keeper: bare},
		"no WAL policy gate":    {Writer: &Writer{effective: &effectiveRuntime{executor: &executor{}, maintenance: maintenance}}, Keeper: bare},
		"no executor":           {Writer: &Writer{effective: &effectiveRuntime{wal: gate, maintenance: maintenance}}, Keeper: bare},
	} {
		if _, err := classTargetStartGate(write, start); !errors.Is(err, ErrClassTargetUnbound) {
			t.Fatalf("%s: the start gate must refuse, got %v", name, err)
		}
	}
}

// A class with no named override runs on the adopted Phase1a policy.
func TestClassTargetKitDefaultsToPhase1aWALPolicy(t *testing.T) {
	read := classTargetEntry(classTargetSpec{Name: "kit.read", Owner: "KIT", Kind: "foreground_read", Reason: "control only"})
	bound, err := classTargetStartGate(read, classTargetStart{})
	if err != nil {
		t.Fatal(err)
	}
	if bound != classTargetWALDefault() || bound.Explicit {
		t.Fatal("a class without an override must run on the adopted policy, unrecorded as an override", bound)
	}
	if bound.Policy != governor.Phase1aWALPolicy() {
		t.Fatal("default policy drifted from governor.Phase1aWALPolicy", bound.Policy)
	}
}

// A named override is applied and recorded as explicit.
func TestClassTargetKitRecordsExplicitWALOverride(t *testing.T) {
	override := governor.Phase1aWALPolicy()
	override.SoftDebtBytes = 32 << 20
	read := classTargetEntry(classTargetSpec{Name: "kit.read-override", Owner: "KIT", Kind: "foreground_read", Reason: "control only", WALOverride: &override})
	bound, err := classTargetStartGate(read, classTargetStart{})
	if err != nil {
		t.Fatal(err)
	}
	if !bound.Explicit || bound.Policy.SoftDebtBytes != 32<<20 || bound.Policy == governor.Phase1aWALPolicy() {
		t.Fatal("explicit override not applied or not recorded", bound)
	}
	// The override belongs to that class alone; the adopted default is intact.
	if classTargetWALDefault().Policy != governor.Phase1aWALPolicy() {
		t.Fatal("an override mutated the adopted default")
	}
}

func TestClassTargetKitRefusesIncompleteDeclarations(t *testing.T) {
	purpose := ProgressAccept
	for name, spec := range map[string]classTargetSpec{
		"no name":             {Owner: "KIT", Kind: "normal_write", Purpose: &purpose, Reason: "control"},
		"no owner":            {Name: "kit.bad", Kind: "normal_write", Purpose: &purpose, Reason: "control"},
		"unknown kind":        {Name: "kit.bad", Owner: "KIT", Kind: "writer", Purpose: &purpose, Reason: "control"},
		"no runner or reason": {Name: "kit.bad", Owner: "KIT", Kind: "normal_write", Purpose: &purpose},
		"read with a purpose": {Name: "kit.bad", Owner: "KIT", Kind: "foreground_read", Purpose: &purpose, Reason: "control"},
		"held without intent": {Name: "kit.bad", Owner: "KIT", Kind: "normal_write", Reason: "control", Run: canonicalInternalRun(runCanonicalHeldN1)},
		"empty override":      {Name: "kit.bad", Owner: "KIT", Kind: "foreground_read", Reason: "control", WALOverride: &governor.WALPolicy{}},
		"degraded override":   {Name: "kit.bad", Owner: "KIT", Kind: "foreground_read", Reason: "control", WALOverride: &governor.WALPolicy{HardBytes: 1 << 20, ActivePressureBytes: 1 << 20, MaxSampleAge: 1, CheckpointMaxHold: 1}},
	} {
		if _, err := classTargetStartGate(classTargetEntry(spec), classTargetStart{}); err == nil {
			t.Fatalf("%s: an incomplete declaration must not start", name)
		}
	}
}

// ONE way: a class that did not register through the kit cannot start.
func TestClassTargetKitRefusesAClassThatDidNotUseIt(t *testing.T) {
	if _, err := beginClassTarget(t, "inventory_n1", classTargetParts{}); !errors.Is(err, ErrClassTargetUnbound) {
		t.Fatalf("a class outside the kit must not start through it, got %v", err)
	}
	if _, err := beginClassTarget(t, "caller_sql", classTargetParts{}); err == nil {
		t.Fatal("an unknown class must not start")
	}
}

// The WC proof classes are declared and started through the kit: they carry a
// kit registration, and with no composed bindings they refuse to start.
func TestCanonicalWCClassesUseTheClassTargetKit(t *testing.T) {
	for _, class := range []string{"progress", "security", "wc_reclaim"} {
		definition, err := canonicalHeldClassFor(class)
		if err != nil {
			t.Fatal(err)
		}
		if definition.target == nil || definition.wal.Policy != governor.Phase1aWALPolicy() || definition.wal.Explicit {
			t.Fatalf("%s: WC must run on the adopted policy through the kit", class)
		}
		if _, err := beginClassTarget(t, class, classTargetParts{}); !errors.Is(err, ErrClassTargetUnbound) {
			t.Fatalf("%s: an unbound WC start must refuse, got %v", class, err)
		}
	}
}
