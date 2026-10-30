# Real-scan 300k ingest round

The replacement for the earlier1M/5M fixture is **300,000 distinct real files**:
90,000 movies,150,000 episodes,60,000 music tracks. No product DB is fabricated
or transplanted. Every file is a valid, independently hashed container accepted
by the scanner, with one inode and unique metadata; there are no hardlinks.
This tests discovery/inventory and normal use, not video fidelity or80/100-user
capacity. Default commands only print plans.

`media.py` encodes tiny source templates and remuxes unique valid0.1s files.
It probes each result. Realistic hierarchy: one movie/title directory,20 episodes
per show/two seasons,10 tracks/album/five albums/artist. No millions-to-one asset
fan-in. Files are generated in six50k phases; each scan runs through the real
`POST /v1/libraries/{id}/scans` API. Leaf pivots are movies/episodes/songs.

`orchestrate.py` records each50k bucket's accepted items/s over generation+scan, separate scan/generation elapsed, writer
share and writer milliseconds/item. Writer basis is the **all-class cumulative
writeGate.heldMillis delta**, including concurrent foreground work; a per-class
duration counter is not published. Memory is owner process.heapAllocBytes,
explicitly not RSS. The quota watcher additionally measures kernel filesystem
allocation and250ms process-tree RSS peaks per phase (up to64 owned descendants). Media, appdata and WAL share one8GiB ext4 loop filesystem, with an
O(1)250ms7GiB stop watcher and a2h ingest deadline; ordinary host filesystems
are refused for300k generation. No host disk filler is used.

Concurrent `load.ts --ingest` uses10 real daily users by default (1..20 allowed),
plus one quiet member and owner. All libraries are declared before member grants.
Users browse the growing movie library and use search, Home, Settings, real
long-source direct play/progress, Reading and Saved. Canonical refresh uses a
separate child per installation and private atomic stores. The conversion engine
may be available; every measured session still must actually deliver directly.
The80/100 direct-only campaign's stricter configuration gate is unchanged.

Before/during/after p50/p99 are recorded per operation, with six bucket phases.
At50k/150k/300k the operator also samples browse in all three libraries, search
and Home20 times each. Completion requires two stable exact leaf and availability
counts, no unavailable media, and stable first200 values of two published facets
per library. Empty facets are reported honestly; this is bounded facet sampling.
No generated filename count substitutes for an API count.

`load.ts` fails missing phases/markers or fewer than20 samples/operation/phase.
Default slowdown gates: p50<=baseline*1.2+10ms; p99<=baseline*1.2+50ms and<=2s.
`summarize.py` requires the full300k evidence and convergence, and flags climbing
writer time/item (>20%+1ms), falling ingest rate (>20%), and climbing resident
latency. These are operational thresholds, not a claim that the curves are flat.
Any rise is reported with the raw curve for review and entered in OUTSTANDING.md.

## Exact supervised command (test round only; not run during preparation)

Use a designated disposable root host with20GiB free disk and adequate RAM.
Keep Hosted running on core; do not run the8GiB pressure tier there. Inputs are
qualified Node24.21.0, the current server/FFmpeg/ffprobe/rclone, a captured0600
capability document, and a small direct-play source with measured duration>=7350s.
A source can be made with the qualified FFmpeg outside the quota:

```sh
umask 077
ffmpeg -nostdin -v error -f lavfi -i color=s=128x72:r=1 \
  -f lavfi -i sine=frequency=440:sample_rate=16000 -t 7350 \
  -c:v libx264 -preset ultrafast -pix_fmt yuv420p -b:v 12k \
  -c:a aac -b:a 12k "$PRIVATE_INPUT/sustained-7350s.mp4"
export PORTICO_FIXTURE_DISPOSABLE=yes
export PORTICO_FFMPEG="$BIN/ffmpeg" PORTICO_FFPROBE="$BIN/ffprobe"
export PATH="$BIN:$PATH"
python3 scripts/batch-round/ingest/run.py --execute \
  --root "$HOME/astra-scratch/P2-65/round-300k" \
  --server-binary "$BIN/portico-server" --node "$BIN/node" \
  --source "$PRIVATE_INPUT/sustained-7350s.mp4" \
  --capabilities "$PRIVATE_INPUT/capabilities.json" --users 10 --port 32665
```

Without `--execute`, the supervisor creates nothing. It builds the real baseline
appdata via ordinary setup/scans, starts the owned server, declares growing
libraries, provisions accounts, binds the manifest to the stopped prep receipt,
then starts load, scans and the kernel observer. Source playback outlasts the
full7200s measurement+ramp. It records failures and stops its own process groups
in `finally`; no broad pkill or Docker action is used. The quota/appdata and
credentials remain private for collection. After collecting evidence and any
reusable inputs, remove only this round:

```sh
python3 scripts/batch-round/ingest/quota.py cleanup \
  --root "$HOME/astra-scratch/P2-65/round-300k/quota" --execute
# Remove this owned round directory after the unmount succeeds and evidence is collected.
```

For a tiny independent light rehearsal:

```sh
python3 scripts/batch-round/ingest/bootstrap_tiny.py --execute \
  --server-binary "$BIN/portico-server" --appdata "$SCRATCH/appdata" \
  --scratch "$SCRATCH/media" --marker "$SCRATCH/markers.jsonl" \
  --facts "$SCRATCH/facts.json" --credentials "$SCRATCH/credentials.json" \
  --control "$SCRATCH/control.json" --port 32666 \
  --ffmpeg "$BIN/ffmpeg" --ffprobe "$BIN/ffprobe"
```

Light receipt:100 exact leaves (30/50/20),100 distinct hashes/inodes, no links;
606,208 physical media bytes including directories; all available and stable
published facet samples. The final rehearsal bucket was27.291s including1.535s generation and25.76s
scan/readback:3.664 items/s, all-writer132.11ms/item, share0.484, heap13,956,816B. These tiny measurements
are **not** the300k curve. The real300k, joined daily-use trend and full backup
under ingest remain unrun. Original receipts are never overwritten.
