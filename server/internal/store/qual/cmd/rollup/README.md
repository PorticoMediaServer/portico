# Qualification roll-up generator

`go run ./internal/store/qual/cmd/rollup -artifacts DIR -out DIR` reads downloaded
`store-qualification-class.yml` artifacts and writes `rollup.json` and
`rollup.md`. It is offline reporting: it opens no database, runs no target,
verifies no provenance and mints no authority. It exists so a qualification row
is generated from the raw evidence instead of hand-summarised.

The tool is Go because the gate must reuse the reviewed statistics in
`internal/store/qual` (`Summarize`, `EvaluateHostedHolds`, the one-sided 95%
p99 upper bound and the fourfold median-split rule). A second Python
implementation would duplicate the reviewed bound and could drift from it.

## Input

`-artifacts` is the directory downloaded from a run. Every directory under it
that holds a `receipt.json` is one cell; a run normally contributes six:
`canonical-class-standard-vm1..3` and `canonical-class-legacy8-vm1..3`.

A cell supplies:

| File | Use |
| --- | --- |
| `receipt.json` | identity, declared source/manifest/producer, status, target exit code, actual host epoch and boot id (`host_before.host.files["proc/sys/kernel/random/boot_id"]`) |
| `raw.ndjson.gz` or `raw.ndjson` | the owner's raw stream; read one bounded record at a time |
| `target.json` | the cell's own target flag (see below) |

`target.json` is the cell's own verdict input. The generator never derives a
target flag from timings, and a row cannot pass when one is false:

```json
{
  "version": "portico-class-target-v1",
  "class": "inventory_n1",
  "profile": "standard",
  "replicate": 1,
  "target": "per-host p99 U95 <= 10 ms for each measured role",
  "target_flag": true,
  "reason": "short ownership statement",
  "gate": "hosted_p99_u95_10ms",
  "min_holds_per_host": 10000,
  "synthetic": false
}
```

- `target_flag` is `true`, `false` or `null`. A missing file, or a `null` flag,
  means **unavailable**, which is non-passing.
- `gate` is `hosted_p99_u95_10ms` (default) for normal-write classes, or
  `cell_declared` for component classes whose own target is not the writer p99.
  A `cell_declared` stratum still enforces the population floor and failure
  counts, but does not apply the 10 ms p99 bound.
- `min_holds_per_host` defaults to 10,000. A lower value is refused unless
  `synthetic` is true; synthetic fixtures are clearly labelled.
- The same fields are accepted directly on `receipt.json` (`target_flag`,
  `target`, `target_reason`) so a cell need not add a second file.

Each raw record carries `schema: portico-canonical-held-class-v1`, a `sequence`
equal to its retained line number, and the cell's `class`, `profile`,
`declared_source_commit` and `host_epoch`. `attempt` records are reduced as
follows; the generator understands both a top-level attempt and the N1 nested
`attempt` object:

| Raw field | Meaning |
| --- | --- |
| `owner_clock.hold_ns` | the owner hold; only positive, error-free, unfenced attempts enter the population |
| `original_population` | warmup/measured/cold/etc.; populations are never pooled |
| `turn_role` (or `operation`/`phase`) | role within the recipe |
| `recipe` or the final record's `recipe` | recipe name |
| `error`, `refusal`, `frame_fenced`, `complete` | retained as failed/refused/fenced counts |

`final.complete: true`, the receipt status and a zero target exit code are all
required for a cell to count as complete. `failed_binding` marks a refused cell.
A stopped or refused cell is never dropped: its retained prefix counts, raw file
name and first problems stay in the report, and the row is non-passing.

## Rules enforced

- Timings are reduced per VM, per profile, per recipe and per population. There
  is no pooled quantile anywhere in the output.
- A profile passes only with three cells whose actual boot identities are
  distinct; a reused boot id or a duplicate replicate label is invalid.
- A row cannot pass when any cell's own target flag is false.
- A short population (fewer complete accepted holds than the cell declares) has
  every quantile withheld as `unavailable` and is non-passing. Unsupported
  p99.9 values are withheld even when the population passes its floor.
- Per-host per-recipe counts are shown for every cell.
- Failed commands and refusals never earn a pass. A stopped or refused cell is a
  non-passing row with its raw prefix retained, never omitted.
- The normal-write gate is the reviewed per-host p99 upper-95% bound against
  10 ms, with the reviewed median-split rule. For the standard 10,000 floor the
  tool calls `qual.EvaluateHostedHolds`; only the floor is parameterised when a
  synthetic or component cell declares a different one, and `cell_declared`
  classes skip the p99 comparison entirely.

## Output

`rollup.json` carries the overall verdict, one row per class, one entry per
profile with the distinct boot identities, one entry per cell with its status,
target declaration, retained prefix counts and per-stratum distributions, and
one gated entry per recipe/population/role with each VM's own distribution and
the reviewed gate report. `rollup.md` renders the same content as per-VM tables
and per-stratum gate tables.

The command exits non-zero when the overall verdict is not `pass`, after writing
both files.

## Not this tool

This is not the wire verifier and does not re-check `files.json`, artifact
hashes, producer provenance or the host profile. Those stay with
`ci/qual_class.py` and the reviewed workflow. It does not compute the quiet
maximum, the error-path ceiling or maintenance/service budgets, and it does not
turn any of them into a pass.
