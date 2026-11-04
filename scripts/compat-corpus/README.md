# Release compatibility evidence

The current development corpus is `packages/client-core/test/compat-corpus/main`.
It is a development producer recording, not a released 1.0 compatibility claim.
The historical `648` stand-in remains unsupported at sign-in; it is not a v1
server or a released compatibility baseline.

Refresh current responses from a disposable loopback-only music fixture:

```sh
portico-heavy api-corpus -- env GOFLAGS=-p=2 python3 scripts/compat-corpus/refresh.py \
  --scratch /absolute/disposable/scratch \
  --out /absolute/corpus/main
PORTICO_COMPAT_CORPUS_ROOT=/absolute/corpus node --experimental-transform-types --test --test-concurrency=2 \
  packages/client-core/test/compat-corpus.test.ts packages/client-core/test/compat-corpus-freeze.test.ts
```

Run from a clean reviewed producer checkout. This builds the current server
with VCS stamping, verifies its embedded revision and clean flag, hashes the
actual binary, creates two synthetic FLACs, completes local
setup, scans one library and calls `record.mjs`. It closes its server process
tree and removes the temporary state, password and binary even on failure. It
never opens an existing database. Review the external scrubbed recording before copying it into the development
corpus and committing. The recording commit changes HEAD; it does not change
which binary produced the responses.
The recorder also supports a separately prepared disposable server using its
`--base`, `--username`, `--password-file`, `--build-info` and `--out` arguments.
The build-info JSON must come from the actual binary verification, not the
recorder working directory or a manually relabelled producer. Never record a
real user's server. Current-corpus missing responses, 404s and parser failures
must fail tests; do not restore current-release TODO/skip exemptions.

The ordinary corpus covers negotiation, Home/directory/browse, item/detail,
playback offers, File info readiness/windows, queues and scoped event polling.
It also records actual malformed-continuation and missing/stale mutation
preconditions. Successful reads alone are insufficient compatibility coverage.
Signed offline receipt producer/verifier cases, restart/resync scenarios and
real platform transport acceptance remain release qualification gates; their
own existing tests do not make them frozen corpus cases automatically.

At the first stable release, re-record into an external directory from the
clean reviewed release commit, then freeze that external recording without
committing it into the checkout first. After qualification and credential review, run
from that same commit (replace `<commit>` with its full SHA):

```sh
node scripts/compat-corpus/freeze.mjs --version 1.0.0 \
  --server-commit <commit> --parser-commit <commit> --corpus /absolute/corpus/main --out /absolute/release-evidence/1.0.0
node scripts/compat-corpus/freeze.mjs --verify --out /absolute/release-evidence/1.0.0
```

The command freezes OpenAPI, generated schemas/types/runtime and corpus with
SHA-256 inventory and exact producer/parser pins. It refuses an existing output
and pins that differ from the checked-out release. It also rejects a corpus
whose recorded producer/build revisions do not both match the claimed producer,
a dirty build or missing binary provenance, before creating an archive.
The existing development recording predates binary provenance; it must be
re-recorded before a release freeze and cannot be relabelled as current HEAD. Retain that immutable artifact
with the release evidence; do not overwrite a released baseline with main.
No 1.0 archive exists yet and this command does not publish or tag a release.

Subsequent releases must run both directions of O-159 before tagging:

1. Current parser against each supported frozen producer corpus.
2. Each released parser, checked out at the archived parser pin, against a newly
   recorded current producer corpus. Set `PORTICO_COMPAT_CORPUS_ROOT` to that
   corpus root for the archived harness. Retain that parser's matching contracts.
3. Current/newer Cast sender/receiver contract projection checks, plus the
   platform transport, extension and offline receipt verification suites.

Use GitHub-hosted disposable jobs. Archive their commit pins, request evidence
and results. Adding unknown response object fields is an additional forward
compatibility check; it cannot replace a released parser/current producer run.
Do not add an API major, capability, clock condition or error normalization to
make a corpus green. Identity and signed authority remain strict; presentation
responses keep the existing additive-field/unknown-variant policy.
