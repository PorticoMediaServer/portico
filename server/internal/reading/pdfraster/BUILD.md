# Bounded PDF page raster worker

This is the source of the worker that Portico's Reading PDF cover/page-thumbnail
path will run. It is deliberately not a general PDF tool: one page, one PDFium
thread, one process per render, fixed argv, bounded input/output, and no
JavaScript, XFA, annotations, actions, network or external file callbacks.

The Go custody is the existing MOBI bundle pattern
(`reading.LoadMobiBundle`/`MobiConverter`): a private absolute directory, a
manifest digest supplied out of band, verified worker/sandbox bytes, private
scratch, `bwrap` sandbox, supervisor-owned completion and physical retirement.
No new scheduler or execution framework is introduced, and the server never
searches `PATH`, downloads, installs or probes a PDF tool at runtime.

## Exact artifact pin

- Repository: <https://pdfium.googlesource.com/pdfium>
- Commit: `7b3f6f93625e6df97a7d9e677137a558df8648c4`
- Tree: `b4026fdde2ec2b18de5efb188330f9fe120777be`
- API source checked at that tree: `public/fpdfview.h` (`FPDF_InitLibraryWithConfig`
  version 2, `FPDF_LoadMemDocument64`, `FPDF_GetPageCount`, `FPDF_LoadPage`,
  `FPDF_GetPageWidthF`/`FPDF_GetPageHeightF`, `FPDFBitmap_CreateEx`,
  `FPDFBitmap_GetStride`, `FPDF_RenderPageBitmap`, `FPDFBitmap_Destroy`).
- License source checked at that tree: `LICENSE`.

The manifest records the exact commit. Release must not float this pin.

## Build (Release-owned runner/tooling)

Run in a Release-published build image whose digest is recorded in the bundle
receipt. The pinned base image is
`debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8`;
the final build image keeps its own digest. `depot_tools` is pinned at
`b52fe87d04af61df3dd07748ff2c2c0b649dca09`; `bubblewrap` is built from
`containers/bubblewrap` tag `v0.11.0`
(`a871b148b7bc0571f50b917cd5fd03b427f54ed1`) as a static sandbox, and every
linked code component and notice is recorded in the receipt. Copy the worker target
and the root group **before** `gn gen`, so the generated Ninja graph contains
the label:

```sh
gclient config --unmanaged https://pdfium.googlesource.com/pdfium.git
gclient sync --revision pdfium@7b3f6f93625e6df97a7d9e677137a558df8648c4
cd pdfium

# 1. Add the worker BUILD.gn + worker.c (from this directory).
mkdir -p samples/pdfraster
cp <portico>/server/internal/reading/pdfraster/BUILD.gn samples/pdfraster/
cp <portico>/server/internal/reading/pdfraster/worker.c samples/pdfraster/

# 2. Make the target reachable from the root graph before generation.
cat >> BUILD.gn <<'GN'

group("pdf_raster_worker") {
  deps = [ "//samples/pdfraster:pdf_raster_worker" ]
}
GN

# 3. Generate, prove the label exists, then build it.
gn gen out/portico --args='
  is_debug=false
  is_component_build=false
  pdf_is_standalone=true
  pdf_enable_v8=false
  pdf_enable_xfa=false
  pdf_use_partition_alloc=false
  use_custom_libcxx=true
  symbol_level=0
  target_cpu="x64"
'
gn ls out/portico //samples/pdfraster:pdf_raster_worker
ninja -C out/portico pdf_raster_worker
```

`BUILD.gn` here is the concrete target to copy into the checkout; Release
verifies its deps/config against `testing/pdfium_test/BUILD.gn`. The target
links `-static`; the worker must be a static ELF with no `PT_INTERP` and no
`DT_NEEDED`, because the bwrap root has no loader or dependency closure. The
adapter refuses a dynamic worker rather than calling it capable; an explicit
verified dependency closure is not implemented. The exact compiler, container
image and dependency versions are recorded in the bundle build log. The worker
is built by the same pinned checkout and container; no prebuilt or PATH tool
enters the bundle.

No `PATH` tool, download or runtime probe is part of the bundle. The delivered
artifact is the worker plus `bwrap` from the same build container.

## Bundle layout

```text
<absolute private dir>/
  manifest.json
  worker
  bwrap
  licenses/PDFIUM.txt
  licenses/PDFIUM-THIRD-PARTY-NOTICES.txt
```

`manifest.json` (schema 1) is documented by `manifest.example.json`. The
directory and files are owner/group private and not group/world writable, as
`LoadMobiBundle` already requires. The server receives:

- `PORTICO_READING_PDF_RASTER_BUNDLE` — absolute bundle directory;
- `PORTICO_READING_PDF_RASTER_MANIFEST_SHA256` — SHA-256 of `manifest.json`.

Both absent means the capability is off. Either present without the other, an
unverified digest, a wrong renderer pin/options, or a platform without the
required resource limits means the capability is off. No adapter alone enables
the feature, and no render success is claimed until a Release checkpoint renders
a real fixture.

## Worker contract

- argv: exactly `worker <pageIndex> <widthPx>`; both unsigned decimal, page
  checked against the real document, width `1..2048`.
- stdin: the complete admitted local PDF bytes, at most 128 MiB.
- stdout (success only, little endian): 32-byte header `"PDR1"`, width, height,
  stride, format (`3` = `FPDFBitmap_BGRx`), payload byte count, four zero bytes;
  then `height * stride` BGRx bytes, top line first.
- exit `0` success, `3` determined unavailable (not a plain PDF, encrypted,
  unsupported page/size), `4` retryable/limit failure. Diagnostics only on
  stderr.

## Bounds checked before allocation

- hard `RLIMIT_AS` (1 GiB), `RLIMIT_CPU` (30 s hard 35 s) and zero core are
  installed before input or parser allocation;
- total adapter budget: 90 s covering slot wait, staging, child run, header
  decode, final source recheck and PNG encoding;
- input bytes: 128 MiB, with an exact-bound one-byte EOF probe;
- page index: `0 <= page < FPDF_GetPageCount`;
- width: `1..2048`, height from the page aspect ratio `1..8192`;
- pixels: at most 8,388,608;
- stride: `width * 4` rounded to 4 bytes, checked against `INT_MAX`;
- raw payload: at most 64 MiB, checked before `malloc`;
- `FPDFBitmap_CreateEx` uses a caller-owned `calloc` buffer, so every outgoing
  byte starts defined; the BGRx unused byte is explicitly zeroed after render,
  and a failed `FPDFBitmap_FillRect` emits no frame; the returned stride must
  equal the allocated stride;
- argument, float, multiplication and size conversions are range-checked.

## Sandbox and failure policy

- `bwrap --unshare-all --die-with-parent --new-session --clearenv
  --cap-drop ALL` with an **empty guest root** (`--tmpfs /`): no host `/`,
  `/usr`, loader or dependency tree is bound. Only the static worker fd and
  `/dev` exist inside the sandbox; input arrives on stdin and output leaves on
  stdout. No network;
- no host fonts or `/usr/share/fonts` are visible, so a PDF that relies on
  non-embedded system fonts may render missing glyphs. That is a stated renderer
  limit, never a guessed or substituted image; a bundled font closure can be
  added later as its own reviewed artifact;
- one page per process; PDFium is documented as single-thread API use, so no
  parallel PDFium calls exist in this worker;
- no `FPDF_LoadXFA`, no XFA/JavaScript build, no annotation/widget flags, no
  external-file callbacks, no password guessing;
- any failure produces no image: the caller keeps the generated client cover or
  no thumbnail. Never a placeholder or guessed page.

## License and notices

PDFium's `LICENSE` at the pinned tree contains the BSD-style text **and** the
Apache License 2.0 text, and PDFium bundles third-party code under its own
notices. `bubblewrap` is LGPL-2.0-or-later; a libcap-linked sandbox adds
libcap's notice. The build inventory must cover **bwrap and every linked code
component**, not only PDFium. The bundle ships the unmodified `LICENSE` texts
and the generated third-party notice files under `licenses/`; do not describe
the artifact as BSD-3 only.

`bundle-receipt.example.json` is the receipt contract: build image digest,
depot_tools commit, PDFium commit/tree, bubblewrap tag/commit/static build,
worker and sandbox SHA-256, the exact license files and the linked-code
inventory. Release fills the actual image digest and hashes; the example names
the concrete pinned inputs above.

## Review split

This directory is source and build documentation only. The worker build script,
CI/runner wiring, artifact publication and the MOBI-style Go loader are
Release-owned tooling changes and need their own reviewable diff; the Reading
adapter that consumes the bundle stays in the reading package and stays dark
until the artifact exists.
