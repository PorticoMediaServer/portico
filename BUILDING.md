# Building Portico

This is for people who want to read the code, run it or work on it. If you just want to use Portico, [install a release](INSTALLING.md) instead.

## What you need

- Go, at the version in `server/go.mod`
- Node.js 24 and npm. CI, releases and the container image all use the exact version in `.node-version`.
- Make
- FFmpeg and FFprobe, for playback and transcoding

## Getting the code

```sh
git clone https://github.com/PorticoMediaServer/portico.git
cd portico
npm ci
```

Everything lives in this one repository. The server and `apikit` share the root Go workspace, and `apikit` is also published as its own Go module. The web app and shared packages use the root npm lockfile.

The native, Vega and website apps each have their own lockfile, so install the ones you need separately:

```sh
npm --prefix apps/native ci --legacy-peer-deps --workspaces=false
npm --prefix apps/vega ci --legacy-peer-deps --workspaces=false
npm --prefix website ci
```

All of them import shared code from the one `packages/` folder. Don't copy or symlink `node_modules` between apps or worktrees; give each its own install.

## Running the server

```sh
make build-web
GOFLAGS=-p=2 make build-server
mkdir -p .scratch/state
PORTICO_DATA_DIR="$PWD/.scratch/state" PORTICO_WEB_DIR="$PWD/web/dist" \
  PORTICO_BIND=127.0.0.1:32500 ./dist/portico-server
```

Then open `http://127.0.0.1:32500/`.

The binary goes to `dist/portico-server`, or wherever `SERVER_OUTPUT` points. Always give it a throwaway state folder like the one above. Without one, a development build uses a relative path, and you can end up sharing a database with another run by accident. Never point tests or development builds at a library or database you care about.

For web development, run `npm run dev:web` in a second terminal. Vite proxies `/v1` to port 32500. `make dev-api` runs the server from source; give it the same state and web settings. Development builds and release builds trust different signing keys.

## Build tags

Some features, like the CBR and CB7 comic readers, only compile with specific Go build tags. The list lives in one place, `scripts/release-build-tags.py`:

- **Capability tags** (`portico_rar7z,portico_cb7`) turn on those readers. `make`, CI and development builds all use them.
- **Release tags** add `release` on top. Only the sealed release builder and the container image use them.

A plain `go build ./cmd/server` leaves those readers out. To build exactly what CI builds:

```sh
(cd server && go build -tags "$(../scripts/release-build-tags.py --capability)" -o ../dist/portico-server ./cmd/server)
```

CI fails if a build, test or vet command drops the capability tags (`scripts/test-official-build-tags.py`), so a plain `-tags release` is treated as a bug.

## Testing

Run focused checks while you work:

```sh
(cd web && npx tsc --noEmit)
(cd server && GOFLAGS=-p=2 go test ./internal/naming -run Test)
npm run check:native
npm run check:vega
```

`make test` runs the wider Go and client suites and can take a while. `scripts/vet-go.sh` handles some known warnings from the bundled SQLite driver. Check a package's own instructions before running its media or performance tests.

Passing tests don't prove playback works on real devices, or that an install works as a service.

CI runs in tiers:

- Every push and pull request gets fast checks on the code that changed.
- The merge queue runs the full test suite for that code before anything reaches `main`.
- Nightly runs cover performance, stress and package checks.

Toolchains and signing only run for releases. See [ci/README.md](ci/README.md).

## What's where

| Path | What it is |
| --- | --- |
| `server` | The server (Go module `github.com/PorticoMediaServer/portico/server`), with its API reference in `server/api` |
| `web` | The web app every server includes |
| `apps/native`, `apps/vega`, `apps/desktop` | The phone, tablet and TV apps, the Vega TV app, and the desktop app |
| `packages/client-core` | Client logic shared by the web and native apps |
| `packages/design`, `packages/i18n` | Design tokens and component specs; text and formatting |
| `packages/contracts` | TypeScript API types generated from the server |
| `apikit` | The Go API toolkit (routes, schemas, cursors, idempotency) |
| `fixtures` | Test data |
| `third_party` | FFmpeg and media engine recipes, patched hls.js, the subtitle renderer and fonts |
| `trust` | The Portico Services public key built into release builds |
| `packaging` | Linux, macOS and Windows package definitions |
| `scripts` | Build, release and test scripts |
| `website` | getportico.tv |

## Platforms

Releases cover Linux (x64 and ARM64), Windows (x64 and ARM64) and Apple Silicon Macs. Installers are built by the release workflow, because they need platform tools and an approved FFmpeg build. See [RELEASING.md](RELEASING.md).

## You don't need Portico Services

This repository includes the public Portico Services protocol documents the apps need to build and test. The service itself, its operator tools and credentials live in a separate private repository, and you don't need any of it to build Portico.
