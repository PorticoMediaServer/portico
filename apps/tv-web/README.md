# Portico for LG and Samsung televisions (TV web)

The Portico television app for LG sets (webOS) and Samsung sets (Tizen). Both run an app as a web
page in the set's own browser engine, so this is one web app for both. Its screens are the shared
TV app's (`../native/src-next`), drawn in the browser by react-native-web, imported in place and
never copied: the same layout, focus and Back behaviour as on Apple TV, Android TV and Fire TV.
This folder holds only what a TV browser needs to run them.

| Path | Role |
| --- | --- |
| `src/index.tsx`, `src/Root.tsx` | Entry: the shared `App` on the 1920×1080 design canvas, with the kit's lite effects tier on. |
| `src/shims/react-native.ts` | `react-native` as the shared screens see it: react-native-web, plus the Apple TV names and behaviour they use (`Platform.isTV`, focus guides, the remote, Portico's modules). |
| `src/shims/focus.tsx`, `focus-geometry.ts` | The focus engine a browser lacks: arrows move focus to the nearest control that way, within the focus guides the kit declares (`trapFocus…`, `autoFocus`, `setDestinations`, `nextFocus…`, `hasTVPreferredFocus`, `requestTVFocus`). |
| `src/shims/keys.ts` | The remote: arrows, OK (held: the control's long press), Back (`hardwareBackPress`, then the app closes) and the media keys, reported to the shared screens as the Fire TV app reports them. |
| `src/shims/components.tsx` | `View`, `Pressable` and `TextInput` with the TV properties react-native-web drops. A text field is read-only until OK, so passing over it never opens the set's keyboard. |
| `src/shims/canvas.tsx`, `masked-view.tsx`, `blur.tsx`, `screens.tsx`, `assets-registry.ts` | The canvas, CSS alpha masks, and the native libraries a TV has no use for. |
| `src/sdk/` | The Amazon SDK calls the Fire TV bridge (`../vega/src/bridge`) makes, on the browser's own APIs: the media element and media source, storage, randomness, the network's online state, the device's name. The bridge's files are used unchanged. |
| `src/overrides/` | Replacements for shared files that cannot be right in a TV browser, at the same relative path: `native-next/…` (under `../native/src-next`) and `vega/…` (under `../vega/src`). Each states what this device is or does; none is a copy of a screen. |
| `src/platform/` | Which set this is (`webos` or `tizen`), its keys and how the app closes. |
| `src/capabilities/` | The capability document the server plans playback from. |
| `test/` | `focus-geometry.test.ts` (node); `cdp.mjs`, a small Chrome DevTools driver; and `browser/`, the checks in real browsers that `.github/workflows/tv-web.yml` runs: the focus map's Home and rail tests (`focus-map.mjs`) in Chromium 85 and 87, the time a focus move takes with the CPU slowed, and the test film playing, against a fresh server-next with a seeded library (`server.mjs`, `front.mjs`). |

## Running in a desktop browser

```bash
npm ci
npm run build
PORTICO_TV_PROXY=https://a.portico.server npm run preview   # serves dist/ and that server's API at 127.0.0.1:5181
```

Open `http://127.0.0.1:5181/`, use the arrow keys, Enter (OK) and Escape (Back), and sign in to
`http://127.0.0.1:5181` as the server address. The proxy is for development only: a desktop page
is not a TV app, and a server lets a web page from another address read its answers only when its
owner lists that address. Add `?platform=tizen` to play the Samsung part.

## Browser checks

```bash
npm run build
PORTICO_SERVER_NEXT_BIN=… PORTICO_SEED_BIN=… TVWEB_BROWSERS=chrome=/usr/bin/google-chrome node test/browser/run.mjs focus
```

`TVWEB_ONLY=<regex>` runs only the focus tests whose ID matches. A test this server cannot set up is
reported "not run" with the reason, and one the app itself cannot reach (an app or server finding,
the same on every TV) "blocked"; neither is counted as passed. The page may be opened with
`?debugfocus` to give a debugging session the focus engine's maps and `__tvFocus.explain(direction)`.

## Effects

These sets are treated as low-powered. The kit's lite effects tier (`platform/motion-policy.ts`)
is always on here: translucent fills instead of live blur, no glows, soft shadows, drift or
arrival fades.

## Unconfirmed on real hardware

Nothing below has been seen on an LG or Samsung set. Each is what the platforms' documentation
and other apps' reports say, and is where a first test on a set should look.

- **Back key codes**: 461 on LG, 10009 on Samsung. Samsung's media and channel keys reach the app
  only after `tizen.tvinputdevice.registerKeyBatch` (`src/platform/device.ts`).
- **The on-screen keyboard** opens when an editable field takes focus, on both platforms; a field
  is made editable by OK and focused again to open it (`components.tsx`).
- **Closing the app**: `window.close()` on LG, `tizen.application…exit()` on Samsung.
- **Storage**: `localStorage` survives restarts and app updates and is removed with the app.
- **The window is 1920×1080** on every set, 4K ones included.
- **Playback**: HLS through Media Source Extensions with fragmented MP4, H.264 and HEVC, on the
  browser's video element. Whether HEVC Main 10 over MSE switches the set into HDR is not known;
  the document claims SDR (`src/capabilities/document.ts`).
- **Speed** of focus moves on 2022 sets (measured only in desktop Chromium with the CPU slowed).
