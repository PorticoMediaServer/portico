# Directional test-only first light

This harness uses **fixture source, NOT production seal proof**. It is a
fresh zero-WAL population, not ordinary warm append, OS-cold proof, sustained
service, reset-cadence or production resource qualification. All constructors,
permits and the target live in `_test.go`; the production source gate stays
closed without real typed stage/seal provenance.

Build a pinned Linux writer test binary off-host and run exactly
`^TestEffectiveStageFirstLightDirectional$`, once. Set
`PORTICO_WRITER_FIRST_LIGHT=1` and `PORTICO_WRITER_FIRST_LIGHT_CONFIG` to a JSON
file with exactly these fields:

```json
{"run_id":"unique-run","host_epoch":"external-epoch","source_commit":"0000000000000000000000000000000000000000","output_path":"/owned/scratch/new.ndjson","fixtures":1}
```

Use the actual declared source commit. `fixtures` is1..64; paths are absolute,
output is exclusive, and the config limit is16KiB. Unknown/duplicate fields and
trailing JSON refuse. There is no DB, SQL, profile, budget or proof input. Each
fresh owned database executes BEGIN, RESOLVE and ACK with the SAME original
receipt reference. Setup inputs and read capture are outside HOLD; every hidden
BEGIN image/pin/receipt effect uses the real facade, grant and transaction frame.

Absent opt-in may skip in normal suites. Explicit opt-in cannot successfully
skip: invalid setup, stubs, missing evidence, zero samples and stop/failure are
incomplete failures. SIGINT/SIGTERM and a fixed5-minute deadline cooperatively
cancel the active call. No next command starts after stop. Native return and
pool/opener join precede exit; nonpreemptible I/O can delay cleanup. Allow at
least7 minutes before a hard process/test timeout. A hard kill leaves a partial
prefix, never a completed run. Owned databases are disposable test fixtures,
not recovery artifacts; retain the NDJSON output separately.

## NDJSON v1

Version is `portico-writer-first-light-ndjson-v1`. Records are synced
incrementally, separately from testing logs: `start`, `phase`, `fixture_close`,
`final`. Every record binds run/host epoch, the exact config hash, actual running
binary hash, declared source commit, observed host/boot, and the fixture manifest
hash. The manifest includes schema identity and the fixed experimental profile;
`WCanonicalTestOnly=true`, `ProductionSealProof=false`, `production_qualified=false`.
Source and binary identity must also match the external pinned-build receipt.

Phase records include ordinal, assigned database ID plus actual path, phase,
original reference, raw typed result or ACK, actual error/panic and this-call
`ExecutionEvidence`. `hold_ns` and `commit_ns` are derived from owner timestamps
in-process BEFORE JSON removes monotonic components. Absent points remain null;
`harness_wall_ns` is separate. Known partial intervals survive an incomplete
observation. Actual fence/ambiguity/pool snapshots are nullable, never inferred
from error strings. New WAL restart observations are emitted once per sequence
per database; initial header creation is not a restart. No cadence is inferred.

`samples` counts attempted phase calls; `complete_samples` counts complete phase
calls. `complete=true` on a phase requires actual expected result and physical,
policy/WAL/COMMIT evidence with no error/fence. Failed/committed-with-error calls
remain raw records and stop the fixture. `native_joined` is observed at fixture
close; null means unverified, not success. Final completion requires every
requested phase, successful joins and no stop/test/output failure. Missing final,
truncated lines or an incomplete final must never be classified as a pass.
