package writer

// The canonical class-target kit: ONE way to add a qualification class target.
//
// A class is declared once as a classTargetSpec and registered with
// classTargetEntry. beginClassTarget is then the ONE way to start it: it wires
// the keeper recovery hook, applies governor.Phase1aWALPolicy unless the class
// names an override explicitly, and refuses to return a runtime that would run
// degraded. A class that did not register through the kit cannot start, so a
// lane cannot copy-paste a canonical_*_binding_test.go and drift.
//
// See CLASS_TARGET_KIT.md for the whole shape of a new class.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

var (
	// ErrClassTargetUnbound means a class target was started without a binding
	// it requires. The kit refuses the start rather than run degraded.
	ErrClassTargetUnbound = errors.New("qualification class target missing required bindings")
	// ErrClassTargetRegistration means a compiled class declaration is invalid.
	ErrClassTargetRegistration = errors.New("invalid qualification class target")
)

// classTargetWAL is the WAL policy a class runs on, plus where it came from.
// Explicit is true only when the class named its own policy; the adopted
// governor.Phase1aWALPolicy() is never reported as an override.
type classTargetWAL struct {
	Policy   governor.WALPolicy
	Explicit bool
}

// classTargetWALDefault is the adopted phase 1a policy. It is the policy of
// every class target unless that class passes a WALOverride.
func classTargetWALDefault() classTargetWAL {
	return classTargetWAL{Policy: governor.Phase1aWALPolicy()}
}

// resolveClassTargetWAL applies the adopted default unless override is non-nil.
// There is no zero-value or inferred way to change the policy: an incomplete
// override is a registration error, not a silent fallback.
func resolveClassTargetWAL(override *governor.WALPolicy) (classTargetWAL, error) {
	if override == nil {
		return classTargetWALDefault(), nil
	}
	p := *override
	if p.HardBytes <= 0 || p.ReserveBytes < 0 || p.SoftDebtBytes < 0 || p.SoftDebtBytes >= p.HardBytes ||
		p.ActivePressureBytes <= 0 || p.ActivePressureBytes >= p.HardBytes || p.MaxSampleAge <= 0 ||
		p.CheckpointMaxHold <= 0 || !p.ConcurrentPassive {
		return classTargetWAL{}, fmt.Errorf("%w: a WAL override must be a complete adopted-shape policy with concurrent PASSIVE", ErrClassTargetRegistration)
	}
	return classTargetWAL{Policy: p, Explicit: true}, nil
}

// classTargetSpec is the ONE class declaration. Register it with
// classTargetEntry; there is no second way to create a class.
type classTargetSpec struct {
	Name, Owner, Kind string
	// Purpose is the compiled command intent the class measures, if any.
	Purpose *CommandID
	// Reason is the honest statement-bytes/binding status records keep.
	Reason string
	// Run is the compiled runner. Nil means a declared but unavailable target,
	// and Reason must say which owner binding is missing.
	Run canonicalClassRun
	// WALOverride replaces the adopted WAL policy. Nil keeps
	// governor.Phase1aWALPolicy(). A screening variant must pass one explicitly.
	WALOverride *governor.WALPolicy
}

// requiresKeeper is derived, never declared: every class that takes a writer
// HOLD gets the keeper recovery hook by default. Only foreground reads, which
// never take a writer HOLD, opt out.
func (s classTargetSpec) requiresKeeper() bool {
	return s.Kind != "foreground_read"
}

// registerClassTarget validates a compiled declaration and resolves the
// bindings the kit applies at start.
func registerClassTarget(spec classTargetSpec) (canonicalHeldClass, error) {
	if spec.Name == "" || spec.Owner == "" || spec.Kind == "" {
		return canonicalHeldClass{}, fmt.Errorf("%w: name, owner and kind are required", ErrClassTargetRegistration)
	}
	switch spec.Kind {
	case "normal_write", "maintenance", "foreground_read":
	default:
		return canonicalHeldClass{}, fmt.Errorf("%w: %s has unknown kind %q", ErrClassTargetRegistration, spec.Name, spec.Kind)
	}
	if spec.Run == nil && spec.Reason == "" {
		return canonicalHeldClass{}, fmt.Errorf("%w: %s has no runner and no unavailable reason", ErrClassTargetRegistration, spec.Name)
	}
	if spec.Kind == "foreground_read" && spec.Purpose != nil {
		return canonicalHeldClass{}, fmt.Errorf("%w: %s is a read and never owns a writer command purpose", ErrClassTargetRegistration, spec.Name)
	}
	if spec.requiresKeeper() && spec.Run != nil && spec.Purpose == nil {
		return canonicalHeldClass{}, fmt.Errorf("%w: %s takes a writer HOLD without a command purpose for its keeper", ErrClassTargetRegistration, spec.Name)
	}
	wal, err := resolveClassTargetWAL(spec.WALOverride)
	if err != nil {
		return canonicalHeldClass{}, err
	}
	declared := spec
	return canonicalHeldClass{
		name:    spec.Name,
		owner:   spec.Owner,
		kind:    spec.Kind,
		purpose: spec.Purpose,
		reason:  spec.Reason,
		run:     spec.Run,
		target:  &declared,
		wal:     wal,
	}, nil
}

// classTargetEntry registers a compiled class declaration. A declaration that
// cannot be registered stays in the closed list carrying its error, so lookup
// and start refuse visibly; it never silently becomes a runnable class.
func classTargetEntry(spec classTargetSpec) canonicalHeldClass {
	registered, err := registerClassTarget(spec)
	if err == nil {
		return registered
	}
	return canonicalHeldClass{name: spec.Name, owner: spec.Owner, kind: spec.Kind, reason: spec.Reason, invalid: err}
}

// classTargetKeeper is the keeper recovery hook. The canonical funded owner
// implements it; the kit only proves the hook is present and healthy.
type classTargetKeeper interface {
	classTargetRecoveryHook() error
}

// classTargetRecoveryHook is the ONE keeper implementation: the owner must hold
// the actual keeper it opened, and the writer's executor must hold its bound
// service (checked by the gate).
func (m *canonicalN1MaintenanceOwner) classTargetRecoveryHook() error {
	if m == nil || m.keeper == nil {
		return fmt.Errorf("%w: no keeper opened by this owner", ErrClassTargetUnbound)
	}
	return nil
}

// classTargetParts are the actual owners a class hands the kit. Never SQL,
// grants, profiles or callbacks.
type classTargetParts struct {
	Opening   *scanOpening
	Governor  governor.Governor
	Authority *governor.Authority
	WAL       *governor.WALGate
	Path      string
	Writer    *Writer
	// Keeper is an already-wired keeper hook. Nil lets the kit wire the ONE
	// funded keeper itself.
	Keeper *canonicalN1MaintenanceOwner
}

// classTargetRuntime is a class that passed the start gate.
type classTargetRuntime struct {
	Class  canonicalHeldClass
	Writer *Writer
	Keeper *canonicalN1MaintenanceOwner
	WAL    classTargetWAL
}

// classTargetStart is what a composed class hands the gate.
type classTargetStart struct {
	Writer *Writer
	Keeper classTargetKeeper
}

// classTargetStartGate is the ONE refusing start gate. A held class without its
// keeper recovery hook, pressure-recovery maintenance hook or WAL policy gate
// does not start: it returns ErrClassTargetUnbound instead of running degraded.
func classTargetStartGate(class canonicalHeldClass, start classTargetStart) (classTargetWAL, error) {
	if class.invalid != nil {
		return classTargetWAL{}, class.invalid
	}
	target := class.target
	if target == nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s is not registered through the class-target kit", ErrClassTargetUnbound, class.name)
	}
	if class.wal.Policy.HardBytes <= 0 {
		return classTargetWAL{}, fmt.Errorf("%w: %s has no WAL policy", ErrClassTargetUnbound, class.name)
	}
	if !target.requiresKeeper() {
		return class.wal, nil
	}
	if start.Writer == nil || start.Writer.effective == nil || start.Writer.effective.executor == nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s has no writer/executor", ErrClassTargetUnbound, class.name)
	}
	if start.Writer.effective.wal == nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s has no WAL policy gate", ErrClassTargetUnbound, class.name)
	}
	if start.Writer.effective.maintenance == nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s has no funded keeper pressure-recovery hook", ErrClassTargetUnbound, class.name)
	}
	if start.Keeper == nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s has no keeper recovery hook", ErrClassTargetUnbound, class.name)
	}
	if err := start.Keeper.classTargetRecoveryHook(); err != nil {
		return classTargetWAL{}, err
	}
	service := start.Writer.effective.executor.keeper.Load()
	if service == nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s keeper is not bound to this writer", ErrClassTargetUnbound, class.name)
	}
	if err := service.Check(); err != nil {
		return classTargetWAL{}, fmt.Errorf("%w: %s keeper recovery hook unhealthy: %v", ErrClassTargetUnbound, class.name, err)
	}
	return class.wal, nil
}

// newClassTargetKeeper is the ONE keeper recovery hook wiring: it funds the
// keeper owner and binds it to the class's writer opening so pressure recovery
// runs before admission. A class must not open a second or advisory keeper.
func newClassTargetKeeper(t *testing.T, class canonicalHeldClass, parts classTargetParts) *canonicalN1MaintenanceOwner {
	t.Helper()
	if class.purpose == nil || parts.Opening == nil || parts.Governor == nil || parts.Authority == nil || parts.WAL == nil || parts.Path == "" {
		t.Fatal(fmt.Errorf("%w: %s needs a command purpose and the actual owners to wire its keeper", ErrClassTargetUnbound, class.name))
	}
	return newCanonicalCheckpointOwner(t, parts.Opening, parts.Governor, parts.Authority, parts.WAL, parts.Path,
		canonicalCheckpointShape{domain: *class.purpose, prefix: "canonical-" + class.name + "-cp", hardBytes: class.wal.Policy.HardBytes})
}

// beginClassTarget is the ONE way to start a class target. It resolves the
// compiled class, wires the keeper recovery hook when the class takes a writer
// HOLD, resolves the adopted WAL policy and refuses a runtime that would run
// degraded.
func beginClassTarget(t *testing.T, class string, parts classTargetParts) (classTargetRuntime, error) {
	t.Helper()
	definition, err := canonicalHeldClassFor(class)
	if err != nil {
		return classTargetRuntime{}, err
	}
	if definition.run == nil {
		return classTargetRuntime{}, fmt.Errorf("%w: %s has no compiled runner of its own", ErrClassTargetUnbound, class)
	}
	if definition.target == nil {
		return classTargetRuntime{}, fmt.Errorf("%w: %s is not registered through the class-target kit", ErrClassTargetUnbound, class)
	}
	keeper := parts.Keeper
	if definition.target.requiresKeeper() && keeper == nil {
		if parts.Opening == nil || parts.Governor == nil || parts.Authority == nil || parts.WAL == nil || parts.Path == "" {
			return classTargetRuntime{}, fmt.Errorf("%w: %s needs a keeper and the actual owners to wire one", ErrClassTargetUnbound, class)
		}
		keeper = newClassTargetKeeper(t, definition, parts)
	}
	wal, err := classTargetStartGate(definition, classTargetStart{Writer: parts.Writer, Keeper: keeper})
	if err != nil {
		return classTargetRuntime{}, err
	}
	return classTargetRuntime{Class: definition, Writer: parts.Writer, Keeper: keeper, WAL: wal}, nil
}
