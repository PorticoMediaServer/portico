# Final batch harnesses

These are operator tools, not CI entry points. No workflow, npm script, test inventory or timer calls them. Live execution requires an explicit flag and is refused in CI. Default invocations only describe the work. Contabo fresh setup is separately authorized now. Scale/load/device acceptance runs only after the implementation pin and batch-start gate are cleared.

- `scalebench.sh`: 1M/5M synthetic catalogue invocations; tiny mini-2 rehearsal is separate from acceptance.
- `load.ts`: mixed real-client-core workload. See `load-README.md` for private manifest, observation and media requirements, metrics and limits.
- `rebuild-test-server.py`: fresh test installation/state/library/accounts/claim orchestration. See `rebuild-README.md` for pinned fixture dependencies, private input and destructive-run controls.
- `claim-test-server.py` / `web-owner-move.mjs`: normal confirmed Local→Portico browser setup on mini-2; never setup-purpose on a Local server.
- `provision-test-accounts.py`: genuine API members, kids/guest profiles, restrictions and avatars using fresh library receipts; does not claim complete fixture state.
- `test-server-health.py`: readiness remains a failing status while busy; only three consecutive failed liveness probes restart the named Contabo test service. Installed by the fresh rebuild tool. Never treats readiness503 as healthy.
- `observe-host.py`: read-only Linux companion. Samples server PID CPU ticks/RSS/threads, host MemAvailable, physical WAL size, disk space, and every cgroup-v2 ancestor's memory/current/max/events. An optional volume gets statvfs ceiling/used/free/inodes. It never reads process command lines, environments or database contents. Output is exclusive-create mode 0600. Exits nonzero if the server disappears, its PID is reused, or the observed WAL exceeds 192 MiB. CPU ticks must be differenced using the host clock tick rate; they are not CPU percentages.

Example telemetry commands, to run only during the batch on the target host:

```sh
python3 scripts/batch-round/observe-host.py --pid "$SERVER_PID" --database "$STATE/server.sqlite" --seconds 1800 --out "$EVIDENCE/host.jsonl"
# Once the batch is cleared, append --execute. During isolated P2-45 stress,
# add --volume "$PRIVATE_TMPFS" --interval 0.1; no shared-host tmpfs stress.
```

Telemetry has a 2-hour/10-Hz ceiling. Filesystem sampling is observational and cannot establish an unsampled maximum. P2-45's kernel hard-volume bound, real FFmpeg child RSS/session/spill and hardware-engine evidence must also be captured by the Media owner; this sampler alone does not establish those gates. A database file without a WAL reads as zero WAL bytes, so correlate server lifetime and workload activity. Permission errors fail rather than turning into zero. This tool is not a backup scheduler or a way to mount memory.

Keep generated databases/media, private manifests, credentials and retained evidence outside Git. Use one task scratch folder, preserve evidence by source SHA/archive hash, stop owned processes, and remove disposable build/fixture scratch at handback.
