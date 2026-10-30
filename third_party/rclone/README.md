# Server rclone bundle

The install carries rclone 1.75.2, revision
`be8d6044eacb843b74d335db0c5fbe09222031fe`, for Linux, macOS and Windows on
amd64 and arm64. This is the same upstream release used by the G57 proof.
Packaging neither establishes nor overrides that proof's result.

`bundle.lock.json` pins official HTTPS archives, each extracted file, the
signed checksum document, source and vendor archives. `KEYS` is the upstream
release key set; verification accepts only fingerprint
`FBF737ECE9F8AB18604BD2AC93935E02FF3B54FA`, published at
<https://rclone.org/release_signing/> and in the pinned source. Run:

```sh
python3 scripts/rclone-bundle.py prepare third_party/rclone/bundle --target linux-amd64
scripts/verify-rclone-bundle.sh third_party/rclone/bundle linux-amd64 --execute-version
python3 scripts/test-rclone-bundle.py
```

The builder uses Python 3, curl and GnuPG. It downloads only at build time,
verifies the upstream signature and pinned archive hash, extracts only the
locked inventory and verifies the resulting payload. Installers copy this
payload unchanged; Docker builds its target's payload. The signed server
manifest binds `rcloneManifestSha256` to all distributed rclone bytes and notices.
The macOS sealer verifies the upstream payload before codesigning, retains its
original manifest in the signed source seal, and signs the resulting derivative
manifest. Custom programs never use this distribution-integrity mechanism.

`LICENSE` is upstream COPYING. The release zip contains README/manual/git history
but no complete module notices file. `DEPENDENCY-NOTICES.txt` retains the vendor
module inventory and all licence/copyright/notice files from the authenticated
vendor archive (including platform/build-only dependencies). The MIT, BSD,
Apache-2.0, MPL-2.0, ISC and other compatible permissive texts retain their
original copyrights. Notices are rendered as UTF-8; the few Windows-1252 source
files retain their copyright symbols and quotation marks. Corresponding
source/vendor URLs and hashes are in the lock.

The minimum custom version is 1.71.1. The source egress policy requires
`--http-proxy` / `RCLONE_HTTP_PROXY`, introduced in 1.71.0. Its explicit
proxy uses `http.ProxyURL`; environment-only discovery uses
`http.ProxyFromEnvironment`, which bypasses loopback destinations. Custom
1.70.x programs therefore cannot enforce the required policy route. Version
1.71.1 additionally fixes the pacer deadlock with `--max-connections`, used
by the source cap; 1.71.0 is not a supported custom version.
Sources: [1.71.1 cap fix](https://github.com/rclone/rclone/blob/v1.71.1/docs/content/changelog.md),
[1.71.0 release notes](https://github.com/rclone/rclone/blob/v1.71.0/docs/content/changelog.md),
[option registration](https://github.com/rclone/rclone/blob/v1.71.0/fs/config.go),
and [transport selection](https://github.com/rclone/rclone/blob/v1.71.0/fs/fshttp/http.go).
The remaining flag inventory predates that release: `--max-connections` was
added in 1.70.0, `--vfs-read-chunk-streams` in 1.68.0 and
`--vfs-cache-min-free-space` in 1.64.0. The minimum's flag availability is
independent of the bundled version and G57 correctness. When a consumer adds
an option it must update this inventory and its minimum.

`rcloneprogram.Resolve` uses `PORTICO_RCLONE` when supplied, otherwise
`<server directory>/third_party/rclone/rclone[.exe]`; it never silently falls
back to PATH. Docker sets the explicit path under `/opt/portico`. The server
checks the included program once at startup and exposes only the bounded
version/status snapshot to the owner. It continues serving local libraries if
rclone cannot start. `CheckCustom` runs only `version`, bounded to five seconds;
wrappers and in-place upgrades need no approval. Paths and stderr never appear
in public failure text. Later source consumers call it at their own start.
