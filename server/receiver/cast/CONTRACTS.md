# Cast contract bundle

The classic receiver loads `contracts.js` before `receiver.js`. Its entry is
`scripts/cast/contracts.ts`, which imports the exact generated
`decodeStartPlaybackSessionResponse`; Rolldown includes that generated decoder,
runtime and complete schema graph. No copied or reduced schema is maintained.

Normal containing generation, from the repository root with the exact locked
Node dependencies already prepared:

```sh
(cd server && GOTOOLCHAIN=go1.26.10 go run ./cmd/apigen -root ..)
node scripts/cast/generate.mjs
python3 scripts/schema-sync.py
```

The deterministic browser IIFE is emitted at
`server/receiver/cast/contracts.js` and embedded with the receiver. The projection
uses the existing locked Rolldown from `web`; it installs nothing. Generate
it after official apigen, then include the output in the same containing
canonical generation/mirror cohort. The authored source does not qualify a
stale or missing generated output.

Body validation and HTTP status have separate jobs: a real 202 owns only its
original cleanup identity and never completes CAF LOAD. Only a valid 201 admits
completion. A closed retry-only 202 owns no session. The canonical lease pattern
checks the producer's UTC RFC3339Nano spelling; calendar correctness and actual
future lease validity remain server authority checks.
