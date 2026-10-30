# Two-session GPU app rehearsal on mini-2

The coordinated light rehearsal **passed** after fixing private decode-capability
persistence. Same150s1080p source/accounts, each12,422,288 HLS bytes/five segments,
hardware h264 decoder and h264_vaapi encoder, zero software/unknown/fallback,
two concurrent observations3.23s apart and both sessions stopped. The controlled
runtime used its original matching appdata baseline plus the fix; current-main
focused tests/build separately passed. No capacity/device claim follows.

All P2-65 mini-2 scratch was removed after collection. Private inputs, original
appdata and receipts are archived outside Git and documented in the integration
report. The commands below are the reproducible preparation sequence for a fresh
isolated fixture; a later run must use a current matching server/appdata and an
available mini-2 window with uptime load<16. No Docker or system driver install.

Target host: `ehler-homelab-mini-2` (`100.113.197.98`), never Docker. Everything
lives under `/home/justin/astra-scratch/P2-65`.

## What is being proved

Two real playback sessions, started through client-core's `SessionsClient`
against a fresh private server, both convert a 150-second 1920x1080 H.264/AAC
colour source to `720p-high` while HLS is still draining. The hardware receipt
must show, for both sessions:

- hardware encoder and hardware decoder execution;
- a DRM render node (`/dev/dri/renderD128`);
- zero software/unknown/fallback launches;
- at least 1 MiB of real HLS media per session and at least two segments;
- at least two during-drain overlap samples with `activeConversionSessions >= 2`.

`scale_vaapi` VPP is known to fail on this host, so the app may scale with CPU
`swscale`. That is allowed: the encoder/decoder hardware requirement is
unchanged, and this fixture does not require a hardware scale stage.

## Layout and stop conditions

```sh
ROOT=$HOME/astra-scratch/P2-65/gpu-rehearsal
APPDATA=$ROOT/appdata-32665
MEDIA=$ROOT/media
PRIVATE=$ROOT/private
EVIDENCE=$ROOT/evidence
REPO=$HOME/astra-scratch/P2-65/repo
BIN=$HOME/astra-scratch/P2-65/bin
NODE=$BIN/node
FFMPEG=$BIN/ffmpeg
FFPROBE=$BIN/ffprobe
```

Stop and report, do not lower a gate, if any of these is true:

- the server binary is not the lead's current P2-65 build;
- `/dev/dri/renderD128` is absent, or the GPU wrapper does not initialise iHD;
- the diagnostics backend is `software` or unresolved, or no supported VAAPI
  probe is present;
- `conversionMode` is not `all`, or the conversion-session limit is present and
  below 2;
- the source is shorter than 150 seconds, not 1920x1080, or has no audio;
- the hardware receipt is not `pass`, or reports software/unknown/fallback,
  missing render node, missing bytes/segments or insufficient overlap.

## 1. Qualified tools and source snapshot

All commands are on mini-2. `node` must be 24.21.0 exactly.

```sh
"$NODE" --version          # v24.21.0
"$FFMPEG" -hide_banner -encoders 2>/dev/null | grep -E 'libx264|h264_vaapi'
ls -l /dev/dri/renderD128
```

Copy the current fixture source from the P2-65 worktree to the private repo
snapshot. This copies load-owned files without editing them. Re-run after any
source change.

```sh
rsync -a --delete --exclude node_modules --exclude .git \
  /home/justin/projects/Portico/.worktrees/astra/P2-65-server/packages/ \
  justin@100.113.197.98:~/astra-scratch/P2-65/repo/packages/
rsync -a --delete --exclude node_modules --exclude .git \
  /home/justin/projects/Portico/.worktrees/astra/P2-65-server/scripts/batch-round/ \
  justin@100.113.197.98:~/astra-scratch/P2-65/repo/scripts/batch-round/
```

The hardware fixture imports `../../../packages`; this layout satisfies it. The
factory is used from `$REPO/scripts/batch-round/provision-scale.py`; it is not
edited.

## 2. GPU wrapper for the server process

`provision-scale.py` starts the server with a minimal environment, so the libva
variables must be set by a wrapper. Create it under the approved scratch bin
directory; it is not a load-owned file.

```sh
test -d "$BIN" || mkdir -p "$BIN"
cat > "$BIN/portico-server-gpu" <<'EOF'
#!/bin/sh
set -eu
export LIBVA_DRIVER_NAME=iHD
export LIBVA_DRIVERS_PATH=/home/justin/astra-scratch/P2-65/gpu/nonfree/extracted/usr/lib/x86_64-linux-gnu/dri
export PATH=/home/justin/astra-scratch/P2-65/bin:$PATH
export PORTICO_FFMPEG=/home/justin/astra-scratch/P2-65/bin/ffmpeg
export PORTICO_FFPROBE=/home/justin/astra-scratch/P2-65/bin/ffprobe
export PORTICO_DECODER_LIBRARIES=$LIBVA_DRIVERS_PATH/iHD_drv_video.so:$LIBVA_DRIVERS_PATH/libigdgmm.so.12
export PORTICO_LINEAR_DECODER_LIBRARIES=$PORTICO_DECODER_LIBRARIES
export TMPDIR=/home/justin/astra-scratch/P2-65/gpu-rehearsal/tmp
mkdir -p "$TMPDIR"
exec /home/justin/astra-scratch/P2-65/bin/portico-server "$@"
EOF
chmod 700 "$BIN/portico-server-gpu"
test -x "$BIN/portico-server-gpu"
sh -n "$BIN/portico-server-gpu"
```

Do not execute the wrapper here: it starts the server. The hash is recorded
after the private evidence directory exists. If the binary is not the current
P2-65 build, stop.

## 3. Private directories and capability document

```sh
umask 077
mkdir -p "$ROOT" "$PRIVATE" "$EVIDENCE" "$MEDIA/source" "$MEDIA/library"
chmod 700 "$ROOT" "$PRIVATE" "$EVIDENCE" "$MEDIA" "$MEDIA/source" "$MEDIA/library"
# APPDATA must remain absent; the factory refuses a non-empty appdata.
test ! -e "$APPDATA"
sha256sum "$BIN/portico-server" > "$EVIDENCE/server-binary.sha256"
```

Use the already captured capability document if it validates; otherwise create
a declared current web document from the snapshot. The hardware fixture rejects
an invalid document either way. The first `scp` is run **from mini-1** after the
mini-2 private directory exists; all other commands are on mini-2.

```sh
# On mini-1:
scp -p /home/justin/projects/Portico/.private/P2-65/rehearsal/capabilities.json \
  justin@100.113.197.98:/home/justin/astra-scratch/P2-65/gpu-rehearsal/private/capabilities.json
# On mini-2:
chmod 600 "$PRIVATE/capabilities.json"
cd "$REPO"
CAP="$PRIVATE/capabilities.json" "$NODE" --experimental-transform-types --input-type=module -e \
  "import {capabilityDocumentProblem} from './packages/client-core/src/client-profile.ts'; import {readFileSync} from 'node:fs'; const d=JSON.parse(readFileSync(process.env.CAP)); const p=capabilityDocumentProblem(d); if (p) { console.error('capability_problem=' + p); process.exit(1); } console.log('capability ok');"
```

Fallback only if that file is unavailable:

```sh
cd "$REPO"
CAP="$PRIVATE/capabilities.json" "$NODE" --experimental-transform-types --input-type=module -e \
  "import {webDeclaredProfile} from './packages/client-core/src/client-profile.ts'; import {writeFileSync} from 'node:fs'; writeFileSync(process.env.CAP, JSON.stringify(webDeclaredProfile()), {mode:0o600});"
chmod 600 "$PRIVATE/capabilities.json"
```

## 4. 150-second 1080p hardware colour source

Encode one cheap H.264/AAC colour source with audio. This is source material
only; the app's conversion is the evidence.

```sh
SOURCE=$MEDIA/source/rehearsal-1080p-150s.mp4
"$FFMPEG" -hide_banner -y \
  -f lavfi -i "color=c=0x101820:s=1920x1080:r=30:d=150" \
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=150" \
  -map 0:v:0 -map 1:a:0 \
  -c:v libx264 -preset veryfast -pix_fmt yuv420p \
  -g 60 -keyint_min 60 -sc_threshold 0 \
  -b:v 1200k -maxrate 1500k -bufsize 3000k \
  -c:a aac -b:a 128k -ac 2 -shortest -movflags +faststart "$SOURCE"
"$FFPROBE" -v error -show_entries format=duration:stream=codec_name,width,height \
  -of json "$SOURCE"
```

Require duration 150.0, a 1920x1080 h264 video stream and an audio stream. A
3-second GPU probe clip is not sufficient.

## 5. Real media scan fixture and fresh appdata

`provision-scale.py media` hard-links the source into one movie folder and
creates a real EPUB. `bootstrap` runs the ordinary setup, creates the libraries,
waits for the real scan, captures the prep receipt and stops the server.

```sh
cd "$REPO"
PATH="$BIN:$PATH" PORTICO_FFMPEG="$FFMPEG" \
python3 "$REPO/scripts/batch-round/provision-scale.py" media \
  --root "$MEDIA/library" --source "$SOURCE" --count 1 --window-seconds 60 \
  --out "$PRIVATE/media-facts.json" --execute \
  > "$EVIDENCE/media.out" 2>&1
jq '{root,windowSeconds,files:(.files|length),
     first:(.files[0]|{path,durationSeconds,width,height,videoCodec,audioCodec})}' \
  "$PRIVATE/media-facts.json"

PATH="$BIN:$PATH" PORTICO_FFMPEG="$FFMPEG" \
python3 "$REPO/scripts/batch-round/provision-scale.py" bootstrap \
  --server-binary "$BIN/portico-server-gpu" \
  --appdata "$APPDATA" \
  --media-facts "$PRIVATE/media-facts.json" \
  --capabilities "$PRIVATE/capabilities.json" \
  --port 32665 --window-seconds 60 --scan-timeout 300 --execute \
  > "$EVIDENCE/bootstrap.out" 2>&1
jq '{receipt,serverId,scan,playable,storage,blocker}' "$APPDATA/prep-receipt.json"
```

Require `blocker: null`, a terminal movie scan, one playable item and
`storage.databaseUnderAppdata: true`. The bootstrap leaves the server stopped.

## 6. Start the fresh server for identities and rehearsal

```sh
(umask 077; cd "$APPDATA"; \
  HOME="$APPDATA" PORTICO_DATA_DIR="$APPDATA" \
  PORTICO_BIND=127.0.0.1:32665 \
  PORTICO_SETUP_HOSTS="localhost:32665,127.0.0.1:32665" \
  GOMAXPROCS=2 nohup "$BIN/portico-server-gpu" > "$EVIDENCE/server.log" 2>&1 & \
  echo $! > "$EVIDENCE/server.pid")
for i in $(seq 1 120); do
  curl -fsS http://127.0.0.1:32665/v1/system >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS http://127.0.0.1:32665/v1/system | jq '{id,name,version}'
```

Read-only preflight before creating identities:

```sh
TOKEN=$(jq -r '.accessToken' "$APPDATA/owner-identity.json")
curl -fsS -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:32665/v1/admin/playback/diagnostics |
  jq '{backend:.diagnostics.hardware.effective,
       probes:.diagnostics.hardware.probes,
       limit:.diagnostics.conversionSessionLimit,
       active:.diagnostics.activeConversionSessions,
       fallbacks:.diagnostics.hardwareFallbacks,
       hls:.diagnostics.hlsConfigured,
       finiteHls:.diagnostics.finiteHlsConfigured}'
curl -fsS -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:32665/v1/admin/settings/configuration |
  jq '.data.effective.conversionMode // .effective.conversionMode // .requested.conversionMode'
```

Require `backend: "vaapi"`, a supported VAAPI probe, `conversionMode: "all"`,
`active: 0`, and a null limit or a limit >= 2. If the owner UI is needed to
raise `maxHardwareSessions` to 2 or set conversion to All, stop and do that
through the normal UI, then repeat this readback.

## 7. Real 2-member identities and the two manifests

```sh
cd "$REPO"
PATH="$BIN:$PATH" \
python3 "$REPO/scripts/batch-round/provision-scale.py" identities \
  --target http://127.0.0.1:32665 \
  --owner-credentials "$APPDATA/owner-credentials.json" \
  --capabilities "$PRIVATE/capabilities.json" \
  --members 2 --pacing-seconds 1 --username-prefix p265gpu \
  --custody "$PRIVATE/member-custody.json" \
  --out "$PRIVATE/identities.json" --execute \
  > "$EVIDENCE/identities.out" 2>&1
jq '{serverId,cohort,totalIdentities,createdMembers,libraries}' "$PRIVATE/identities.json"

PATH="$BIN:$PATH" \
python3 "$REPO/scripts/batch-round/provision-scale.py" manifest \
  --identities "$PRIVATE/identities.json" \
  --prep-receipt "$APPDATA/prep-receipt.json" \
  --workload "$APPDATA/workload-template.json" \
  --out "$PRIVATE/load-manifest.json" --execute \
  > "$EVIDENCE/manifest.out" 2>&1

# Derive the hardware fixture's narrower current shape: owner + two members.
umask 077
jq '{version:1,target:.target,serverId:.serverId,owner:.owner,users:(.users[0:2])}' \
  "$PRIVATE/load-manifest.json" > "$PRIVATE/hardware-manifest.json"
chmod 600 "$PRIVATE/hardware-manifest.json"
jq '{target,serverId,ownerRole:.owner.role,userRoles:[.users[].role],
     itemIds:[.users[].itemIds],users:(.users|length)}' "$PRIVATE/hardware-manifest.json"
```

The hardware manifest must have exactly 2 member users with non-empty
`itemIds`, valid capabilities, fresh tokens and a loopback target. The extra
load fields (`libraryId`, `reading`, `refreshToken`, `serverIdentity`) are
ignored by the hardware fixture.

## 8. The two-session hardware rehearsal

```sh
mkdir -p "$PRIVATE/mutation-store"
chmod 700 "$PRIVATE/mutation-store"
test -z "$(ls -A "$PRIVATE/mutation-store")"   # fresh P251 custody only
cd "$REPO"
"$NODE" --experimental-transform-types scripts/batch-round/hardware/hardware-execution.ts \
  --execute --rehearsal --target http://127.0.0.1:32665 \
  --sessions 2 --direct-users 0 --quality 720p-high \
  --bytes 10000000 --seconds 90 --render-node /dev/dri/renderD128 \
  --manifest "$PRIVATE/hardware-manifest.json" \
  --mutation-store "$PRIVATE/mutation-store" \
  --out "$EVIDENCE/hardware-rehearsal.json"
```

Exit code 0 and `verdict: "pass"` are required. Inspect, do not rewrite, the
receipt:

```sh
jq '{verdict,failures,overlappingConversions,observed,
     sessions:[.sessions[]|{sessionId,hls,stopped,lastLaunch:.execution.lastLaunch}]}' \
  "$EVIDENCE/hardware-rehearsal.json"
```

Both sessions must show a hardware encoder/decoder, `/dev/dri/renderD128`,
`hls.bytes >= 1048576`, at least two segments, `stopped: true`, and empty
`failures`. `observed.overlapSamples >= 2`, `observed.drainOverlap: true`,
`observed.maxActiveConversions >= 2`.

## 9. Stop, evidence and cleanup

```sh
PID=$(cat "$EVIDENCE/server.pid")
kill -TERM "$PID" 2>/dev/null || true
for i in $(seq 1 30); do kill -0 "$PID" 2>/dev/null || break; sleep 1; done
kill -KILL "$PID" 2>/dev/null || true
ss -ltn | grep ':32665' || true    # must be empty
```

Keep `$APPDATA`, `$PRIVATE`, `$EVIDENCE` for lead review. Do not delete P251
mutation-store records or the identity custody file. Remove only disposable
source/media after the lead has reviewed the receipt. No Docker, no host
services, no Git writes.

## Failure handling

- `hardware_backend_unavailable` / `hardware_backend_unresolved`: the wrapper
  did not expose iHD or the server did not select VAAPI. Stop; do not use
  software.
- `conversion_capacity_unavailable` / a limit below 2: raise the server's
  hardware session limit through the owner UI, re-read, retry only after lead
  coordination.
- `encoder_not_hardware`, `decoder_not_hardware`, `unknown_launch_observed`,
  `hardware_fallback_observed`, `render_node_missing`: preserve the receipt and
  server log; report. Do not relax the encoder/decoder requirement.
- `overlap_evidence_insufficient`: HLS finished before two during-drain samples
  or fewer than two conversions were active. Increase `--bytes`/`--seconds` or
  inspect `scale_vaapi`; do not call a post-drain page acceptance.
- App uses CPU `swscale` because `scale_vaapi` VPP fails: acceptable, provided
  the receipt's encoder and decoder are hardware and the encode stage is
  hardware.
- Any child/server exit failure: stop the server and report the receipt, log
  and exact code. Do not rerun blindly.

## Explicit non-claims

This is a two-session execution/overlap rehearsal on one GPU host. It is not a
capacity, thermal, 80/100-user, 5M/300k, browser-device, decoder-quality or
campaign-acceptance result. No later live run is implied by this retained recipe.

Driver correction: use the scratch-extracted full-feature Ubuntu intel-media-va-driver-non-free26.1.2+ds1-1 package (DEB SHA256 caae4c71ff72f17b625319a195857a174e66ec3bcf2d97c2d98a449fbc9e6264). The free variant exposes CQP only and fails the real bitrate-control probe. Patched private ICD RUNPATH=$ORIGIN SHA256 a68377b120b46d9f5a9c1c7cd35b4295848231b5315f871bd8b7d1707be6ace9. No package was installed system-wide. Set the actual ELF FFmpeg path through PATCH /v1/admin/console/settings, not a shell FFmpeg wrapper; shell wrappers cannot establish runtime identity. Server wrapper is fine. Existing optional decoder-library settings grant the private ICD/GMM files without changing confinement.
