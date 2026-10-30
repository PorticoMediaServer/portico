# Real CAF browser qualification

`real-caf-runtime.cjs` runs the checked receiver source through Google's actual
CAF and CAF-loaded Shaka. It creates small synthetic H.264/AAC HLS, MP3 and FLAC
fixtures and runs one Chrome instance. Only the native IPC boundary and server
API authority responses are simulated. It does not emulate media, SDK methods,
clock progression, playback events or success.

Use an existing Playwright installation through `PORTICO_PLAYWRIGHT_MODULE` and
an existing `/usr/bin/google-chrome`. Pass an explicit source Git ref, an owned
scratch directory, output JSON path and the checked Caddy response receipt:

```sh
PORTICO_PLAYWRIGHT_MODULE=/path/to/existing/playwright \
TMPDIR=/path/to/Portico/.scratch/cast-vega-media-r29 \
GOTMPDIR=/path/to/Portico/.scratch/cast-vega-media-r29 \
node --max-old-space-size=512 server/receiver/test/real-caf-runtime.cjs \
  --source-ref origin/main \
  --policy-receipt /path/to/csp-header-isolation.json \
  --scratch /home/justin/projects/Portico/.scratch/cast-vega-media-r29/runtime \
  --output /path/to/receipt.json
```

The page is fulfilled at a controlled HTTPS origin with the exact Git source
bytes and response-derived document CSP. Google scripts use real HTTPS requests;
there is no local CA, certificate bypass, SDK interception, URL normalization,
vendoring or alternate Shaka loader. The source must be on MAIN or an ancestor of
the lane's own HEAD; the receipt distinguishes these cases.

The checks require actual decoded video frames, matching HTML/CAF clocks,
pause stability, seek/resume, Stop, then natural MP3 completion through the
ordinary server `:advance` response into real FLAC playback. Missing/interrupt
completion cannot count as natural advance. Grant-bearing media URLs must stay
out of CAF/custom status. When the selected MAIN source includes the composed
view, read-only DOM observations also require its real VIDEO element without
controls, catalogue year, pause pill/overlay, Stop idle, distinct next-song
artist/album, loaded fonts and no CSP violation. A MutationObserver records the
view transitions; it never calls presentation methods or drives clocks/events. Page errors are failures. Browser shutdown is in
`finally`; remove the owned scratch directory after the qualification round.

`--diagnostic-empty-style` appends a candidate empty-style hash exception to the
checked input policy. This is **diagnostic only** and cannot establish product
CSP acceptance. The receipt records that flag and retains all CSP violations.
The default never changes the checked response policy. Neither mode establishes
physical Chromecast, device capabilities, speaker output, production server
behavior or pixel parity with the receiver frames.
