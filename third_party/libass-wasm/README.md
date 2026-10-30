# libass for the browser (@jellyfin/libass-wasm 4.2.4)

The web player draws styled subtitles (ASS and SSA) with libass compiled to WebAssembly, so a styled track no longer has to be burned into the picture. This folder holds the published build, unmodified.

- **What:** `@jellyfin/libass-wasm` 4.2.4, the Jellyfin project's build of JavascriptSubtitlesOctopus (libass, FreeType, HarfBuzz, FriBidi, Fontconfig, Expat, Brotli and zlib compiled with Emscripten, a worker, and a small main-thread wrapper).
- **Source:** https://github.com/jellyfin/JavascriptSubtitlesOctopus, commit `2d4ec9b16a639f23733cd32982e09e0eb757f092`.
- **How it was obtained:** `curl -O https://registry.npmjs.org/@jellyfin/libass-wasm/-/libass-wasm-4.2.4.tgz` on 13 October 2026. The archive's SHA-512 matched the registry's integrity value and its SHA-256 is recorded in `provenance.json`. The package was published by the project's GitHub Actions workflow with an npm provenance attestation. It has no runtime dependencies and no install scripts are run: it is never installed with npm.
- **What is kept:** the archive itself, and four files copied out of it byte for byte into `files/`: `subtitles-octopus.js` (main thread), `subtitles-octopus-worker.js` and `subtitles-octopus-worker.wasm` (the worker; it looks for the `.wasm` beside itself under that exact name), and `default.woff2` (Liberation Sans, the default font). The asm.js fallback worker for browsers without WebAssembly is not shipped; those browsers get text subtitles.
- **Check:** `python3 -I third_party/libass-wasm/verify.py` compares the kept files with the archive and the recorded digests.

## Licences

The wrapper is MIT (`LICENSE`). The compiled worker contains several libraries under their own terms, all reproduced in `COPYRIGHT` (shipped with the package): libass (ISC), FreeType (FreeType Licence), HarfBuzz and Fontconfig (MIT-style), Expat and Brotli (MIT), zlib (Zlib), and **FriBidi under the GNU Lesser General Public License, version 2.1 or later**. FriBidi is the one part that is not permissively licensed. libass cannot be built without it, so every WebAssembly build of libass carries it. LGPL-2.1-or-later code may be distributed as part of a GPL-3.0-or-later work such as Portico; the obligations are to keep these notices with the files and to make the corresponding source available, which is the upstream repository at the commit above (its build pins each library as a git submodule). Release packages should attach that source archive the way the FFmpeg source is attached.

`files/default.woff2` is Liberation Sans 2.00.5 under the SIL Open Font License 1.1 (`OFL-1.1.txt`). The package's `COPYRIGHT` does not list it; the font's own name table does.

## How the web app uses it

`vendoredFiles()` in `web/scripts/config.mjs` serves the `files/` folder unchanged under `/vendor/libass-wasm-4.2.4/` in development and copies them there in a build. A build also copies `LICENSE`, `COPYRIGHT` and `OFL-1.1.txt` into that folder, so the notices are distributed with the files. They are meant to be requested only when a viewer selects a styled track; the web player does not draw styled subtitles yet, so the server burns them in. The server's content security policy allows WebAssembly compilation (`'wasm-unsafe-eval'`) for this.

## Updating

Download the new archive into this folder, read its `package.json`, `LICENSE` and `COPYRIGHT` again, replace the four files, update `provenance.json` and the version in the served path (`web/scripts/config.mjs`), and run `verify.py`.
