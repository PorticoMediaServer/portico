# Personal filter SQL parity oracle (fixture only)

Status: the values-only personal-filter kernel replays all 95 frozen compiler
cases against sparse native personal rows, including another profile and unrated
versus rating zero. This establishes the tested scalar semantics only. It supplies
no native source reader, authority, publication or performance qualification.

## Provenance

`testdata/rb-personal-filter-parity.json.gz` was generated at base
`34f0b4f2b1254e621342d5e25ae8346000d7888c` from the existing public parser and
the existing private browse compiler in package `catalog`:

- `catalog.ParseBrowseQuery(raw, "query")`
- `(&catalog.browseCompiler{profile: "p", itemPivot: true, bounded: true, now: 2026-11-07T00:00:00Z}).compile(node)`

The temporary exporter was `server/internal/catalog/rb_personal_oracle_export_test.go`
with test `TestRBExportPersonalOracle`, run from `server`:

```sh
GOTOOLCHAIN=go1.26.10 go test ./internal/catalog -run '^TestRBExportPersonalOracle$' -count=1
```

The exporter existed only to call those existing parser/compiler entry points and
was removed before commit. No DB fixture, package suite, source change or
compiler change was used. `CompileBrowseSelection` and
`CompileCompactBrowseSelection` were deliberately not used: they refuse personal
fields on shared channels with `ErrBrowsePersonalField`. That refusal is real
shared-channel behavior, not permission to weaken any LC rule.

The `Where`, `Args` and ordering in the fixture are actual compiler output at
that base, not hand-authored expected SQL. Gzip is deterministic (mtime 0,
OS 255); record order is field, then atom, then wrapper order.

## Vocabulary and coverage

Exactly three personal fields on playable movie item pivots: `favorite`,
`watchlisted`, `personalRating`.

- Boolean atoms: `favorite` and `watchlisted` × `equals`/`not-equals` ×
  `true`/`false` = 8.
- Rating atoms: `equals`, `not-equals`, `less-than`, `at-most`,
  `greater-than`, `at-least` with value 5; `between` `[0,5]`; `in`/`not-in`
  `[0,5,10]`; `is-present`/`is-missing` with `null` = 11.
- Five wrappers per atom: bare predicate; `group not(predicate)`; double
  negation `group not(group not(predicate))`; `group all[predicate,not(predicate)]`;
  `group any[predicate,not(predicate)]`.

All public inputs parsed, giving 19 atoms × 5 wrappers = 95 records. Each record
is `{Query, Where, Args, Profile: "p", ItemPivot: true, Bounded: true}`. `Query`
is the valid published AST JSON, `Where` is the exact compiled SQL, and `Args`
is the exact compiler argument order, which always retains profile `"p"`.

## Replay constraints

Compiled predicates target `catalog_browse_rows` aliased `e`, so any replay must
supply that exact `e.entity_id` alias. Personal state is sparse: the fixture
freezes `personal_items` `EXISTS`/`NOT EXISTS` behavior, including absent rows,
and must not be densified or reinterpreted by adding default false/zero state.
Preserve the emitted `NOT`/`EXISTS` forms and argument order. The native replay supplies that alias only in the test oracle. The production
kernel takes already-proved values and cannot authorize a source or viewer.
