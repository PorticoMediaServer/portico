# Lane tests and captures on standard runners

Use this before occupying a development host for tests. Push your commit, then dispatch:

```sh
gh workflow run lane-focused.yml --repo PorticoMediaServer/portico --ref main -f source_ref="$(git rev-parse HEAD)" -f packages='./internal/store/writer' -f run='TestInventoryFresh|TestN1' -f shards=auto
```

Find, wait for, read and download the result: `gh run list -w lane-focused.yml -L 5`, then `gh run watch RUN_ID --exit-status`, `gh run view RUN_ID --log-failed` and `gh run download RUN_ID`. The planner and combined summary print the full run URL. Choose your run by its source/ref and dispatch time when several lanes dispatch together. [GitHub CLI reference](https://cli.github.com/manual/gh_workflow_run).

The workflow file must first exist on main. `--ref main` selects the runner tooling; `source_ref` independently selects any pushed branch or full 40-character SHA in this repository. Branches resolve once to a SHA before scheduling shards. The plan and every receipt retain that SHA. Fork-only commits must be pushed to a repository branch first.

Defaults: `module=server`, `runner=ubuntu-24.04`, `packages=./internal/store/qual`, `run=.`, `race=false`, `count=1`, `shards=auto`, `mode=tests`. `module=apikit` and `runner=macos-15` are supported. Packages are whitespace-separated module-relative patterns, including `./...`. `run` is Go's regular expression, including slash-separated subtest filters. `tests` optionally supplies whitespace-separated exact parent names; absent names fail. Capability tags come from the existing build-tag source; the certified `release` tag is never selected.

Automatic splitting is the default: discover build-selected test files with Go's parser, then assign sorted top-level tests round-robin, about 25 parents per runner, up to 32 runners. A single large package and a long exact test list both split across runners. `shards=1..64` overrides the automatic count, bounded by selected parents. There is no cross-lane concurrency lock or matrix parallelism limit. A single parent (and its subtests) stays together; this first version does not balance historical durations. Each shard rebuilds its selected packages. Do not depend on test ordering or package-global state across parents.

The original `-run` filter reaches Go unchanged; `-skip` excludes parents assigned elsewhere. Subtest filters conservatively discover all candidate parents, then Go selects the actual matches. A completely unmatched filter fails the combined result. Packages with no tests are explicitly compile-only. An exact parent list produces an anchored parent selector; a combined subtest filter retains the original matcher and excludes every unselected package parent. Go's successful skips remain visible; a skipped opt-in target is **not** evidence that it ran. Race findings, failed tests/builds, process timeouts, missing parent terminals, missing shards and mismatched source receipts fail. There is no quarantine or retry. `count` is 1..100. Each package invocation has a 25-minute Go deadline, a 30-minute process-group watchdog and a 45-minute shard job limit; planning has 20 minutes and aggregation 5 minutes.

## Captures and fixed harnesses

`mode=capture` enables only the deliberately named recording flags below (`count=1`). `packages`, `run` and `tests` are ignored in capture/harness modes; `targets` is required. Example: `gh workflow run lane-focused.yml --ref main -f source_ref="$(git rev-parse HEAD)" -f mode=capture -f targets='hn-canonical hn-browse'`. Files, their SHA-256 hashes and comparisons with the checked-out baselines are uploaded; differences remain visible and require owner review. Nothing commits, adopts or seals a golden.

| Capture target | Recorded files under owning package's testdata |
| --- | --- |
| hn-canonical | canonical.json, canonical-fk.json.gz |
| hn-issuer | issuer-source-octets.json |
| hn-issuer-library | issuer-library-root-octets.json |
| hn-autoindexes | autoindexes.json |
| hn-scan | scan-inventory-accept.json, scan-inventory-accept-fk.json.gz |
| hn-scan-fresh | scan-inventory-fresh.json, scan-inventory-fresh-fk.json.gz |
| hn-wc-security-composition | wc-security-composition.json, wc-security-composition-fk.json.gz |
| hn-wc-progress-composition | wc-progress-composition.json, wc-progress-composition-fk.json.gz |
| hn-wc-progress | wc-progress-capture_start.json, wc-progress-pending_drain.json, wc-progress-fk.json.gz |
| hn-browse | browse-plans.json.gz |
| hn-foreground | foreground-plans.json.gz |

HN byte identity: **not fully byte-identical** at the default toolchains: Ubuntu captured 8/14 identical files; the six gzip files differ in compressed bytes but all 14 JSON payloads are byte-identical after decompression (mini-1 Go1.27.1; hosted Go1.26.10). [Pinned nine-target pilot](https://github.com/PorticoMediaServer/portico/actions/runs/42314245107), source `a055479469953f43c194615e49c568d4f1f00888`. Captures can be collected remotely for owner review; raw gzip goldens must not be silently replaced. macOS capture identity is untested.

`mode=harness` accepts a fixed functional target list: `connections` (shared connection-policy controls), `wal-protocol` (governor WAL controls), `n1-protocol` (N1 observation/presence controls), `wc-progress` (WC lineage/native protocol controls), `ec5-memory` (tiny N1/read memory control inside its own assigned2e9B/zero-swap cgroup on Ubuntu). The EC5 controller uses sudo only to create/limit/join/remove its unique leaf, then runs the existing native fixture as the runner user. It is a functional control, not a measurement or fit verdict. EC5 source/counter details are in writer/N1_COMPONENT.md. Exact selectors live in `ci/lane_focused.py`; absent matches fail. These are functional tests, not opt-in timing workloads. For another functional test, use ordinary `packages`/`run`/`tests` inputs instead of adding arbitrary command strings or flags.

Every shard uploads raw Go JSON, stderr, exact argv, source-bound receipts and SHA-256 manifests. Tests can write additional bounded evidence to `PORTICO_LANE_EVIDENCE_DIR`; those files are uploaded too. Temporary test homes and caches are excluded. Uploads run after failure; a canceled runner can leave a missing receipt, which the summary refuses. Artifacts expire after 14 days. The combined `lane-summary` artifact and job summary show every shard and capture comparison.

## Scope and permissions

This is tests and captures, **never performance qualification**. Runner timing in Go logs does not qualify a hold, deadline, host profile or service budget. It has only `workflow_dispatch`, disposable `ubuntu-24.04`/`macos-15` runners, `contents: read`, no secrets, no environments and no reusable/release/signing workflow calls. Checkout credentials are not persisted; dependency-cache publishing is disabled. Inputs enter through environment variables, are validated, then become subprocess argv without a shell. Auth and Actions file-command environment channels are removed before source code runs. As with any CI job, pushed test code itself executes on the disposable runner; this is not a sandbox against hostile repository maintainers.

Focused runner controls: `python3 -m unittest discover -s ci -p test_lane_focused.py` and `go test ci/lane_inventory.go ci/lane_inventory_test.go`. No product suite is needed to test the planner.

Hosted pilots of the first implementation: [two-shard connection/restart controls](https://github.com/PorticoMediaServer/portico/actions/runs/42314241494), [macOS race controls](https://github.com/PorticoMediaServer/portico/actions/runs/42314248504), and [automatic single-package N1 split](https://github.com/PorticoMediaServer/portico/actions/runs/42314280693) all passed with complete combined summaries. Because a new workflow cannot dispatch before it exists on main, pilot tooling commit `301bbd117d06c01fe841cd9e02dc1bc8d5918a57` ran the exact YAML through the existing manual `native-plan-pair.yml` entry on an isolated pilot branch; source remained pinned to `a055479469953f43c194615e49c568d4f1f00888`. Main and the existing diagnostic definition were untouched.
