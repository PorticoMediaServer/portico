// Package plan is the schema-v1 qualification harness for static statements.
//
// Serving hot paths use forced indexes or an asserted native primary-key seek,
// exact INT partial predicates and equality-tail/next-key cursors. SQL lint uses
// a closed SELECT/DML grammar: column/parameter/literal comparisons, AND/OR,
// IS [NOT] NULL, scalar parameter/integer/string IN lists, BETWEEN, explicit
// known scalar calls (at most 16 levels), column collations and ORDER BY/LIMIT
// keysets. Unknown predicates, grouped expressions, CASE, windows, CTEs and
// subqueries are flagged; the lint never infers safety from arbitrary SQL.
// SELECT also accepts explicit INNER/LEFT/CROSS JOIN of named tables, explicit
// AS aliases and per-table INDEXED BY/NOT INDEXED. Every join requires ON with
// ANDed column=column/parameter/integer points only. Comma/bare/NATURAL/RIGHT/
// FULL joins, USING and grouped/table-function sources remain unsupported.
// Joined factors require AS aliases; joined identifiers must be unquoted and
// sources unqualified. All SELECT sources must resolve to ordinary MAIN base
// tables. LintSQLOn performs this live check (including TEMP shadow rejection);
// Capture always invokes it. SQL-only LintSQL cannot certify catalog identity.
// Views, virtual/shadow tables and unresolved sources grant no qualification.
// INSERT accepts one explicit column/parameter VALUES row, optional named
// ON CONFLICT(columns) DO NOTHING/DO UPDATE SET column=excluded.column|?,
// and plain-column RETURNING. UPDATE requires SET and WHERE, DELETE requires
// WHERE. SET accepts the scalar subset plus up to eight +/- scalar terms;
// WHERE uses the unchanged predicate grammar. UPDATE/DELETE may force INDEXED BY.
// Their WHERE seeks require expected index/PK contracts even if Hot is false.
// Generated sqlite_autoindex_* names are asserted in Plan.Indexes and actual
// EQP only; INDEXED BY must not embed them. Explicit names remain forced.
// INSERT destinations require Capture's index/column and full FK audits instead
// of a nonexistent destination SEARCH. UPSERT requires one expected full unique
// index or PK resolved unambiguously by the exact conflict column set. Partial
// targets and explicit target collations are outside the grammar. The probe
// retains the resolved key's collations and requires explicit bound INSERT
// inputs; its separately labelled EQP must SEARCH that key. Native INSERT EQP
// and canonical VM remain separate from this probe. DML column references are
// resolved against main.table_xinfo before preparing SQL. No registration
// confers accepting-command authority. Any user trigger in main, TEMP or an
// attached schema rejects qualification, including an unrelated trigger.
//
// Independently, SQLite's EQP rejects SUBQUERY, CO-ROUTINE, MATERIALIZE,
// MULTI-INDEX OR, USE TEMP B-TREE and automatic indexes for every statement,
// and scans on declared hot paths. Legacy AllowedScans grants no permission.
// An exceptional EQP form requires a registry waiver bound to the statement ID,
// form, exact detail and reason. Grammar failures are never waivable. Waivers
// are immutable manifest data for explicit PR approval; the current baseline
// inventory is empty.
//
// Each cohort retains the full EXPLAIN VM and exact EQP. Only Transaction's
// checked schema cookie is replaced with a placeholder, and literal b-tree
// roots become database-qualified object names from sqlite_schema. All other
// operands, registers, flags and jump targets remain exact. EQP replaces only
// SQLite's notused field with a placeholder; IDs, parents and detail stay exact.
// Unknown roots fail
// closed. Raw schema/root-page metadata remains in the environment evidence.
// Planner statistics do not permit a different serving access path.
//
// Fixtures are immutable during capture. Unanalyzed and analyzed populations
// have separate reviewed baselines, including exact stat1/stat4 content hashes,
// engine/source/compile options, schema/index/predicate bytes and relevant
// connection pragmas. Capture and FK audits require the declared shared
// sqliteconn policy and retain actual Assert settings (including TempStore)
// before examining plans; drift is rejected, never repaired. Capture never
// runs ANALYZE or optimize. Incidental planner
// maintenance is forbidden in foreground requests and accepting commands; any
// future governed statistics recipe requires its own qualification. SQL, schema,
// statistics, functions/collations or engine changes require reviewing the raw
// plan difference, semantic controls and affected scale measurements before a
// deliberate baseline update. A passing tiny plan does not prove bounded work
// or release latency at scale.
package plan
