# Canonical per-class targets

These closed TEST-ONLY entrypoints are scaffolding for QL's ONE canonical 1M
population. `inventory_n1` is executable through the existing test-only N1
owner and a real funded concurrent PASSIVE keeper. The other classes emit
explicit failed bindings. No production issuer, profile, SQL callback, authority
or qualification report is introduced. Merged command bodies alone do not open
an accepting workload path.

| Class | Exact Go parent | Owner / current binding |
| --- | --- | --- |
| inventory_n1 | TestCanonicalHeldInventoryN1 | SC/W N1 original/progressive owner |
| basic_accept | TestCanonicalHeldBasic | SC/W accepted Basic owner |
| staged_enrich | TestCanonicalHeldStagedEnrich | EN/W staged owner |
| progress | TestCanonicalHeldProgress | WC actual Progress Action |
| security | TestCanonicalHeldSecurity | WC actual FULL Security Action |
| wc_reclaim | TestCanonicalHeldWCReclaim | WC retained receipt/source reclaim; purpose unavailable, never publisher reclaim |
| publisher_fragment | TestCanonicalHeldPublisherFragment | PB accepting fragment composition |
| publisher_flip | TestCanonicalHeldPublisherFlip | PB accepting head-publish/flip composition |
| checkpoint | TestCanonicalHeldCheckpoint | GV governed maintenance owner, outside writer HOLD |
| mp.queue_head | TestCanonicalQueueHeadRead | MP current device-owned queue, proposed6lookup+6final statements |
| mp.session | TestCanonicalSessionHeadRead | MP current session point read, proposed6+6 |
| mp.preparation | TestCanonicalPreparationRead | MP prepared-job point read, proposed7+7 |
| rb.sorted | TestCanonicalRBSortedRead | RB ten scalar orders/directions, proposed11/11–12SQL debt |
| rb.chronology | TestCanonicalRBChronologyRead | RB dated/NULL/content chronology, proposed11–13SQL debt |

The MP and RB rows are foreground reads through ConsumeForeground, NOT held
writer/COMMIT samples. Source/authority drift, byte admission, statement bytes,
original ready/consumer timing and concurrent-scan comparisons remain required.
RB requires raw64/output40 and actual per-domain Reader Capture receipts;
phase1a lease remains OFF (#1337 is phase2-only opt-in). Hints from pending MP/RB handoffs are not observed native facts or execution pins.

Default runs skip these targets. Explicit opt-in requires `-canonical-held=true`,
`PORTICO_CANONICAL_HELD=1` and `PORTICO_CANONICAL_HELD_CONFIG` naming a strict JSON
object with exactly these fields:

```json
{
  "run_id": "class-profile-cell",
  "host_epoch": "externally-proved-host-epoch",
  "source_commit": "0000000000000000000000000000000000000000",
  "output_path": "/owned/scratch/class.ndjson",
  "class": "progress",
  "profile": "standard",
  "attempts": 10000,
  "artifact_directory": "/owned/scratch/canonical-artifact",
  "manifest_sha256": "0000000000000000000000000000000000000000000000000000000000000000"
}
```

The example pins are placeholders, never execution evidence. Use the trusted
producer receipt's actual manifest digest, NOT a digest computed from the same
untrusted file. Profiles are standard, legacy8, ram2gb or reference declarations;
the target does not install settings or attest host provenance. Requested
originals are bounded10000..1000000; this does not change owner command caps.
Select exactly one matching parent/class. Unknown fields, duplicate fields,
trailing input, bad pins and mismatched parent/class fail before execution.

New wire: `portico-canonical-held-class-v1`. A durable start records requested
class/count/profile and expected1M population, with actual population/owner
intervals/statement bytes null until observed. Failed seed/Action bindings are
written individually, then a nonpassing final with no report. Empty/refusal-only
populations never become fake zero-quantile reports. Compiled purpose hints are
intent metadata; actual grant class/durability remain null.

QL's merged seam is `UseQualificationPopulation(t, absoluteArtifactDirectory,
pinnedManifestSHA)` immediately BEFORE the existing owner constructor for the
EXACT same t. It copies the artifact through the existing canonical fixture
attachment, never opens the artifact in place or changes opener/issuer. The
initial schema010 shape is minimal current movies in numeric library7, key
qualification-movies, root /qualification-media, entity/asset IDs1e9+ordinal.
The class must adapt its existing owner setup to THAT populated parent, rather
than reinserting library7 or measuring a tiny unrelated library. Receipts,
inventory/probe/enrichment/search states, seals and command authority are absent
and must come from the actual reviewed owner composition, never a seeder clone.

The common attachment now calls the exact merged UseQualificationPopulation API,
checks the trusted manifest and requires exactly1M residents. Only inventory_n1
has a compiled runner. No pending WC/PB/MP/RB implementation is imported. The same
seed remains EC5's future scale input; there is no separate memory seed.

## First executable recipe

`inventory_n1` / `TestCanonicalHeldInventoryN1` / recipe
`canonical-n1-fresh-active-current-v1` uses the existing N1 seed journal factored
onto the populated library7, key qualification-movies, root /qualification-media.
It never reinserts the library, kind, catalogue, membership or age baselines.
The actual target-library count is observed before warmup and checked against
the pinned manifest. The resident baseline grows as new N1 originals arrive;
this is not a fixed-size reset or replay workload. It covers the synthetic
single-file, active-bank/current-age/present-unrated-bucket branch, not all N1
branches, legal-MAX, Basic or a production scan issuer.

The tiny real-clock `TestCanonicalN1PopulationAndFundedKeeper` control is also
opt-in: set `PORTICO_CANONICAL_N1_FUNCTIONAL=1` and select that exact test. Unset
skips before artifact setup; another value fails. A loaded merge-queue runner
can legitimately exceed the unchanged 25 ms quantum, so this measurement control
is excluded from default correctness CI. When explicitly run, its completeness
assertions remain strict and all refusal/error prefixes stay in the raw output
and test log. Default CI keeps the deterministic registry/config/clock controls.
This gate does not change the canonical qualification entrypoint or its caps.

Eight retained warmup originals precede the requested10000..1000000 measured
originals on ONE persistent measured writer connection. Every original takes
its actual fresh BEGIN, saved-original Resolve, ACK and joined current snapshot
before NextOriginal. All three roles have distinct attempt/physical IDs and
owner windows; Resolve/ACK do not inflate the original count. Warmup, errors,
refusals, fences, unknowns and interrupted prefixes stay raw. No retry, repair,
reset, cap increase, connection replacement or success-only renumbering occurs.
The existing per-turn ledger, nullable owner/native facts and observer scopes
are reused; timing mode omits native counter snapshots inside HOLD. Registry,
fixture setup and process observations remain outside HOLD. Fixture cleanup
callbacks and observer costs are included, not subtracted.

The merged GV keeper/checkpointer is attached through the actual canonical W
opening/native-file binding, SAME governor/authority/WAL gate. Writer limits stay
25ms/32MiB WAL forecast and4MiB preparation+4MiB reserve. WAL hard512MiB,
reserve1MiB, soft debt64MiB and active pressure256MiB stay unchanged. A separate
Recovery partition funds lifetime Reader1+4MiB; startup/active/shutdown each use
64KiB+CPU1000+1GiB IO (twice the unchanged hard prefix). The total declared
maintenance partition is Reader2/8MiB/CPU2000/2GiB IO, including the keeper's
nonborrowable shutdown floor. These are synthetic component resource figures,
NOT newly qualified bounds or production profile authority.

One initial real PASSIVE precedes warmup. A normal timer schedules further
PASSIVE calls at least50ms after the previous return, with separate5s maintenance
MaxHold and the whole hard-prefix page budget. Each N1 request carries fresh
actual gate RestartRisk. The checkpointer writes only into a bounded128-entry
value ledger; the owner flushes it after command return. No file sink runs from
the checkpoint worker inside a concurrent writer HOLD. Worker stop/join precedes
actual W close/Retired, keeper shutdown/native return, and final grant retirement.
Final completion requires that physical chain and zero remaining total usage.
Failed/partial/refused maintenance or raw-ledger overflow is visible and marks
the run incomplete. A WAL refusal caused by a missing/failed composition is a
harness gap, never a relabelled product failure or an empty passing report.

Wire stays portico-canonical-held-class-v1 with existing N1 attempt payloads.
`population_attached` binds the actual manifest, observed target count, recipe,
warmup, timing cohort and maintenance resources. `checkpoint` records preserve
actual CheckpointResult, errors, refusal and physical-safe facts, separately from
writer attempts. checkpoint.owner_clock has nullable started_ns, native_started_ns,
native_ended_ns, physical_returned_ns and retired_ns. These are derived with
n1NS(stream.origin, actualTime) before JSON, using the SAME origin as the W
owner_clock; missing zero-time phases remain null and observed relative0 remains0.
The wall-clock typed result is retained as raw data. These joins establish no
IO attribution, independent causality or profile pass. `final.steady` preserves role census, physical ordinals,
connection continuity and final native-close/usage facts. The raw target always
emits qualification=false and qualified_profile_ready=false; QL independently
checks source/binary/artifact/host/coverage and computes qualification results.
Absent native counters, xSync, device attribution and complete LIVE peaks remain
null with existing reasons. No raw counter is renamed a bound.

Normal held classes retain hosted p99<=10ms and quiet max<=25ms. Rare error
rollback/discard paths are separate, starting at100ms. Maintenance has its own
owner budget and cannot be counted as a normal writer hold. Read classes inherit
B-READS, not writer ceilings. No profile or grant is widened to meet these values.
QL owns >=3VMs/profile, >=10k actual holds/recipe, confidence bounds, failure and
coverage accounting, Netcup quiet-max scheduling, and final qualification.
