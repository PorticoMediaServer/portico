# Reading Home wire fixture policy

`home-reading-wire.test.ts` runs in the recursive client-core inventory only when
`PORTICO_READING_HOME_FIXTURE_DIR` is set (CI's client job records it from Go first);
otherwise the runner says it was not run. When run, it requires `PORTICO_READING_HOME_FIXTURE_DIR` pointing to a directory containing
`book.json` and `comic.json` produced by the real Go HTTP test at the same revision.
Missing configuration, missing files and invalid responses fail the test; the
runner does not skip it or substitute hand-written JSON.

When Go fixture generation is authorized, create a disposable directory, export
its absolute path as `PORTICO_READING_HOME_FIXTURE_DIR`, then run from `server/`:

```sh
go test -p 4 ./internal/httpapi -run '^TestHomeReadingSavedDeferredHTTP$' -count=1
```

From `packages/client-core/`, run `npm test` with that same environment to include
the producer-consumer check, or run only the consumer:

```sh
node --experimental-transform-types --test test/final-batch/home-reading-wire.test.ts
```

Keep the directory outside version control and remove it after the run. The Go
producer's own assertions also run without fixture output when the variable is
unset; that does not qualify the TypeScript consumer.
