# Schema v1

`SQL()` and `SHA256` expose the exact change-controlled bootstrap schema. `schema.sql`
matches canonical `planning/v3/schema-v1/phase0/SCHEMA.sql` byte for byte.
`Contracts()` pins the companion artifacts, `ReadContract(name)` returns their
bytes, and `SHA256SUMS` supports external harnesses. `INT-INDEX-POLICY.json` is
the shared partial-predicate authority. Contribution SQL is already included in
the full schema; do not execute the fragment separately.

INT owns this package and the canonical DDL. Every DDL or contract change must
update the canonical source and embedded copy together in the same reviewed
change, with the reason and R review. The DDL stays under change control until
the phase-1a go/no-go passes, then locks. Other store lanes consume these bytes
and predicates without editing them.

For a fresh harness file, open an exclusive bundled-SQLite connection, configure
its resource/WAL policy and `foreign_keys=ON` through DSN/connector settings on every physical connection, then call
`InitializeEmpty(ctx, conn)` before starting any runtime connection users. It
installs atomically and refuses every non-empty database. It opens no handles,
adds no seeds and performs no migration. Runtime write authority stays with the
private typed writer. This package is not yet wired into server startup.

For a restart, call `AssertExisting(ctx, conn)` on the exclusive opening
connection before starting runtime users. It requires FK-on and the pinned
bundled engine, then compares every native `main.sqlite_schema` object against
`NativeLayoutSHA256` and `NativeLayoutObjects`. Missing, modified, extra and
legacy layouts fail closed. It executes no bootstrap DDL, ANALYZE, migration,
repair or seed. It does not inspect user data, prove its integrity or authorize
access; those are separate startup responsibilities.

The fingerprint recipe is `portico-native-layout-sha256-v1` plus NUL, then
length-prefixed schema SHA and SQLite version, followed by every object sorted
by BINARY `(type,name,tbl_name)`. Each tuple contains those three length-prefixed
UTF-8 strings and SQL: byte 0 for NULL, or byte 1 and the length-prefixed native
SQL text. Lengths are unsigned 64-bit big endian byte counts. It includes native
autoindexes, FTS shadow objects and views; table contents and physical root-page
numbers are excluded. This proves the exact stored native layout, not the bytes
of an unavailable historical bootstrap script. New indexes, statistics tables,
bank declarations or engine changes require an INT-reviewed pin update. Runtime
code must not create unregistered layout objects. DDL/engine changes regenerate
the pins from a fresh canonical bootstrap; the restart and mutation controls
must pass on the new exact bytes. No version PRAGMA or compiled SQL hash alone
substitutes for this existing-file check.

INT-CHANGE-001 adds scan/search/enrichment operation receipts and acknowledgement
floors, per-image search seal/reclaim custody, exact own-play quarter/day/month
and exact playing-span edges, a bounded pending occurrence cursor, and six
admin-only event/cohort time access paths. It is adopted after R review.
Receipt result IDs do not pin retired mutable work resources;
private writer commands enforce immutable results, complete CAS and acknowledgement
ordering. The package initializer does not implement those commands. No performance pass follows from these storage contracts.

INT-CHANGE-002 is adopted at5f3c7f67 after independent R agreement. Credits
use staged effective images and a same-entity head, with verified small deltas.
Retained owner/source cohorts have exact incarnation/revision provenance;
metadata_repair_cascades owns single-target authored input and immutable command
receipts, and image headers own bounded retirement through final acknowledgment.
The provider100cast/50crew selector is not a manual-credit cap: preserve current
512explicit/2048normalized repair input and use complete bounded staged
preparation/pages. No provider re-trim of authored decisions is permitted.
The six artifacts and native-layout fingerprint move together; actual writer,
producer, crash/replay,1M/both-profile,quiet-max,2GB and R-09b gates remain open.
InventoryRefuse and PB witness/next-bank changes are separate later reviews.
