# Reading MOBI/AZW3 conversion — proposed packaging, NOT approved or enabled

Dependency: libmobi v0.12, exact upstream commit
`85dcfe803fc2a21020ddcf15c3eb66b93d388add`.
Canonical source: https://github.com/bfabiszewski/libmobi/tree/85dcfe803fc2a21020ddcf15c3eb66b93d388add
Licence: LGPL-3.0-or-later (upstream COPYING); preserve upstream notices and
provide matching source, modifications and relinking obligations. Release must
review the complete static-link distribution obligations before delivery.

The real upstream `tools/mobitool.c` EPUB writer is used, not a relabel or new
engine. Build its main as `portico_mobitool_main`, link worker.c and the pinned
library, with EPUB XML writer enabled and encryption support DISABLED. Do not
build/package mobidrm, encryption/key modules or CLI key/serial options. The
worker independently refuses encrypted and Print Replica parts, including both
hybrid parts. No downloaded/system-PATH converter is admitted.

Initial proposed runtime target: Linux server, static worker, exact SHA-256
qualified in the Release bundle; Linux bubblewrap exact executable hash also
qualified. No Linux sandbox means unavailable, never an unsandboxed fallback.
Other server platforms remain unsupported until an explicitly reviewed bounded
platform implementation exists. This does not add a native guest binary.

The sandbox has no network, library root, credentials, home or persistent output.
It sees only the exact staged input inode, static worker and private 128MiB
output tmpfs. Root and device mounts are explicitly remounted read-only; only the bounded output tmpfs is writable. Worker sets address-space768MiB, CPU30s, output-file128MiB,
no-core/no-child and fd32 limits before parsing. Server wall deadline45s,
input64MiB, output128MiB and one active conversion bound are independent.
Only complete stdout EPUB bytes return. Conversion failure never publishes a
partial package. Existing EPUB ZIP/resource/sanitization limits still apply.

Release review needed: upstream licence and transitive miniz/XML writer closure,
source/relink delivery, pinned reproducible recipe and hashes, static worker
closure, required bubblewrap options/user namespace posture, actual artifact
packaging/resolver and supported server targets. This document grants no build,
install, checkpoint or tooling approval. No generated or bundled binary is
present in this source WIP.

Concrete source recipe and complete linked-source pins are now in inputs.json, CMakeLists.txt, build-policy.h, build.py and REBUILD.md. libxml2 and external zlib are explicitly excluded; internal XML writer + bundled miniz + pinned musl form the intended worker closure. Actual builder/artifact qualification remains Release-owned and open.
