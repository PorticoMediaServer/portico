# Canonical test databases

Use this package from tests that need the empty canonical schema, then add each
test's own seed to its independent copy:

```go
func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

func databasePath(t *testing.T) string {
    return schematest.Clone(t)
}
```

`Run` lazily creates one template per test process through the shared SQLite
opener, checks its connection policy and canonical layout, and closes every
handle. Publication refuses either WAL or SHM sidecar. `Clone` copies into a new
file under the requesting test's temporary directory, using a 64 KiB buffer and
verifying the complete size and digest. No writable handle or template path is
exported. Open each copy through the normal shared opener/connector and retain
the usual acquisition assertions, including DQS and replacement policy.

The process template lives under the job's `TMPDIR`. `Run` removes it before
returning, independently of individual test cleanup, and reports schema, engine,
file size, hash and bootstrap/copy counts. Tests that never call `Clone` create
no template. There is no persistent cache or cross-process shared database.

Keep tests of actual fresh bootstrap, old-layout refusal and policy failures on
their original direct paths. This helper does not replace those controls or
qualify production startup, writer hold time, WAL work or memory limits.
