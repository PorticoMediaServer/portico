# SQLite driver maintenance patch

Source: modernc.org/sqlite v1.58.0, https://gitlab.com/cznic/sqlite.
Upstream commit: `722282f38b49191a4e24569eeac960bc033bd8f0`.
Module checksum: `h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=`.
Only its handwritten top-level Go driver files are copied here. SQLite's generated library and virtual-table support remain the pinned upstream module imports. Original copyright and BSD license are preserved in each source file and LICENSE.

Portico additions:

- `context_progress.go`: scoped cancellation progress callbacks, including later row iteration, with reusable per-connection state and closure cleanup.
- `stmt.go`: recheck context after binding and before initial execution; preserve the query context in returned rows; expose retained VM allocations through SQLite's public `SQLITE_STMTSTATUS_MEMUSED` diagnostic.
- `rows.go`: restore that context during each `Next` call; report a statement's VM steps through SQLite's public `SQLITE_STMTSTATUS_VM_STEP` diagnostic, so tests can assert how much work a read does; harvest every statement's steps into a per-connection total when it is reset or finalized (`VMOperations`), which the write gate's per-transaction work budget reads.
- `conn.go`: own and release progress callback state; hold the VM step total and harvest it on finalize.
- `wal_io.go`: forward the source-reviewed default Linux unix VFS, observing WAL writes/truncates/closes without another OS descriptor. Fixed counters include attempted/failed/repeated writes and rollback spills. Other operating systems and explicit custom VFSes report unqualified. Native methods retain their original return codes and lock/SHM/sync behavior; the shim does not enforce a quota. State remains until every connection/WAL file closes, including SQLite zombie-close lifetimes.
- `tx.go`: scope transaction execution cancellation.
- `backup.go`, `conn.go`: a connection tracks the online backups running on it, and `Close` finishes them before `sqlite3_close_v2`. Upstream leaves a zombie handle whose next `Step` would run on freed per-connection state; here a `Step` or `Finish` after the source closed returns `ErrBackupClosed`. Portico's backup steps on the pooled writer connection across gate tickets, so the pool may close it between steps.

A canceled `sqlite3_interrupt` before VM activation can be cleared at activation. The additional progress callback checks the request's closed cancellation channel inside the VM every 1,000 instructions. It is removed/restored before another call uses the connection. Nothing caches results or authorizations.

The package keeps the `sqlite` database/sql registration name. Server imports must use this maintained package rather than also importing upstream's root driver (which would register the same name and introduce incompatible nominal error, backup and function-context types).

Statement memory accounting runs before retention and after execution resets and clears bindings. SQLite's per-statement allocation diagnostic works independently of disabled global memory statistics. Portico adds an allowance for its SQL copies and Go bookkeeping; other driver implementations retain a conservative estimate.

When upgrading, diff these files against the exact upstream tag, preserve the small cancellation and retained-memory patches and rerun driver, dbwork, backup, scalar/collation, race and ARMv6 checks. Include schema reprepare, variable memory growth and binding-clearance regressions. Upstream v1.60.1 was inspected during this work and still had the same bind-to-step cancellation gap.

`go vet` reports inherited `unsafe.Pointer` diagnostics in the upstream driver. Compare these with the exact upstream source when reviewing an upgrade; the WAL observer adds the same native-pointer conversions at its VFS/method-table boundary, covered by real-file, fault, lifetime and race tests. `go vet -unsafeptr=false` passes for this package.
