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


INT-CHANGE-001 adds scan/search/enrichment operation receipts and acknowledgement
floors, per-image search seal/reclaim custody, exact own-play quarter/day/month
and exact playing-span edges, a bounded pending occurrence cursor, and six
admin-only event/cohort time access paths. This candidate
requires R review. Receipt result IDs do not pin retired mutable work resources;
private writer commands enforce immutable results, complete CAS and acknowledgement
ordering. The package initializer does not implement those commands. Effective
credit images are a separate INT-CHANGE-002 change; no performance pass follows.
