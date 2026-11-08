# Reusable store population artifact

This manual producer runs once on GitHub's standard `ubuntu-24.04` runner. All
consumer shards reuse the **same producer run ID and artifact name**. It does
not measure, profile, qualify timing, adopt evidence or change production
enablement, openers or authority. The default `canonical-movies-v1` shape is a
minimal movie population, not the enriched/search/probe workload or production
authority. `-f search_bank=true` selects the separate `canonical-movies-v1-search`
shape, which keeps the same catalogue and adds one real v1 search bank for every
resident plus a deterministic People/credit subset; the plain shape and its
consumers are unchanged. No 100k+ generation belongs on a development host.

## Dispatch and retrieve

After the workflow and parent Go export target have been integrated and the
source has passed review and merged into `main`, dispatch a qualification
population with the exact reviewed, merged 40-character source SHA:

```sh
gh workflow run store-qualification-fixture.yml \
  --repo PorticoMediaServer/portico --ref main \
  -f source_ref=REVIEWED_MERGED_FULL_SHA -f purpose=qualification \
  -f seed=0 -f residents=1000000 -f timeout_minutes=60 -f search_bank=false
gh run list --repo PorticoMediaServer/portico \
  --workflow store-qualification-fixture.yml --limit 5
gh run watch PRODUCER_RUN_ID --repo PorticoMediaServer/portico --exit-status
gh run view PRODUCER_RUN_ID --repo PorticoMediaServer/portico --log-failed
gh run view PRODUCER_RUN_ID --repo PorticoMediaServer/portico \
  --json url,headSha,event,conclusion
gh run download PRODUCER_RUN_ID --repo PorticoMediaServer/portico \
  --name qual-population-receipt-PRODUCER_RUN_ID-1 --dir producer-receipt
gh run download PRODUCER_RUN_ID --repo PorticoMediaServer/portico \
  --name qual-population-PRODUCER_RUN_ID-1 --dir population
```

Replace uppercase placeholders before running. Choose your dispatch by its
source/ref and dispatch time when others are running; read `report/receipt.json`
from the receipt artifact (download may flatten its root to `receipt.json`).
The producer prints the full run URL. Artifact suffix `1` is the run attempt;
reruns have a different attempt suffix and must be pinned separately. Never
resolve a floating "latest" artifact or regenerate the population per shard.
Both artifacts expire after 14 days; retain required evidence before expiry.

`--ref main` selects reviewed producer tooling independently of `source_ref`.
The latter supports pushed repository branches (optionally `refs/heads/…`)
and full SHAs; it resolves once and records the actual detached checkout SHA.
Tags, expressions, options and shell syntax are refused. Preview defaults to
`purpose=preview`; for a pushed branch use the same command with
`-f source_ref=your/pushed-branch -f purpose=preview -f residents=10`.
Its receipt explicitly says **NOT qualification: preview source, never
auto-adopt**. Preview artifacts never become qualification evidence by being
downloaded. No producer run is authorized before parent integration.

Qualification purpose fails unless the source is an ancestor of fetched
`main` and the producer tooling runs from `main`; the receipt records that main SHA. Protected-main review and the
operator's check of the reviewed merge remain prerequisites. This reachability
check is not an independent attestation of reviews and is never a timing
qualification verdict. The consumer owner must check the successful run,
purpose and source against the reviewed merge before pinning a receipt.

## Protocol and receipt

Inputs enter Python through environment variables and strict validation before
fetch/build. Seed is uint32, including zero; residents are 1..1,000,000 (default
1,000,000); export timeout is 1..90 minutes (default 60). Build timeout is 20
minutes, the export watchdog adds 30 seconds to Go's deadline, and the job limit
is 120 minutes. Subprocesses receive argv without a shell; the watchdog kills
their process group. Paths are absolute, private and disjoint; source subprocesses
receive no auth tokens or Actions file-command channels. `GOTOOLCHAIN=local`,
`GOWORK=off`, `CGO_ENABLED=0`, readonly modules and no caller build flags are used.
Go must be exactly `go1.26.10`, matching `server/go.mod`. The actual test-binary build info must identify Go1.26.10 and the writer test
package. Go test binaries omit dependency entries from build info; a same-source,
same-environment `go list -deps -test` separately proves the selected, unreplaced
`modernc.org/sqlite/lib` from v1.58.0 plus the repository's bundled
`internal/thirdparty/sqlite` driver. An upstream root driver or replaced module
fails this check. The actual shared connection assertion
must also report bundled SQLite engine `3.53.4`. There is no alternate engine or system SQLite path.

From `server`, the producer builds:

```text
go test -p 1 -c -o /absolute/private/work/writer.test ./internal/store/writer
```

It runs that pinned binary from `server/internal/store/writer` with
`-test.run=^TestExportQualificationPopulation$ -test.count=1
-test.timeout=60m -test.v` (the timeout follows the bounded input), and
`PORTICO_QUAL_FIXTURE=1`, `PORTICO_QUAL_FIXTURE_HOSTED=1`,
`PORTICO_QUAL_FIXTURE_CONFIG=/absolute/private/work/export-config.json`.
The strict JSON config has exactly four fields:

```json
{
  "output_dir": "/absolute/private/work/export",
  "source_commit": "0123456789abcdef0123456789abcdef01234567",
  "seed": 0,
  "residents": 1000000,
  "search_bank": false
}
```

The source target joins and closes its database before exporting
`population.sqlite` and `population.json`. Python never opens SQLite, executes
SQL, accepts a caller DB/factory, or supplies write authority. The v1 manifest
records source/schema, engine/source ID, exact Go version, actual connection
policy, explicit `dqs_ddl`/`dqs_dml`, image SHA/length, and facts (shape, seed,
resident count, library 7 / `qualification-movies`, first/last IDs, counts and
logical hash). Top-level and facts fields are closed for this v1 contract;
counts is a bounded source-owned table-name map. A shape/envelope change needs
an explicit producer update. The search shape adds `facts.search_bank`
(entities/people/credits/publications/jobs/fragments/prepared bytes/own-prefix/
seals/logical digest), the concrete `facts.statistics` statement (the producer
runs no ANALYZE, so statistics are absent), and the post-bank
`search_bank_integrity`/`search_bank_foreign_keys` results (`ok`/`empty`). Its
People/credit coverage is a path subset (first and every 128th resident), not
People at scale. Actual Settings keep their source JSON spelling
and optional omitted zero DQS/read-limit fields.

Validation binds source/schema/seed/residents and checks the policy, ID/count
bounds, image length (at most 8 GiB), streaming SHA-256 and absence of `-wal` or
`-shm` sidecars. Only after the target succeeds and validation passes are the
two files renamed into the publish directory. There is no extra multi-GB
Python copy. Failed/skipped targets never publish an image, even if they wrote
partial files. A failure receipt and command argv/logs are retained separately;
the image artifact also includes `report/receipt.json` so its preview warning
travels with the image (the separate receipt remains the review/pinning source).
If setup never reached the producer, the fallback receipt points to GitHub job
logs. Runner loss/cancellation can prevent final uploads: missing receipt,
missing image, or unsuccessful workflow means unusable evidence. `complete`
means local producer validation passed; artifact upload/run success must also
be checked.

The external receipt independently records the actual image and manifest
hashes, binary hash and `go version -m` build identity, source/tooling/workflow,
full `git archive` SHA-256 (archive scratch removed),
module/sum hashes, runner image, actual policy and immutable run/artifact lookup.
Cache identity is source SHA + schema SHA + shape version (including the search
flag) + seed + resident count; it is **not** an image digest. Deterministic logical content has its own
`facts.logical_sha256`; separate generations need not have identical SQLite
bytes. Consumers pin the actual image through the trusted manifest SHA.

## Reuse in every shard

Record the exact producer run, attempt, artifact name, source/schema and
manifest SHA in the owner's reviewed consumer configuration. Obtain
`manifest_sha256` from the independently reviewed receipt; do not compute a pin
from the untrusted file being verified. All shards use those same values.
Example consumer workflow fragment (replace placeholders with literal pins):

```yaml
permissions:
  contents: read
  actions: read # concretely required for cross-run artifact download
# Inside each consumer shard, after checking out its reviewed workload SHA:
steps:
  - uses: actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093
    with:
      github-token: ${{ github.token }}
      repository: PorticoMediaServer/portico
      run-id: 'PRODUCER_RUN_ID'
      name: qual-population-PRODUCER_RUN_ID-1
      path: population
  - name: Verify the pinned image before running the owner test
    env:
      CONSUMER_SOURCE: REVIEWED_WORKLOAD_FULL_SHA
      POPULATION_SOURCE: REVIEWED_PRODUCER_FULL_SHA
      POPULATION_SCHEMA: PINNED_SCHEMA_SHA256
      POPULATION_MANIFEST: PINNED_MANIFEST_SHA256
    run: |
      test "$(git rev-parse HEAD)" = "$CONSUMER_SOURCE"
      python3 -B ci/qual_fixture.py verify --artifact "$GITHUB_WORKSPACE/population" --source "$POPULATION_SOURCE" --schema "$POPULATION_SCHEMA" --manifest-sha "$POPULATION_MANIFEST" --seed 0 --residents 1000000
# add --search-bank for a canonical-movies-v1-search artifact
```

The owner test then calls the parent test-only API before its existing canonical
fixture constructor:

```go
UseQualificationPopulation(t, artifactDir, pinnedManifestSHA)
// Invoke this exact test owner's existing canonical fixture constructor next.
```

`artifactDir` must be absolute. The API verifies the manifest pin, copies the
immutable source image for the same test owner and leaves existing connection
openers/policy unchanged. Shards never open or mutate the downloaded image in
place. The producer source and workload consumer source are separately pinned. A
later reviewed class target accepts the image through its population content
identity, not by comparing test-source bytes: the recorded shape (kinds and
counts), seed, resident count, logical digest and embedded schema must equal
what that consumer reviewed. A matching schema alone is not that decision, and
a shape with no reviewed identity refuses. If population semantics changed,
generate a new artifact and record its new content identity. Do not rewrite the
producer source in its manifest or relabel the old binary as the new target. The
Python verifier checks the producer source pin; the parent API checks embedded
schema and the trusted manifest digest. Each workload receipt records both
identities. No automatic adoption across source revisions occurs.

Focused local protocol controls only:
`python3 -B -m unittest discover -s ci -p test_qual_fixture.py`.
These use clearly synthetic bytes, never a real SQLite population, and make
no engine, scale, performance or qualification claim. Actual producer execution
and 1M population generation require integrated source on GitHub runners.
