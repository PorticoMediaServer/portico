# Hosted canonical class cells

This dispatch path collects per-class evidence from reviewed main code on six
independent standard GitHub VMs: standard and legacy8, three VMs each. It does
not create an accepting composition, repair a profile or declare qualification.
The source owner must bind its actual canonical target first. An unavailable
binding fails visibly; an actual refusal/error stops the cell and keeps its raw
prefix. There is no retry, database reopen/reset, CP injection or cap increase.

After this tooling, the successful 1M producer and the class binding are reviewed
and merged, use the actual full main workload SHA and the independently checked
producer manifest pin:

```sh
gh workflow run store-qualification-class.yml --repo PorticoMediaServer/portico --ref main -f source_commit=REVIEWED_WORKLOAD_SHA -f class=inventory_n1 -f producer_run=SUCCESSFUL_PRODUCER_RUN -f producer_attempt=1 -f manifest_sha256=TRUSTED_MANIFEST_SHA256
```

Find the exact run with `gh run list -w store-qualification-class.yml`, then use
`gh run watch RUN_ID --exit-status`, `gh run view RUN_ID --log-failed` and
`gh run download RUN_ID`. Retain required evidence before the 30-day expiry.
An unsuccessful run is evidence to inspect, never a set of omitted cells.

The fixed normal-write class list is N1, Basic, staged enrichment, progress,
security, WC reclaim and the two publisher stages. The list is not an assertion
that these bodies are runnable. Reads, maintenance and error-path experiments
need their own composed targets and budgets; scalar-only/primitive success is
not a composed read-class receipt. Phase1a lease/preparation remains OFF.

Every class requests 10,000 originals, not an inferred 10,000 accepted holds.
The owner keeps its actual warmup and each recipe/phase separate. A successful
wire/exit means only owner-execution completion. Per-host per-recipe counts,
actual policy on every physical connection/replacement, custody/COMMIT clocks,
correctness/coverage witnesses and confidence bounds still go through QL's
reducer and roll-up. No timings are pooled across VMs, profiles, recipes,
warm/cold/error populations, sources or host epochs. Positive refusals prevent a
pass; short populations and unavailable facts never become zero quantiles.

The N1 binding plan uses one warm persistent original chain with eight retained
warmup originals and BEGIN/Resolve/ACK recorded separately. The exact reviewed
source defines its WAL profile and any separately funded maintenance. This
dispatcher adds none, never counts CP as a writer hold and never widens a cap.
Runnable does not promise10,000 accepted originals: actual refusal must remain
a stopped, nonpassing cell.

Where maintenance is composed, preserve its actual typed result and nullable
`checkpoint.owner_clock` intervals in the SAME origin as writer owner clocks.
Missing time points stay null; an observed relative clock point0 is legal.
Those joins permit observed overlap accounting, not a causal IO attribution.

All six jobs download the SAME successful producer attempt and image. The
producer workflow/API attempt, main tooling, receipt, source, manifest and full
image hashes are checked. The consumed schema must match exactly. Known seed,
projection, sort and module source paths must be unchanged since the producer;
QL still reviews compatibility before reusing an older source artifact. Never
relabel a schema010 image as011. After an embed change, use a newly generated
reviewed artifact. The minimal movie population is not all-kind/enriched or
production seal proof; the class owner adds its actual legal recipe state to
that populated library7 through its test-only composition.

The standard profile uses four allowed vCPUs /400% quota and unrestricted memory
on the 16GB-class VM. Legacy8 retains four allowed vCPUs with200% quota,
8,589,934,592-byte memory limit and zero swap. Effective limits/ancestors, IO and
real hierarchy/namespace evidence are asserted by the existing fail-closed
`qual/cmd/host` collector before and after the target. Unknown/unreadable evidence
fails the cell. Three job labels do not establish three VMs: the combined
summary also requires distinct actual boot identities within each profile.

The root supervisor lives in one own transient systemd unit so the collector
can read the real-root namespace evidence. The measured binary drops to the
original unprivileged runner UID/GID, in the SAME cgroup/namespace; inherited auth
and Actions file-command environments are removed. NoNewPrivileges applies.
This is a disposable GitHub-runner path, not the Netcup operator or a production
service. Permissions are read-only contents/actions, no user secrets, no release
or signing workflow calls. The read-only GitHub token is used only for artifact
and producer-attempt metadata, not passed to source code.

Builds have20-minute limits, each Go cell75minutes with a30-second hard watchdog,
the service80minutes and each job120minutes. Fail-fast is disabled. Source-owned
partial/error/fence facts and original identities remain untouched. A final
process-drain check supplements, never substitutes for, owner physical-return
and connection-close evidence. The unit is stopped before hashing/upload;
private scratch is then removed. Missing artifacts, failed cells, bad pins,
truncated streams or incomplete finals fail the execution summary. Cleanup or
profile failures do not disappear behind a normal target exit.

Artifacts include actual source/archive/binary/build-selected bundled-driver
identity, baseline population, actual config, host evidence, exact argv, stdout,
raw NDJSON, every failed receipt and file hashes. The summary is deliberately
NOT a timing verdict. It does not establish sustained WAL-BOUND/RESET, FULL
cadence, foreground/user-write wait, quiet maximum, a complete memory budget,
physical scan service or full phase1a acceptance.

Focused protocol controls: `python3 -B -m unittest discover -s ci -p test_qual_class.py`.
They use synthetic streams and receipts only. No native measurement is valid
from an unmerged dispatcher or an unbound class.
