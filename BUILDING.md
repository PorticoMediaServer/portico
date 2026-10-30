# Build Portico Media Server

This guide is for people who want to inspect or run the source. Most users
should install a package from the [latest release](https://github.com/PorticoMediaServer/portico-server/releases/latest).

## Requirements

- Go at the version declared in `server/go.mod`
- Node.js 24 and npm (CI, releases and the container image use exactly the release in `.node-version`; `scripts/check-node-version.mjs` keeps them together)
- Make
- FFmpeg and FFprobe for playback and transcoding tests

From the repository root:

```sh
npm ci
make build-web
GOFLAGS=-p=2 make build-server
mkdir -p .scratch/state
PORTICO_DATA_DIR="$PWD/.scratch/state" PORTICO_WEB_DIR="$PWD/web/dist" \
  PORTICO_BIND=127.0.0.1:32500 ./dist/portico-server
```

The binary is written to `dist/portico-server` unless `SERVER_OUTPUT` is set.
Use disposable development state. A raw development binary otherwise defaults
to a relative state path; setting the absolute path avoids sharing another run's
database accidentally. FFmpeg/FFprobe must be available for probing and playback;
qualified release bundles are validated against `third_party/ffmpeg`.

A development build (`make build-server`, `make dev-api`) carries the same
reader capability as CI but not the release trust marker: `make` reads
`scripts/release-build-tags.py --capability` (`portico_rar7z,portico_cb7`). Every
official build takes its Go tags from that one helper: the sealed release
builder and the container image use its release set
(`release,portico_rar7z,portico_cb7`), and CI compiles and tests the capability
set so tagged-only code cannot rot. Plain `go build ./cmd/server` without the
tags is still dark. To reproduce the reader capability locally, use the same
capability set:

```sh
(cd server && go build -tags "$(../scripts/release-build-tags.py --capability)" -o ../dist/portico-server ./cmd/server)
```

`scripts/test-official-build-tags.py` fails CI when a build, test or vet command
drops that capability, so a plain `-tags release` is a regression, not a local
preference.

For browser development, run `npm run dev:web` in another terminal. Vite proxies
`/v1` to port 32500. `make dev-api` runs Go source; provide the same explicit
state and web environment. Development and release trust roots differ.

Run focused checks while editing, for example:

```sh
(cd web && npx tsc --noEmit)
(cd server && GOFLAGS=-p=2 go test ./internal/naming -run Test)
```

`make test` is the broader Go/client verification entry point and can be costly;
`scripts/vet-go.sh` handles the maintained SQLite driver's inherited pointer
warnings. These commands do not establish real-device playback or service
installation acceptance. Read package-specific test instructions before running
media/performance tiers. Do not use a database or collection you care about for
fixtures or tests.

## Repository layout

| Path | Contents |
| --- | --- |
| `server` | The server (Go module `github.com/PorticoMediaServer/portico/server`); API reference in `server/api` |
| `web` | The web application every server serves |
| `packages/client-core` | The headless client core shared by the web and native apps |
| `packages/design`, `packages/i18n` | Design tokens and component specification; strings and formatting |
| `packages/contracts` | TypeScript contract generated from the server's API registry |
| `apikit` | The Go API toolkit (routes, schemas, cursors, idempotency) |
| `fixtures` | Test fixtures |
| `third_party` | FFmpeg recipe, patched hls.js, libass WebAssembly and subtitle fonts |
| `trust` | The Hosted root public key compiled into release builds |
| `packaging` | Linux, macOS and Windows package definitions used by releases |
| `scripts` | Build, release and test scripts |

The shared client packages here are the source of truth. The
[React Native clients](https://github.com/PorticoMediaServer/portico-react-native)
copy selected packages through their shared-package sync script. Verify the
recorded package versions when changing both repositories.

Release builds cover Linux x64/ARM64, Windows x64/ARM64, and Apple Silicon
macOS. Installer creation belongs to the release workflow because it requires
platform-specific tooling and an approved FFmpeg component release.

## Private services are not required

The repository contains the public Hosted protocol documents needed to compile
and test clients. Portico's Hosted Services implementation, operator tooling,
credentials, and internal planning are deliberately not part of this source
tree and are not required to build Portico Media Server.


## Monorepo checkouts

Clone `https://github.com/PorticoMediaServer/portico.git`. The server and apikit use the root Go workspace; apikit is also a separately versioned public module. Web and shared packages use the root npm lockfile. Native, Vega and website retain their own pinned lockfiles and each needs its own installation; their shared source imports resolve into the one root `packages/` tree. Do not copy or symlink node_modules between apps or worktrees.

From the root, use `npm ci`, then `npm --prefix apps/native ci --legacy-peer-deps --workspaces=false`, `npm --prefix apps/vega ci --legacy-peer-deps --workspaces=false`, and `npm --prefix website ci` for the components you need. `npm run check:native`, `check:vega` and `check:website` expose their type/check commands. Go recipes use `scripts/env.sh` to select the compiler pinned in server/go.mod.

Push and pull-request feedback uses affected short tests; the merge queue runs the full affected deterministic suite. Nightly owns pressure, performance, scale and package smoke; release owns source toolchains and signing. See [CI](ci/README.md).
