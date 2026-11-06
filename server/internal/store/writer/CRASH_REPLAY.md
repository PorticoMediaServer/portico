# Process crash and receipt recovery controls

On Linux, `TestCrashReplayEffective` and `TestCrashReplayInventoryHistory` run
the actual merged commands in separate test processes against independent
canonical database files. The parent receives a versioned boundary witness
over a private pipe, sends SIGKILL, joins the child, and reopens its file through
the shared connection policy and `schema.AssertExisting`. EOF, timeout,
unreached boundaries and ordinary exits fail the case. Killed templates,
databases, WAL and spill remain within parent-owned temporary directories.
Both `TMPDIR` and Go's higher-priority `GOTMPDIR` are rebound to each child's
parent-owned scratch; the containment assertion remains mandatory.

The witness binds the original receipt identity/request digest, command,
boundary, engine and schema. The fixture source facts and clock are fixed;
generated image IDs are checked against their exact durable receipt/attachment.
The operation-journal model derives expected committed/ACK state from the
declared native boundary independently of the production receipt classifier.
Inventory freezes every raw receipt-header column and ordered child row from
the original parent's fixture before any tested command. After reopen it
compares the complete raw saved rows against that journal, even below the ACK
floor, then independently projects the expected API result from raw columns.
`ResolveScan` is only an operation under test; its output never seeds expected
values. Retired output must have every payload field zero, with only its
Retired state/original reference and actual per-call evidence remaining.
Restart binding grants only current disclosure and identity-only Resolve/ACK;
it neither mints nor copies fresh-source fixture permission. After a proven
receipt miss, a fresh Stage retry must return zero output and Unqualified,
preserving zero effects. Executing that retry remains gated on W-owned reviewed
fixture-source rebinding.

| Command | SIGKILL boundaries | Cases |
| --- | --- | ---: |
| EffectiveStage BEGIN | Before/after BEGIN, image insert, owner attach, receipt insert, before/after COMMIT, connection return, API return; lost COMMIT reply before/after native commit | 11 |
| EffectiveStage Resolve | Before/after BEGIN, receipt read, before/after COMMIT, connection return, API return; both lost-reply controls | 9 |
| EffectiveStage ACK | Resolve boundaries plus ACK-floor write; both lost-reply controls | 10 |
| Governed PASSIVE | Before native call, after native rows close, after complete Step return | 3 |
| Inventory Resolve | Before/after BEGIN, receipt read, before/after COMMIT, connection return, API return; both lost-reply controls | 9 |
| Inventory ACK | Resolve boundaries plus ACK-floor write; both lost-reply controls | 10 |

After every restart the controls require clean `integrity_check` and
`foreign_key_check`, retained exact receipts and ordered result cohorts, atomic
owner/effect/floor state, no duplicate effects, saved-result replay under the
same original identity, current disclosure, and idempotent ACK. Effective BEGIN
also preserves serving/source witnesses and its hidden unsealed image. Missing
uncommitted proof stays Pending; committed lost replies return zero output and
ResolveRequired before the kill. Neither an error nor response loss is treated
as rollback. ACK retains physical receipts/children and makes replay Retired.

The fixtures use synthetic resource forecasts, virtual time and explicitly
test-only source permission. The PASSIVE composition separately funds its
reader; the writer-only first-light profile is not a checkpoint profile. Native
SQL and restart IO are real; elapsed time is not measured qualification.
All helpers live in `_linux_test.go`; there are no production hooks or exports.

V's source-journal approach is applied to these implemented receipt semantics.
Its broader leaf browse/chronology/keyset controls are separate existing tests;
the full domain/API/authority/reducer/search reference evaluator remains open.
Fresh InventoryAccept has no accepting body on this baseline; publisher facade,
final enrichment, security and progress accepting commands also remain outside
this matrix. These are explicit coverage gaps, not skipped passing cases. Add
their body/state boundaries when their reviewed implementations land.

This proves process-crash recovery for the named small fixtures on the bundled
engine. It does not simulate a crash inside a native statement/fsync, power
loss, torn writes, or qualify latency, WAL bounds/reset, memory, scale or runtime
cutover.

Run the focused controls with concurrency two and a disk-backed scratch root:

```sh
GOMAXPROCS=2 go -C server test -p 2 -parallel 2 \
  -run '^TestCrash(ReplayEffective|ReplayInventoryHistory|OracleRejectsReceiptEffectMutants|InventoryOracleRejects.*)$' \
  ./internal/store/writer
```

Set `TMPDIR` to the job's owned disk scratch before running. Full and race
suites belong in CI. Each failing case names its command/boundary/fault so the
same immutable fixture and journal can reproduce the first mismatch.
