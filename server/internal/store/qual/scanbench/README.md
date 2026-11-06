# Scan fixture and traversal harness

This first SP slice is **DIRECTIONAL, traversal-only**. It opens no catalogue,
executes no SQL engine, accepts no Inventory/Basic command and grants no absence
authority. `filename_visible_ns`, `basic_ns` and `engine` are JSON null. Successful
traversal and a matching file count do not mean library-ready or Basic-complete.

Secure generation uses retained directory handles on Linux/Darwin; other
platforms refuse rather than use an unsafe pathname fallback.

The library generator supports 400,000 independent physical files, in buckets of
128. Supply a small real media template. Every file copies its bytes, with fixed
file mtime and deterministic unique filename/year/seed; no hardlinks or sparse
media. The manifest hashes layout, seed, file count, template bytes/hash and fixed
mtime, excluding native inode/device/directory timestamps. Failure leaves an
incomplete caller-owned root without a completed manifest. Existing roots are
refused. Disk preflight leaves 20 GiB free plus conservative small-file overhead;
operators must also check filesystem occupancy, inode headroom and concurrent use.
Generation supports homogeneous MKV/MP4/AVI/audio/reading templates; this is not
a mixed-media/probe-cost qualification. An extension is not proof of valid media.

From the repository root, build into task-owned disk scratch:

```sh
GOMAXPROCS=2 go -C server build -o "$PWD/.private/sp-scratch/scanbench" ./internal/store/qual/cmd/scanbench
.private/sp-scratch/scanbench -mode generate -root /absolute/task-scratch/library -template /absolute/seed.mkv -files 1000 -seed 1
.private/sp-scratch/scanbench -root /absolute/task-scratch/library -source-sha <exact-source-sha> -storage ssd > /absolute/task-scratch/ssd.json
```

`source-sha` is explicitly a declaration, not a verified executable identity.
QL must bind executable hash, source SHA/archive hash, fixture manifest, engine
and exact command to its eventual receipts. This slice performs count-only
traversal validation, not full path/content reconciliation. Real file Basic must
validate bounded identity/media/local facts in the SC-owned driver.

The production scanner is untouched. The harness uses storage.Client under its
supervisor/containment and exact operation scope; it joins physical work even
on cancellation. Linux `InventoryPage(Stream=false, Limit=1)` is the native
getdents64 candidate: original decimal d_off is preserved, never recomputed from
file offsets or from slices of larger pages. No caller-selected cursor-kind is
minted here. Retained `Stream=true` and non-Linux `stream1:` session cursors need a
separate typed/versioned accepting/restart-custody contract; do not strip or
relabel them. The CLI refuses non-Linux FIRST runs.

DFS retains a cursor per ancestor and at most one observed name per turn.
Callback failure/refusal stops further reads. It supplies original request/page
values synchronously, including directory, issue and empty EOF turns. There is
no durable traversal-resume implementation in this slice. Re-running the walker
is a fresh traversal, not resolution of a lost response or uncertain COMMIT.

FIRST accepts only N1 new ordinary movie files with progressing nonempty native
cookies and a separately proved claimed source/run/lease/directory tuple. SC owns
the public constructor/reference seam (none on base f02070da). W owns actual
acceptance, receipt resolution, ACK and resource admission. Directory discovery,
empty EOF, ignored/unsupported entries, existing identity reuse and non-movie
kinds need their own accepting protocol. Do not skip those observations and call
a whole scan accepted. Basic follows actual Applied/historical resolution; the
Inventory info_epoch echo is not Basic. The IO→CPU probe seam has no real broker
here; retain whole-helper DeviceIO through physical completion. No fake
IOCompleted calls, probe callback or synthetic availability clock is provided.

QL schedules all 400k SSD/HDD runs. Mini-1 remains strictly under 100k; the CLI
requires `-scheduled-scale` elsewhere for 100k+, and rejects it on mini-1. No
workflow dispatch or reference-host access is authorized by these examples.
Full/race suites belong in CI; local tests use concurrency 2.

For a scheduled throttled-HDD cell, QL provisions a disposable scratch filesystem
and a delegated cgroup-v2 parent with `io` already in `cgroup.subtree_control`.
Generate/flush the fixture before measurement, keep the database on SSD, and run:

```sh
python3 scripts/schema-v1-scan/hdd.py --allow-scratch-io --parent /sys/fs/cgroup/<delegated-parent> --root /absolute/task-scratch/library --device <major:minor> --rbps 50000000 --riops 80 -- /absolute/task-scratch/scanbench -root /absolute/task-scratch/library -source-sha <exact-source-sha> -storage throttled-hdd -scheduled-scale > /absolute/task-scratch/hdd.json 2> /absolute/task-scratch/io-emulation.jsonl
```

The launcher requires explicit --allow-scratch-io, a private caller-owned
completed fixture root, and a delegated writable cgroup parent below the cgroup
root. System roots and incomplete/unmarked scratch roots are refused.
The launcher checks the root filesystem's actual major:minor, creates one new
leaf, sets only its read limits, then puts the child/helpers into that leaf.
It records io.max and io.stat and removes only its own drained leaf. It never
changes parent/global limits, mounts devices, drops global caches, provisions a
host or uses sudo. Lingering task descendants are killed in that exact leaf and
a failed physical drain retains it with an error. Missing delegation fails.
This privileged path is **UNRUN** in the first PR; pure argument controls run.

Block-rate and IOPS limits approximate a throttled device; they do not reproduce
seek geometry, rotational latency or real HDD fragmentation. Cached directory
metadata/media may bypass block IO entirely. The wrapper does not silently evict
host caches or claim cold media. QL must record actual scratch-device topology,
cache state and cgroup/physical IO deltas; a warm cache cell is not HDD evidence.
`requested_storage` is a requested label only. Host-before/after evidence retains
actual limits and missing counters. No throughput, memory-fit or exit-criterion-4
pass follows from these controls.
