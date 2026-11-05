# Client API detection additions (F4/F5)

Detection-only changes over the initial ownership census source `0ecb62138e9091894448b3514cd77b1a6e90b466`. No product code added. [Lead ruling](https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646280885) approves exactly these **29 existing occurrences (533 → 562)** as initially direct sites. The manifest retains all 29 and verifies each against its immutable source and current PR base. [CC-4's separate ruling](https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646427108) approves its six forwarding occurrences; [CC-1's separate ruling](https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6647211850) approves the two complete metadata-upload guards. An exception activates only when the current source matches its exact approved span and multiplicity. CC-4 upload/bounded pins bind the full SettingsEntry policy, never just a nested callback.

Rebuilt on `2537ed4a6db62cfda1a269061df019636efcd36d`, preserving all intervening slice reductions: **460 → 417 direct / 223 exempt**. Of the original reviewed detection additions, 17 occurrences across 15 IDs enter this current direct baseline; the remaining original sites have moved into core or match separate exact transport rulings. F5 adds Java URL readers, Swift async URLSession and fluent/borrowed Rust HTTP coverage with positive/negative fixtures; no additional current F5 sites found. F4 includes resolved HttpLocalApi methods and V1Request send ports, including layered app implementations. Nested executors remain separate sites, preserving the runtime.ts subtitle-upload detection.

Current newly detected direct IDs (all included in the exact original-source tables below):

- `apps/native/src-next/platform/playback/runtime.ts#0b0d0f25f152f5e8` ×1
- `apps/native/src-next/platform/playback/runtime.ts#6eaf5d1ad651ca99` ×2
- `apps/native/src-next/platform/playback/runtime.ts#9a5956e7ca7d1e8b` ×1
- `apps/native/src-next/platform/playback/runtime.ts#b93c2b0e400ec9e1` ×1
- `apps/native/src-next/platform/playback/runtime.ts#f0a2d245dacf0916` ×2
- `apps/native/src-next/platform/playback/scope-http.ts#21d1e987785bc408` ×1
- `web/src/platform/playback/runtime.ts#53df8861a53fc1ec` ×1
- `web/src/platform/playback/runtime.ts#692bd4c5a755f009` ×1
- `web/src/platform/playback/runtime.ts#6b285d4646a96382` ×1
- `web/src/platform/playback/runtime.ts#78dc8210c398edf9` ×1
- `web/src/platform/playback/runtime.ts#d08cac842cd1705e` ×1
- `web/src/platform/playback/runtime.ts#ea423be48cb3f904` ×1
- `web/src/platform/playback/start-owner.ts#6c05124bbe8c6d46` ×1
- `web/src/screens/settings/Settings.route.tsx#4a0ed0f9855c7d2e` ×1
- `web/src/screens/settings/Settings.route.tsx#7393699cf50b5a29` ×1

## CC-1: 2 occurrences

| Exact site | Request | ID |
|---|---|---|
| `apps/native/src-next/screens/read/ReadOwnerEditSheet.tsx:85` | `context.api.requestMetadataUpload<T>(path, body, scoped.signal)` | `apps/native/src-next/screens/read/ReadOwnerEditSheet.tsx#dcf70cf97a348b63` |
| `web/src/screens/read/ReadMetadata.tsx:29` | `context.api.requestMetadataUpload<T>(path, body, request)` | `web/src/screens/read/ReadMetadata.tsx#e85721b4e94e1386` |

## CC-3: 21 occurrences

| Exact site | Request | ID |
|---|---|---|
| `apps/native/src-next/platform/playback/capacity-retry.ts:38` | `input.http.send(request)` | `apps/native/src-next/platform/playback/capacity-retry.ts#f33d21145457958f` |
| `apps/native/src-next/platform/playback/capacity-retry.ts:40` | `input.http.send(request)` | `apps/native/src-next/platform/playback/capacity-retry.ts#f33d21145457958f` |
| `apps/native/src-next/platform/playback/capacity-retry.ts:63` | `input.http.send(captured)` | `apps/native/src-next/platform/playback/capacity-retry.ts#749ca04919a11a85` |
| `apps/native/src-next/platform/playback/runtime.ts:229` | `this.http.send(request)` | `apps/native/src-next/platform/playback/runtime.ts#6eaf5d1ad651ca99` |
| `apps/native/src-next/platform/playback/runtime.ts:534` | `this.http.send({...request, signal: scoped.signal})` | `apps/native/src-next/platform/playback/runtime.ts#f0a2d245dacf0916` |
| `apps/native/src-next/platform/playback/runtime.ts:665` | `this.http.send(request)` | `apps/native/src-next/platform/playback/runtime.ts#6eaf5d1ad651ca99` |
| `apps/native/src-next/platform/playback/runtime.ts:667` | `this.http.send(adopted ? request : {...request, signal: scoped.signal})` | `apps/native/src-next/platform/playback/runtime.ts#b93c2b0e400ec9e1` |
| `apps/native/src-next/platform/playback/runtime.ts:906` | `this.http.send({...request, signal: scoped.signal})` | `apps/native/src-next/platform/playback/runtime.ts#f0a2d245dacf0916` |
| `apps/native/src-next/platform/playback/runtime.ts:1313` | `this.http.send({path, method, body, signal}, {send: async request => ({ status: 200, headers: {}, body: await this.o.api.requestSubtitleUpload(request.path, method, body, request.signal), })})` | `apps/native/src-next/platform/playback/runtime.ts#9a5956e7ca7d1e8b` |
| `apps/native/src-next/platform/playback/runtime.ts:1314` | `this.o.api.requestSubtitleUpload(request.path, method, body, request.signal)` | `apps/native/src-next/platform/playback/runtime.ts#0b0d0f25f152f5e8` |
| `apps/native/src-next/platform/playback/scope-http.ts:33` | `transport.send({...request, signal: controller.signal})` | `apps/native/src-next/platform/playback/scope-http.ts#21d1e987785bc408` |
| `web/src/platform/playback/capacity-retry.ts:39` | `input.http.send(request)` | `web/src/platform/playback/capacity-retry.ts#f33d21145457958f` |
| `web/src/platform/playback/capacity-retry.ts:41` | `input.http.send(request)` | `web/src/platform/playback/capacity-retry.ts#f33d21145457958f` |
| `web/src/platform/playback/capacity-retry.ts:84` | `input.http.send(captured)` | `web/src/platform/playback/capacity-retry.ts#749ca04919a11a85` |
| `web/src/platform/playback/runtime.ts:233` | `inner.send(request.method === 'DELETE' ? request : {...request, signal: request.signal ? AbortSignal.any([request.signal, this.life.signal]) : this.life.signal})` | `web/src/platform/playback/runtime.ts#692bd4c5a755f009` |
| `web/src/platform/playback/runtime.ts:342` | `inner.send(request.method === 'DELETE' ? request : {...request, signal: request.signal ? AbortSignal.any([request.signal, life.signal]) : life.signal})` | `web/src/platform/playback/runtime.ts#6b285d4646a96382` |
| `web/src/platform/playback/runtime.ts:374` | `this.source.api.farewell(path, method, body)` | `web/src/platform/playback/runtime.ts#78dc8210c398edf9` |
| `web/src/platform/playback/runtime.ts:423` | `inner.send({...request, signal: life.signal})` | `web/src/platform/playback/runtime.ts#ea423be48cb3f904` |
| `web/src/platform/playback/runtime.ts:1011` | `stack.http.send({...value, signal: value.signal ? AbortSignal.any([value.signal, signal]) : signal})` | `web/src/platform/playback/runtime.ts#53df8861a53fc1ec` |
| `web/src/platform/playback/runtime.ts:1160` | `stack.http.send({...value, signal: value.signal ? AbortSignal.any([value.signal, request.signal]) : request.signal})` | `web/src/platform/playback/runtime.ts#d08cac842cd1705e` |
| `web/src/platform/playback/start-owner.ts:36` | `original.send({...request, signal})` | `web/src/platform/playback/start-owner.ts#6c05124bbe8c6d46` |

## CC-4: 6 occurrences (separately approved transport exceptions)

| Exact site | Request | ID |
|---|---|---|
| `apps/native/src-next/screens/settings-live/SettingsRoute.tsx:40` | `raw.send({...request, signal})` | `apps/native/src-next/screens/settings-live/SettingsRoute.tsx#32ee3e8586b61f52` |
| `apps/native/src-next/screens/settings-live/SettingsRoute.tsx:40` | `raw.send({...request, signal})` | `apps/native/src-next/screens/settings-live/SettingsRoute.tsx#32ee3e8586b61f52` |
| `apps/native/src-next/screens/settings-live/owner-dashboard/reader.ts:45` | `raw.send({...request, signal})` | `apps/native/src-next/screens/settings-live/owner-dashboard/reader.ts#32ee3e8586b61f52` |
| `web/src/screens/settings/Settings.route.tsx:208` | `originalApi.requestMetadataUpload<T>(path, body, signal(input))` | `web/src/screens/settings/Settings.route.tsx#7393699cf50b5a29` |
| `web/src/screens/settings/Settings.route.tsx:211` | `originalApi.requestBounded<T>(path, maxBytes, signal(input))` | `web/src/screens/settings/Settings.route.tsx#4a0ed0f9855c7d2e` |
| `web/src/screens/settings/Settings.route.tsx:214` | `originalApi.requestLyrics<T>(path, method, body, signal(input))` | `web/src/screens/settings/Settings.route.tsx#7c87dc11818a60a0` |
