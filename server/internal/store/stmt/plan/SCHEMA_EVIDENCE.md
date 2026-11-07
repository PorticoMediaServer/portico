# Statement plans and shared schema evidence

A statement golden asserts the manifest, cohort coverage, VM program and EQP.
A schema embed regenerates **one** shared file,
`testdata/schema-evidence.json.gz`, using the hosted `hn-schema-evidence` capture
target. Run `TestPlanSharedSchemaEvidence` with recording disabled afterwards.
The file contains the native table/column/index metadata and the entire FK
DELETE, UPDATE and parent-lookup audit, including native nodes and SEARCH paths.
A real FK-plan change is reviewed there once, rather than in each writer file.

`Capture` still inspects the actual caller-owned connection, checks its policy,
SQL grammar, sources, full keys, predicates, native VM and EQP, and mints the
same opaque, full-definition receipt. Nothing in JSON, comparison, or the shared
file can register or execute SQL. A canonical Capture now also proves that its
complete native catalogue matches the schema owner's embedded layout pin.
Custom-schema fixtures retain exact legacy comparison.

`Golden.Compare`, wrapped `CompareJSON`, and standalone FK `CompareExactJSON`
validate the current schema evidence before comparing statement plans. Equal
JSON is not a bypass. Reports still contain the complete actual environment;
it is not erased from evidence. Old complete goldens need no metadata rewrite.
The three old compact native-key fixtures now retain their full environment
for the same validation, without replacing their old plan arrays.

The following are checked as current schema evidence, rather than pinned in
each statement baseline:

- schema SQL/layout and index-policy identity, native declarations and roots;
- table/column/index audit metadata, including native index-list sequence;
- native statistics and their digest/profile and table/index membership;
- fixture source journal hash and implementation description (still recorded;
  native population assertions remain owned by the fixture);
- full physical table record widths in `OpenRead`, only when exactly equal to
  the named table's complete width. Generated/virtual declarations and decoder
  prefixes keep exact comparison.

Everything else stays in the statement comparison: SQL, ordered parameter and
cohort definitions, Hot/index/seek/waiver contracts, opcodes, registers, jumps,
flags, constants, named opened objects, access order, EQP nodes/parents/details,
conflict probes, forced-key identity, native FTS audits, engine/sourceID,
functions, collations and connection policy. The closed Linux/Darwin compile
profile equivalence is unchanged. A changed instruction, SEARCH/SCAN, index,
order or unknown operand still fails. This does not authorize statistics
manipulation, broaden grammar or make a performance/authority qualification.

Capture harnesses use `CheckSchemaForeignKeys`, not a copied graph count. Its
check includes every probe, not just cardinality. Their duplicate old FK files
may remain as historical artifacts; current FK plans must match the one shared
reference. New statement baselines and genuine plan changes still require the
normal reviewed capture. Domain semantic/measurement recipe pins are separate
and are not automatically requalified by this comparison.

For desk checks: review the one generated schema-evidence diff alongside DDL,
and review only statement-plan changes in owner goldens. A metadata-only schema
change requires no owner recapture or metadata-only PR hold. Preserve the raw
Capture report and actual source identity when a plan does change.
