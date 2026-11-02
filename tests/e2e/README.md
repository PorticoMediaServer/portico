# Disposable E2E fixture

`server.py` starts a local-only devtrust server with a generated, cached media
library, fresh test identities, and a restricted profile. It scans the library
before running its child and destroys the server state afterwards. The media
cache is separate from the checkout and contains no credentials. The cache is
not a pre-scanned database snapshot: every run still scans the library.

Use `--live` to publish a short synthetic video in a looping HLS channel. The
fixture serves an M3U and XMLTV guide over loopback, registers their origin via
the owner-confirmed source flow, and stops the encoder and HTTP server when the
child exits. No outside metadata or Portico Services account is used.

```sh
python3 tests/e2e/server.py --binary /path/to/devtrust-server \
  --media-cache /path/outside/checkout --live -- python3 tests/e2e/live_smoke.py
```

The optional nightly and on-demand workflow checks that the guide exposes the
channel and schedule. It keeps its sanitized log for 14 days; credentials,
session data and server state are never uploaded. It is not a required merge
check. Playback was also exercised during the Android TV walk; this smoke check
does not replace an end-to-end player test.
