# Canonical per-class targets

These closed TEST-ONLY entrypoints are scaffolding for QL's ONE canonical 1M
population. Every class currently emits explicit failed bindings. They create
no database, grant, issuer, profile, SQL callback, authority or qualification
report. Merged command bodies alone do not open an accepting workload path.

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

QL's draft seam is `UseQualificationPopulation(t, absoluteArtifactDirectory,
pinnedManifestSHA)` immediately BEFORE the existing owner constructor for the
EXACT same t. It copies the artifact through the existing canonical fixture
attachment, never opens the artifact in place or changes opener/issuer. The
initial schema010 shape is minimal current movies in numeric library7, key
qualification-movies, root /qualification-media, entity/asset IDs1e9+ordinal.
The class must adapt its existing owner setup to THAT populated parent, rather
than reinserting library7 or measuring a tiny unrelated library. Receipts,
inventory/probe/enrichment/search states, seals and command authority are absent
and must come from the actual reviewed owner composition, never a seeder clone.

The compiled population attachment and all class runners are currently nil.
After QL's seed/transport merges, one common adapter consumes that exact seam;
each class then switches on in one small reviewed binding PR when its typed
Action/composition is merged. No unmerged QL/WC/PB/MP implementation is imported.
The same seed becomes EC5's scale input; there is no separate memory seed.

Normal held classes retain hosted p99<=10ms and quiet max<=25ms. Rare error
rollback/discard paths are separate, starting at100ms. Maintenance has its own
owner budget and cannot be counted as a normal writer hold. Read classes inherit
B-READS, not writer ceilings. No profile or grant is widened to meet these values.
QL owns >=3VMs/profile, >=10k actual holds/recipe, confidence bounds, failure and
coverage accounting, Netcup quiet-max scheduling, and final qualification.
