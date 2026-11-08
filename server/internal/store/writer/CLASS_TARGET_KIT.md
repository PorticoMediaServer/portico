# The canonical class-target kit

One way to add a qualification class target. A lane declares its class once and
starts it once; it does not copy a `canonical_*_binding_test.go`.

Files: `class_target_kit_test.go` (the kit) and
`class_target_kit_controls_test.go` (its refusing-gate controls). The proof
class is the WC path (`progress`, `security`, `wc_reclaim`), which is declared
and started through the kit.

## What the kit applies for you

- **Registration.** `classTargetEntry(classTargetSpec{...})` is the only
  constructor for a class. A declaration that cannot be registered stays in the
  closed list carrying its error, so lookup and start refuse instead of the
  class silently running.
- **Keeper recovery hook, by default.** Every class that takes a writer HOLD
  requires the funded keeper; only `foreground_read` classes opt out, derived
  from the kind so it cannot be forgotten. `beginClassTarget` wires the keeper
  with `newClassTargetKeeper` when the class has not supplied one.
- **Refusing start gate.** `classTargetStartGate` (inside `beginClassTarget`)
  refuses `ErrClassTargetUnbound` when the keeper owner, its bound writer
  service, the writer's pressure-recovery hook or the WAL policy gate is
  missing, and surfaces a `Check()` failure. It never starts degraded.
- **`governor.Phase1aWALPolicy()` by default.** The kit resolves the adopted
  policy at registration. A different policy is only reachable by passing
  `WALOverride` explicitly, is validated, and is recorded as `Explicit`. Screen
  fixtures are not class targets.

## Adding a class in under 20 lines

```go
// 1. Declare it once, in canonicalHeldClasses().
classTargetEntry(classTargetSpec{
    Name: "domain_command", Owner: "XX", Kind: "normal_write",
    Purpose: &purpose, // the compiled command intent the class measures
    Reason:  "what is unavailable and why it is not zero",
    Run:     canonicalInternalRun(runDomainClass),
})

// 2. Start it through the ONE entry point. The keeper is wired when Keeper is
//    nil; the gate refuses anything less than the full binding.
bound, err := beginClassTarget(t, "domain_command", classTargetParts{
    Opening: opening, Governor: g, Authority: a, WAL: wal, Path: path,
    Writer: w, // Keeper: owner — only when the composition already wired one
})
if err != nil {
    t.Fatal(err)
}
_ = bound // bound.WAL.Policy, bound.WAL.Explicit, bound.Keeper

// 3. Add the control to your controls test: classTargetStartGate(entry,
//    classTargetStart{Writer: w}) must fail for each missing binding.
```

`classTargetSpec.Kind` is one of `normal_write`, `maintenance` or
`foreground_read`. A `foreground_read` never owns a writer `Purpose` and never
needs a keeper. A held class without `Run` is an unavailable target and must
give a `Reason`; it is emitted as a non-passing `failed_binding` prefix.

## Limits

The gate verifies the bindings it can see from the composed runtime: the keeper
owner, its bound writer service health, the maintenance hook and the WAL policy
gate's presence. It does not read the WAL gate's private policy back out; the
class resolves and records that policy through the kit. WC's keeper is still
constructed by the hash-pinned `wc_component_fixture_test.go`; the gate proves
that same keeper is the wired, healthy hook.
