# Python regression inventory

`python3 scripts/run-python-regressions.py --report-dir /tmp/portico-python-results`
runs every suite in `python-regressions.json`. Use `--list` to inspect the
inventory, or repeat `--suite scripts/test-name.py` for a focused local run.
CI runs the entire inventory hourly and through the reusable CI workflow. Its
result is required by the existing `repository` job.

The runner uses one process group per suite. It records each test start and
outcome to flushed JSONL, retains the full verbose unittest log, and updates
`results.json` after each suite. Test deadlines fail the assertion and suite
deadlines terminate the fixture process group; neither is a pass or a skip.
The outer CI time limit is a backstop. A failing suite does not prevent the
remaining suites from reporting their results.

## Fixtures and dependencies

Provision the pinned `scripts/requirements-release.txt` dependency before
running the tests. The suites themselves must remain offline and use fresh,
disposable signing keys; they must never read production release credentials.
Node is needed by the CI-runner regression suite, and packaging tests require
the usual Bash, tar, zip and sed tools.

| Suite | Inputs and scope |
| --- | --- |
| `test-ci-affected.py` | Disposable source trees and subprocesses; tests selection and runner behavior. |
| `test-release-seal.py` | Disposable release packages and Ed25519 keys; executes the real seal scripts. |
| `test-release-fixture.py` | Synthetic pinned rclone contract and payload; executes the unchanged release verifier for Linux, Windows and macOS, including tampering refusals. |
| `test-portable-packaging.py` | Disposable signed payload with the real checked-in portable launchers, installer recipes and license inputs; installer command fixtures keep the test focused on archive behavior. |
| `test-macos-seal.py` | Disposable media/rclone inventories and signer, real checked-in app/package recipes, controlled OS-signing and Go-build-info commands; verifies final signatures, provenance and tampering refusals. It never downloads rclone. |
| `test-server-release-snapshot.py` | The actual server module and mandatory checked-in siblings; tests faithful capture and tamper detection. Missing checkout inputs are failures. |

The synthetic rclone contract belongs only to the disposable test repository.
No production verifier, trust root or pinned distribution is replaced. These
tests do not claim to authenticate an upstream rclone release, compile a real
server, validate an OS signature, or qualify an actual installer on a device.
Those remain the existing build-validation/release jobs' responsibility.

An incomplete exported work package may lack packaging files, licenses or
source siblings. Report that limitation; do not fabricate a launcher, loosen
verification, skip the suite, or fetch missing inputs to make it pass.
