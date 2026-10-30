# Portico for Fire TV (Vega OS)

The Portico television app for Amazon's Vega OS, built with the Vega SDK's React Native. The
screens are the shared native app's (`../native/src-next`), imported in place and never copied; this folder
holds only what Vega needs to run them.

| Path | Role |
| --- | --- |
| `index.js`, `src/Root.tsx` | Entry: the shared `App`, drawn on the television design canvas. |
| `src/shims/react-native.ts` | `react-native` as the app and the shared screens see it: the SDK's React Native, with the few exports whose names or behavior differ from the Apple TV build. |
| `src/shims/canvas.tsx` | The 1920×1080 design canvas (Vega lays a 1080p screen out as 960×540 at a pixel ratio of 2). |
| `src/shims/pressable.tsx` | `hasTVPreferredFocus` as a preference read once, as on Apple TV. |
| `src/shims/focus.tsx` | `TVFocusGuideView` with the Apple TV's meaning of `focusable={false}` and `autoFocus`. |
| `src/shims/snap.tsx` | Rows and pages that follow focus (the tvOS `snapToAlignment="item"` and `scrollSnap…` props). |
| `src/shims/text-input.tsx` | A field's hidden input, mounted only while the system keyboard is up. |
| `src/shims/motion.ts` | Whether rows and pages glide to follow focus (`portico.animations` in `app.json`). |
| `src/shims/menu-key.tsx` | The remote's Menu key (≡): it opens the focused control's menu, as on a Fire TV that runs Android. |
| `src/debug.tsx`, `scripts/debug-sink.mjs` | An on-screen console for development (`portico.debugConsole` in `app.json`), and a listener for its lines. |
| `src/bridge/` | Every `NativeModules.Portico…` module the shared `src-next/platform` expects, with the Apple names, methods and events, written over the SDK's APIs. |
| `src/overrides/` | Replacements for shared files that cannot be right on Vega: `native-next/<path under ../native/src-next>` (and `packages/<path under ../../packages>`, none today). |
| `test/` | Tests of the bridge that need no device: the stream reader, the player engine, subtitles, event streams and the capability document, against stand-ins for the media element and a server (`test/stubs`). |
| `metro.config.js` | Resolves `react-native` to the shim, community package names to the SDK's builds, and shared files to their overrides. |
| `scripts/vega.mjs` | Build, install, launch, remote keys, screenshots and logs. |

## Current shared shell

`../native/App.tsx` mounts `src-next/App`, including the TV entry, actual restore lifetime and
shared playback coordinator. `src/overrides/native-next/platform` supplies Vega installation
metadata, capability publishing input, listening support and physical Menu handling. The
installation host keeps the shared generation, persisted-installation and saved-session checks;
it classifies the device as `vega`. The capability adapter uses `PorticoCapabilities.facts()`'s
actual bounded Vega probe, one W3C engine and no native audio decoder. Late probe answers and
app resume republish the output facts. It never selects an Apple capability profile.

Physical Menu is heard once from the SDK and sent to the new shell's actual focused-menu
registration or top overlay. Back retains its separate navigation meaning. There is no separate
native listening owner, playback-speed control, gapless, crossfade or volume-levelling engine.
The `native` overrides below remain for legacy sources that the bridges still import.

## Requirements

- Vega SDK 0.24 (`vega` on the PATH), which provides React Native 0.83 for Vega
  (`@amazon-devices/react-native-kepler` 4) and the virtual device (Vega OS 1.2).
- Node 22.

## Running

```bash
npm install --legacy-peer-deps
node scripts/vega.mjs device start     # the SDK's virtual device, headless
node scripts/vega.mjs run --release    # build, install and launch
node scripts/vega.mjs shot shot.png    # a screenshot of the virtual device
node scripts/vega.mjs key right select # remote keys
node scripts/vega.mjs logs             # the app's recent log lines
```

`node scripts/vega.mjs help` lists every command. A Release build embeds the bundle and needs no
Metro; `node scripts/vega.mjs start` runs Metro for a Debug build.

Checks: `npx tsc --noEmit` (this app and the shared sources together), `npx tsc -p test/tsconfig.json --noEmit`,
`npm run lint:strings`, and `npm test` (about ten seconds; no device and no SDK tools needed).

## Things the SDK's tools need to be told

`scripts/vega.mjs` handles all three; they matter when running the SDK's commands by hand.

- **Spaces in the checkout's path.** The build tools pass paths to a shell unquoted. From a
  checkout whose path contains a space, the script mirrors the app, the shared sources and the
  packages to `~/.cache/portico-vega-stage` (or `PORTICO_VEGA_STAGE`) and builds there.
- **An Intel `bash` first on the PATH** (Apple-silicon Macs with an old Homebrew). The SDK's
  wrappers start with `env bash`, every tool then runs translated, and packaging fails with
  "macOS x86_64 binary not available". The script puts `/bin` first.
- **Android emulators on the same machine.** The virtual device is a fork of the Android emulator
  and `vda` a fork of `adb`, with the same default ports, so each tool sees (and may stop) the
  other's devices. The script starts the virtual device on port 5701 (`PORTICO_VEGA_DEVICE_PORT`)
  and talks to it through its own device-tool server on port 5039 (`PORTICO_VEGA_ADB_PORT`).
- **mini-1 display/capture.** SDK 0.24's `--gui-off` mode produced all-black screenshots of
  both stock Home and Portico on this host. The stock Home screen renders and the script's
  screenshot works with `PORTICO_VEGA_GUI=1 DISPLAY=:<isolated-display>` on an owned Xvfb
  process (`Xvfb -displayfd 3 -screen 0 1920x1080x24 -nolisten tcp -noreset`). Keep that display
  alive for the whole Vega window; stop the exact Vega instance before stopping Xvfb. This
  setup preserves the saved instance and isolated ports; it does not establish app parity.
- **Headless graphics.** `PORTICO_VEGA_GL_ACCEL=0 node scripts/vega.mjs device start` selects the
  SDK's supported `--gl-accel-off` backend. This is an opt-in diagnostic/setup option; it does
  not reset the saved device or change Android's bridge server.

## How the shared screens run unchanged

- `react-native` resolves to `src/shims/react-native.ts` for the app, the shared sources and the
  shared packages. Libraries in `node_modules` keep the SDK's package.
- `NativeModules` there serves the modules in `src/bridge`; `NativeEventEmitter` listens to their
  events; `requireNativeComponent` returns their views.
- `useTVEventHandler` reports the remote's keys in the Apple TV's vocabulary (`playPause`,
  `menu` for Back, `long…` for a held key).
- Focus, scrolling and text entry follow the Apple TV's rules where the SDK's differ: see the
  comments at the top of `focus.tsx`, `snap.tsx`, `pressable.tsx` and `text-input.tsx`.
- The new `src-next` shell uses the SDK 0.24 gesture, Reanimated and Worklets packages, including
  layout animations, scroll handlers and reactions. Babel uses the SDK Worklets plugin.
  React Native's own `Animated` is the SDK's.
- The SDK MaskedView renders real alpha masks; the adapter removes Android's unsupported
  `androidRenderingMode`. The existing TV glass policy selects translucent fills. An unexpected
  native backdrop-blur mount fails explicitly; this app does not provide that native module.
- A shared file that cannot be right on Vega has a replacement under `src/overrides` at the same
  relative path (a replacement may import the file it replaces, to wrap it). They are kept few and
  small: each is a statement of what this device is or can do, not a copy of a screen.

## Settings in `app.json`

Under `portico`:

- `hostedUrl`: the Portico Account origin; empty for the shared default.
- `animations`: `false` makes every transition arrive at once (for hardware that cannot keep up).
- `debugConsole`: `false` for anything that ships. `true` draws the app's console output and every
  call into the Portico modules over the screen; `"errors"` draws only what went wrong. (An app's
  own console output does not reach the device log in a Release build.) Values that could be
  credentials are shown by their length only.
- `debugSink`: with the console on, an address the same lines are also posted to, for example
  `http://10.0.2.2:5799` with `node scripts/debug-sink.mjs` running on this machine. The player
  is the reason: once video is drawn, the virtual device's screenshots stop updating until the app
  is restarted, so the player is checked by its trace.

## What this device is, and what it does not do

The shared screens never ask the operating system what they run on. They ask three small files,
and Vega answers each in its own override, so nothing else in the app checks for Vega:

- **`bridge/device-platform.ts`**: a television called "Fire TV" (in Settings, in sign-in
  approvals, in Link a TV), announced to servers and the Portico Account as platform `vega`, with
  no AirPlay, no Picture in Picture and nothing to send playback on to.
- **`bridge/native-listening-kinds.ts`**: no separate listening owner (music and audiobooks play
  through the one player), one playback speed, and no gapless, crossfade or volume levelling. So
  Settings leaves those rows out, the player has no Speed box, and a profile's default speed set
  on another device is not applied here.
- **`PorticoPlaybackModule.features`** (`src/bridge/playback.ts`) and the **capability document**
  (below): what the player itself does. The player screen hides Picture in Picture, and the
  Player setting (mpv) is not offered because this build has no second engine.

## Playback

`src/bridge/playback.ts` is `PorticoPlaybackModule` and the `PorticoPlayer` view: the app's one
player engine, written to the contract every platform's engines keep
(`../native/Native/ENGINE-CONTRACT.md`: a lease, full-state commands, sequenced facts) over the
SDK's W3C media element. The shared playback path drives it exactly as it drives AVPlayer or
Media3: the shared playback service, the shared session controller, and a change of quality,
version, audio or subtitles as a change to the playing session. Three things about the element
shape the rest:

- It opens an address itself only over HTTPS, and has no HLS client. The server's HLS is played by
  `src/bridge/hls.ts` through media source extensions (the app fetches the segments), which is
  also the only way to play from a server on plain HTTP.
- It cannot change speed. `supportedRates` is `[1]`.
- The system acts on it directly for the remote's transport keys unless the app declares the
  media category in `manifest.toml` and supplies a handler: `src/bridge/system-media.ts` turns
  those requests into `PorticoSystemMediaCommand` events for the shared player.

What the engine does beyond playing:

- **Automatic quality.** Every byte of a stream passes through `hls.ts`, so that is where the
  connection is measured: `progress` facts carry `throughputKbps` (only while fresh) and
  `bufferedSeconds`. A file the element opens from an HTTPS address is fetched out of sight: only
  its buffer is reported, and Automatic then steps down on stalls alone.
- **Live channels.** A channel's playlist slides and restarts its clock at a change of programme.
  Segments are followed by number, placed one after another (the source buffer's `sequence` mode,
  or carried across the gap where the device keeps the segments' own times), and a position is
  the moment of broadcast the playlist gives, counted from the channel's origin.
- **Subtitles.** The server's cue document (`src/bridge/subtitles.ts`, `subtitle-cues.ts`), from
  the session's own address only, checked again every 25 seconds, drawn by `PorticoPlayer` in the
  profile's caption size and background, with the track's timing offset. Styled and image
  subtitles are burned in by the server (the capability document says the engine draws text only).
- **A route in doubt.** While the connection to the server is being checked the media is closed
  and the place kept; it opens again there.
- **Leaving the app.** A video is paused while the app is not on screen, and the shared player
  is told, so it shows Play when the viewer comes back. Music and audiobooks play on.
- **Why a title stopped.** Error facts say whether the server refused (with its status), the
  connection gave no answer, or the element would not take the media, so the shared player can
  tell "try again" from "this can't be played" and ask the server for another way to deliver it.

Not done, because the SDK gives no way to: asking the television to match a title's frame rate or
dynamic range before it starts, and following the caption style set in the system's
Accessibility settings.

### The capability document

`src/bridge/capability-profile.ts` builds the one document the server plans from; the override of
`bridge/capability-publisher.ts` keeps it published. One engine (`engines.native`), no mpv.

- The floor is client-core's declared table for Vega. On top of it, `capability-probe.ts` asks
  the device itself (`MediaSource.isTypeSupported` and the SDK's `decodingInfo`) which codecs its
  decoders take and at what size, and the answers replace the table's codecs and transports.
  (The SDK never fills in `smooth` for a picture, so a supported size is taken to play smoothly.)
- Text subtitles only; no switching between the audio tracks inside an original file.
- No HDR and no surround output are claimed: the SDK does not say what the television shows or
  what the sound goes to. The server tone-maps and mixes down. "Turn off HDR" and "Don't pass
  sound through" are therefore not offered in Settings. Dolby audio stays listed as sound to
  hand on to a receiver, never as sound the device decodes, whatever the device answers: its
  "yes" does not tell the two apart, and a television that cannot decode it would be silent.
- Files are played from their address only when the server is reached over HTTPS.

Music and audiobooks use the same player. On Apple a native owner takes them over so that they
continue while JavaScript sleeps; here the shared queue player keeps them
(`src/overrides/native-next/platform/playback/native-listening-kinds.ts`), so there are no gapless or crossfade
edges: the next track starts when its stream opens.

## Not provided

- **Rows on the Fire TV home screen.** The SDK's route is content personalization
  (`@amazon-devices/kepler-content-personalization`), which reports what a customer watches to
  Amazon by catalog identifier. A personal library has no such identifiers, and Portico does not
  send viewing activity to third parties. `PorticoTopShelf` keeps the shared code's record and
  draws nothing.
- **Nearby discovery** (no mDNS API): a server is found by code or by its address, and a phone
  finds this television for "Play on" through the server, not on the local network.
- **AirPlay, picture in picture, downloads, casting, a second player engine (mpv), playback
  speed**: the modules exist with the Apple names and answer "nothing" or "not available", and
  the shared screens do not offer them here.

## Storage

Vega has no keychain. Records (credentials, the TV sign-in key, journals, the profile trust
vault) are files in the app's private `/data` directory, which no other app can read and which is
removed with the app. Writes are crash-safe (two slots per record, verified by reading back).
