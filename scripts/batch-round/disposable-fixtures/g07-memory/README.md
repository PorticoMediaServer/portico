# G07 — real conversion memory pressure (BC-101…108)

Runs on a disposable Docker host only. Everything is private to Docker: a
container-private `/memory-transcode` tmpfs, private named volumes for `/config`
and `/transcode`, and a private bridge network. The only host listener is the
server API published on `127.0.0.1:<port>` so the host-side driver can call it;
nothing binds 53/88/445 and no host mount, loop device, DNS change or host
service is created.

## Phases

| Phase | Container | `/memory-transcode` | `/transcode` | Expected |
| --- | --- | --- | --- | --- |
| `memory-2g` | `memory=2g` | 512 MiB, 8192 inodes | private volume | conversions grow in RAM, one spill per session, playback continues from disk |
| `memory-4g` | `memory=4g` | 512 MiB | private volume | same |
| `memory-8g` | `memory=8g` | 512 MiB | private volume | same |
| `parent-2g` | child `8g`, parent `2g` | 512 MiB | private volume | effective ceiling is the parent; same outcome |
| `floor-1g` | `memory=1g` | 512 MiB | private volume | the reserve floor refuses new RAM; playback uses disk |
| `disk-1g` | `memory=2g` | none | 1024 MiB loop filesystem | disk placement, eviction instead of OOM |
| `spill-fail` | `memory=4g` | 512 MiB | 256 MiB loop filesystem | memory placement, spill copy hits ENOSPC naturally on the tiny target, stays disk-pinned, no crash; a follow-up session still makes disk progress |
| `restart-after-spill` | `memory=4g` | 512 MiB | private volume | kill during/after the first spill, restart, then either the same session serves matching generation bytes or it ended and a follow-up session makes disk progress with no new spill |

## Disk ceiling: refusal first, scoped loop second

A Docker named volume shares the host filesystem. Filling one to reach a target
free space would consume whatever the host has free and can starve an unrelated
service, so this fixture **never fills host or volume space**. The disk phases
require an exact fixed-size filesystem:

- Without `--disk-mode loop`, the phase refuses and records `BLOCKED-ENV`.
- With `--disk-mode loop`, the operator must also set
  `PORTICO_FIXTURE_DISK_QUOTA=loop`; the runner creates a `truncate`d ext4 image
  under the phase's scratch directory, mounts it with
  `nosuid,nodev,noexec,noatime`, verifies the mount source and the filesystem
  size (`<= --disk-mib`), then bind-mounts it to the container.
- `spill-fail` uses a 256 MiB image, so the spill copy reaches ENOSPC naturally;
  no filler file exists.
- Cleanup unmounts, detaches the loop device, removes the image and the mount
  point on success and on failure. The generated `cleanup.sh` contains only
  validated fixed paths (names and state paths are restricted to
  `[a-z0-9-]`/a scratch prefix), and nothing is `eval`ed.

The parent phase on the Docker `systemd` cgroup driver needs a transient slice,
which is a host unit. It is not created unless the operator sets
`PORTICO_FIXTURE_ALLOW_TRANSIENT_SLICE=yes`; otherwise the phase records
`BLOCKED-ENV` with the driver name. On the `cgroupfs` driver the parent is a
private cgroup directory under `/sys/fs/cgroup`, created and removed by the
fixture.

## Commands

```sh
export PORTICO_FIXTURE_DISPOSABLE=yes
export PORTICO_IMAGE=ghcr.io/porticomediaserver/portico-server:<pin>
cd "$FIXTURE_SOURCE"
# Disk phases refuse unless the operator explicitly allows the scoped loop:
export PORTICO_FIXTURE_DISK_QUOTA=loop
scripts/batch-round/disposable-fixtures/g07-memory/run.sh --execute \
  --image "$PORTICO_IMAGE" --sessions 3 --seconds 120
# one phase only:
scripts/batch-round/disposable-fixtures/g07-memory/run.sh --execute \
  --image "$PORTICO_IMAGE" --phases memory-4g --seconds 180
```

Without `PORTICO_FIXTURE_DISK_QUOTA=loop`, `disk-1g` and `spill-fail` record
`BLOCKED-ENV` and create nothing; that is the expected result on a host where
the operator has not approved a loop mount. On the Docker `systemd` cgroup
driver, `parent-2g` likewise records `BLOCKED-ENV` unless
`PORTICO_FIXTURE_ALLOW_TRANSIENT_SLICE=yes`.

Media is generated once into
`~/astra-scratch/P2-65/disposable/g07/media` with `media.sh` (120 s, 1080p,
~12 Mbit/s, AAC + AC3, plus an MKV with an embedded SubRip track). Tokens and
the owner password stay in `~/astra-scratch/P2-65/disposable/secrets` at 0700/0600.

## What is sampled

`sample.py` records at 100 ms: host `MemAvailable`, every cgroup ancestor's
`memory.current/max/high/events`, server and descendant RSS, tmpfs statfs and
inode counts read through `nsenter` in the container mount namespace, disk bytes
and free space at 1 Hz, playback diagnostics at 1 Hz, and each session's state,
generation and presentation. It never reads command lines, environments or
database contents. `assert.py` evaluates the named gates and records
`PASS`, `FAIL` or `BLOCKED-ENV`; a missing sample is blocked, never green.

## Limits and honesty

- This is a software-conversion fixture. It does not establish hardware-encoder
  capacity or G01's no-software-fallback contract.
- `one-spill-per-session` is a conservative gate over the run: spills must be at
  least one and no more than the session count. A run with several sweeps needs
  the sampler timestamps read by the owner before making a stronger claim.
- The disk case is a private loop filesystem of an exact size. It never fills
  host or Docker-volume space; without the explicit loop approval the disk
  phases are `BLOCKED-ENV`, not a partial pass.
- Live web/native playback, focus and visuals remain G10 device work.
