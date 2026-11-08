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

Roll-up rows come from the downloaded artifacts, not from hand summarisation:
`go run ./internal/store/qual/cmd/rollup -artifacts DIR -out DIR` emits markdown
and JSON with per-VM tables, each cell's own `target.json` flag and a computed
verdict. See `server/internal/store/qual/cmd/rollup/README.md`. A row cannot
pass if a cell's target flag is false, a profile lacks three distinct boot
identities, a population is short, or a cell stopped or refused.

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
and connection-close evidence. Before hashing/upload, cleanup requires the own
unit to be stopped and its cgroup absent or unpopulated. It removes only fixed
task-owned scratch, repairs private Go module directory permissions, and retains
cleanup.json with the artifact. Evidence is removed locally only after upload. Missing artifacts, failed cells, bad pins,
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

The raw NDJSON transport ceiling is16GiB per cell, parsed one bounded record at
a time (at most2MiB/record and1M records). This is an offline evidence-file
limit, not a Go heap, writer, WAL, IO or hardware/profile allowance. The retained
failed N1 prefix had median234,635-byte attempt records; 30,000 role records alone
project to7,039,050,000 bytes, so the former4GiB verifier limit could reject a
fully observed10k-original cohort. No raw fields are trimmed to fit. Files above
the new ceiling, oversized/truncated records and incomplete owner finals still
fail visibly and retain their original evidence.

The full wire verifier runs on each runner before a successful cell can be
reported complete. After unit retirement, finalization gzip-compresses the
closed raw stream, verifies its original byte count/SHA by decompressing it,
and compares the successful verifier's digest before removing the uncompressed
copy. Artifacts retain `raw.ndjson.gz`, `raw-storage.json` (original and stored
length/digest, wire-verification flag and compression interval), summaries,
host/build/policy metadata and hashes. Failed prefixes remain failed and are
also compressed; round-trip integrity never promotes them to verified wire or
qualification. A digest/compression error keeps the original and fails closed.
Download the compressed representation and metadata; do not expand all six
large streams on mini-1. JSON readers can stream through `gzip.open`.

Cell receipts also record filesystem total/used/free immediately before the
child starts and after it exits. These are checkpoints, not minimum free space
or peak disk usage. Actions step timestamps retain compression/upload elapsed
time; inspect these and the first redispatch's receipts for runner-disk pressure.

Raw collection in the next method epoch sends the unchanged Go JSON record
bytes over a bounded Unix socket to an external streaming-gzip observer. Its
768 MiB dedicated `tmpfs,noswap` has a 767 MiB compressed-file ceiling (1 MiB is
reserved for socket/receipts), plus the unchanged 16 GiB decompressed/raw bound.
Every compressed write checks the cap before writing. Exceeding either cap
fails the cell explicitly, closes the producer socket and retains the failed
compressed prefix; it cannot become a successful truncated run. Missing or
mismatched mount evidence refuses execution; there is no database-disk fallback.
Linux documents `noswap`: https://docs.kernel.org/filesystems/tmpfs.html .

The observer runs outside the engine systemd unit. Its gzip workspace and tmpfs
pages are allocated by that external process, not charged to the engine's
unchanged cgroup limit and excluded from EC5 server accounting. The supervisor
asserts actual observer PID/cgroup placement outside the engine cgroup before
accepting a capture. Bounded socket buffers and record encoding still have
observer overhead in the producer; compression CPU can compete on the host.
This is a new observation method, not an uninstrumented or EC5 fit claim.
Receipts retain actual peak compressed spool bytes beside engine `memory.peak`.
The previous largest complete cell compressed to 531,186,672 bytes, below the
804,257,792-byte compressed ceiling. Caps never grow automatically.

Producer EOF closes/flushed gzip (CRC and trailer), then an independent streaming
decompression must match the original byte count and SHA-256. The engine
supervisor waits for this proof before its unit retires, including failed owner
prefixes. Only after stopped/drained unit proof does the runner copy compressed
bytes to disk, recheck compressed digest/length and round-trip, then remove the
memory copy. Failed/incomplete gzip prefixes remain failed and are retained
byte-for-byte without claiming successful decompression. Unmount refuses any
unpersisted data. Mount, capture, persistence and cleanup proofs precede upload.

A PR-only standard-runner protocol job exercises the actual no-swap tmpfs,
external cgroup placement, close/round-trip and cleanup with a tiny synthetic
stream, never SQLite or qualification. Focused controls cover cap overflow,
round-trip mismatch, same-cgroup rejection and unchanged JSON/null/zero content.
No command budgets, per-record fields, SQLite policy or runtime enforcement change.


## Early SCREENING and checkpoint components

Set `purpose=screening` for early failure discovery, never sign-off. A pushed
tooling branch is allowed only in this mode; workload source must still be a
merged main SHA. Default qualification remains main-tooling-only. Purpose is
explicit in run names/receipts. Bounds, source/schema/engine checks and both
six-VM hardware profiles stay unchanged. Earlier dispatches made before this
input existed are labelled by their external QL dispatch receipt/report.

`checkpoint_1mib`, `checkpoint_8mib`, `checkpoint_64mib`, and
`checkpoint_hardcap` invoke the existing merged GV `TestDiagnosticConcurrentKeeper`
case with `-test.count=10000 -test.failfast`. Each iteration creates its original
component fixture; no same-owner reset/retry or new SQL is added. These fixtures
are SMALL physical-frame components, NOT a canonical1M catalogue (the downloaded
seed is unused). They retain the existing eight-grant component config and
192MiB hard/16MiB reserve/64MiB soft debt/96MiB pressure; no N1/publisher profile
claim. IO is truthfully declared unrestricted on standard runners, distinct from
the capped Netcup comparison. Hard-prefix preparation can hit the fixed75min
test limit; retain the incomplete prefix, never replace it or invent10k samples.

Verbose GV records stream through the external capped gzip observer. Native
checkpoint HOLDs remain separate from writer holds and recovery. A 10ms external
host+actual-engine timeline retains raw vmstat/meminfo/IO PSI/memory.stat/io.stat,
actual page bytes, timestamps/gaps and unavailable reasons; no missing zeroes or
OS attribution. It is closed and copied after unit drain. All checkpoint runs
are SCREENING with sign_off_eligible=false; normal EC1 thresholds are not a
maintenance gate. Per-record raw fields, native policies and fixture limits are
unchanged. Screening reports empirical p99 only for a complete10k cell; short
prefixes keep N/max/errors and withhold a qualified quantile.
