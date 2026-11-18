# Continuous integration

`merge.yml` runs on every pull request, every merge-queue group and on demand. Its required
check is `ci/required`, which passes only when every job the planner selected passed. Merges go
through the protected queue, rebase only. `nightly.yml` runs the full inventory at 04:17 UTC.

## Jobs in `merge.yml`

| Job | What it runs |
|---|---|
| `planner` | `ci/plan.py`: picks the jobs and tests the changed paths need (below), and `ci/commit_bylines.py` (no AI authors or trailers) |
| `hygiene` | `python3 -m unittest discover -s ci`, `ci/source_guard.py`, `ci/secret-scan.sh`, the engineering ratchet (`ci/engineering_rules.py`, see [ENGINEERING-RULES.md](../docs/ENGINEERING-RULES.md)) and the client API ratchet (`ci/client_api_census.py`, see [client-api-ownership.md](../docs/client-api-ownership.md)) |
| `server_next` | In `server-next`: `go build`, `go vet`, `go test ./...` with `CONTRACTS_CHECK=1` (generated contracts must be committed), and the comic-archive test with the official capability tags. Runs when `server-next/` or `merge.yml` changes, and for every queue group |
| `go_build`, `go_vet_cross`, `go_tests` | The `apikit` module: build, cross-platform vet and duration-balanced test shards |
| `js`, `js_static` | Tests per component (`web`, `packages`, `apps/native`, `apps/vega`, `website`) in duration-balanced shards; then types, lint, strings, design tokens, boundary checks and `scripts/check-no-external-fonts.mjs` against the built web bundle (`ci/run.py`) |
| `android` | `android-tests.yml` when Android or Robolectric inputs change |
| `roku` | `apps/roku/tools/check.sh` and `test.sh` when `apps/roku/` changes |

## How the planner chooses

- A path under `server-next/` selects `server_next`; `apps/roku/` selects `roku`.
- A path under a JS component selects that component; `packages/` selects every component but
  the website. `packages/contracts/` and `apikit/` also select the Go jobs.
- Markdown and images select nothing.
- Any other path selects everything.
- PRs run the short tier; queue groups plan against the target branch's tip and run the merge
  tier. `workflow_dispatch` with `full=true` runs everything.

Test files are packed into shards by measured duration: `ci/js-durations.json` for JS, and
`ci/merge_durations.json` and `ci/durations.json` for Go. A file with no measurement still runs,
estimated from its component. Each JS shard reports every file it was given, and the aggregate
fails if one is missing.

## Other workflows

- **server-next:** crash, load, media, upgrade, container home and the Windows install and
  service runs (`server-next-*.yml`).
- **Apps and packaging:** desktop, Apple build and TestFlight, Android tests, packaging
  qualification, FFmpeg and media tools.
- **Release and website:** release, release-published and website.
- **Repository:** CLA and labels.

## Upkeep

- `ci/quarantine.json`: exact package and test exceptions, each with an owner, a reason and an
  expiry. An expired entry fails CI.
- `python3 ci/update_js_durations.py --plan selection/ci-plan.json --results results/js --source RUN_URL`
  refreshes JS durations from a passing run.
- `python3 ci/update_merge_durations.py --runs RUN_ID` and
  `python3 ci/refresh_go_durations.py --pr N` refresh Go durations.
- `python3 ci/timings.py RUN_ID...` reports wall time, runner minutes and the runner hardware of
  given runs.
- `ci/package-smoke.py --package TARBALL` installs and starts a built Linux package and checks
  that it is ready.
