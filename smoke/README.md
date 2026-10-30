# M0 smoke harness

The scripts in this folder run the core flows from `_program/v2/M0-HANDS-ON.md` against what
Justin will actually open. They file nothing themselves: a failed check is turned into a P0
queue item by the M0 smoke lane, with the surface, the build SHA and the exact step.

Owner: lane `ds-m0-smoke`, family `P0-SMOKE`.

## When to run

Run these flows at a Release checkpoint, after Release publishes the deployment and artifact
pins and grants the named device window. Between checkpoints, edit and review harness source
without starting runner jobs, tests, devices or captures. Native frame/build pairs belong to
Visual QA and Release's checkpoint batch; they do not block item merges or implemented credit.
A merged item is done. Checkpoint failures become FIX items in the owning family.

## Where each surface runs

| Surface | Runner | Why there |
|---|---|---|
| Web and mobile web | mini-2 (headless Chrome via Playwright) | Browsers never run on mini-1 |
| Hosted | mini-2 (API bootstrap plus Playwright) | Same reason; all Hosted data is disposable |
| Android phone and TV | mini-1, **one** emulator at a time | Android is the only local testing allowed on mini-1 |
| iOS and tvOS | the Mac over `ssh portico-mac-build`, one Xcode job | Apple simulators exist only there |

Nothing here starts a build. Release owns builds and deploys; this harness consumes their
artifacts and reports the SHA it saw. During the current custody hold, do not run Android or
Apple drivers, builds or installs without an explicit Release handoff and frozen artifact pin.
R3 setup is permanently Release-exclusive; this family must never initialize or drive it.

## Credentials

Never committed, never printed, never in a report. The scripts take a path:

- demo server: `/home/justin/projects/Portico/.private/demo-testing/active-credentials.json`
- on a runner, a private copy with mode 600 inside the lane's own project scratch, removed at wind-down.

`smoke/lib/creds.mjs` reads `username` and `password` and redacts the password from every
error string before it reaches a log.

## Web

```bash
SMOKE_ORIGIN=https://demo.getportico.tv \
SMOKE_CREDENTIALS=/path/to/private/creds.json \
SMOKE_OUT=/path/to/scratch/web-<checkpoint> \
SMOKE_CHECKPOINT=v2-M0-001 \
SMOKE_CHANNEL=chrome \
node smoke/web/run.mjs
```

Optional `SMOKE_ONLY=boot,signin,home,library,library-controls,detail,detail-show,
playback-direct,playback-controls,playback-subtitles,playback-transcode,album,search,
search-open,settings,settings-owner,read,live,signout,mobile`, `SMOKE_VIEWPORTS`,
`SMOKE_MOVIE_ID`, `SMOKE_ALBUM_ID`.

Checks come from the manual's core flows 1-9 plus phone width. Two rules the script enforces:

- **A 2xx is not playback.** `playbackEvidence()` requires a decoded frame (`videoWidth > 0`),
  `readyState >= 2`, an unpaused element and a `currentTime` that advances. The transcode and
  album checks follow the same rule.
- **A missing control is a failure, not a skip.** No captions control, no Play control or no
  filter control fails the check that needs it. Only a missing *fixture* (no Books library, no
  Live source) is reported as BLOCKED.

A crash that takes the renderer down is recovered between checks, so one blank screen does not
hide the rest of the run.

## Android

Only after Release explicitly hands off the named device and frozen artifact, one emulator, phone or TV:

```bash
python3 smoke/android/flows.py --serial emulator-5554 --apk /path/app.apk \
    --mode phone --out /path/scratch/android-phone --creds /path/private/creds.json
```

Install, cold launch, sign in, Home, Library, Detail and Play. The Android check observes
`dumpsys media_session` reporting `PLAYING`; that is session-state evidence and does not establish
decoded frames, audible output or physical-device acceptance. TV adds a 12-press D-pad
walk that must reach at least three distinct labelled controls.

## Apple

```bash
smoke/apple/flows.sh --surface ios --app /path/Portico.app --out /path/scratch/apple-ios
```

`simctl` installs, launches and screenshots but cannot tap. This proves install, cold launch, a
painted screen and no crash log; the interaction flows belong to `ds-apple-core`, who own Apple
core flows. It holds `/tmp/portico-apple-smoke.lock` so two Apple jobs never stack.

## Reading a run

`report.json` holds `verdict`, `counts` and one entry per check with `status`, `detail`,
`evidence` and the screenshot path. `verdict` is `fail` when any required check fails;
`pass-with-blockers` when the only problems are unavailable fixtures.

## Editorial rules baked into the checks

- Fixtures are read from the deployed server where possible; the two media fixtures are Media's
  measured ones (12 Angry Men, `jJxPchu5fFfLRAJWCOyfvQ`, and Abbey Road 2019 mix,
  `MxluJLkSmbFU2RFs2sO_9Q`).
- No check accepts a raw catalogue key on screen, an invented title or a stub state as success.
- Screenshots are scratch, never committed.
