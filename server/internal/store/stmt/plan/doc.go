// Package plan is the schema-v1 qualification harness for static statements.
//
// Serving hot paths use forced indexes or an asserted native primary-key seek,
// exact INT partial predicates and equality-tail/next-key cursors. SQL lint uses
// a closed SELECT grammar: column/parameter/literal comparisons, AND/OR,
// IS [NOT] NULL, scalar parameter/integer/string IN lists, BETWEEN, explicit
// known scalar calls (at most 16 levels), column collations and ORDER BY/LIMIT
// keysets. Unknown predicates, grouped expressions, CASE, windows, CTEs and
// subqueries are flagged; the lint never infers safety from arbitrary SQL.
//
// Independently, SQLite's EQP rejects SUBQUERY, CO-ROUTINE, MATERIALIZE,
// MULTI-INDEX OR, USE TEMP B-TREE and automatic indexes for every statement,
// and scans on declared hot paths. Legacy AllowedScans grants no permission.
// An exceptional form requires a registry waiver bound to the statement ID,
// form, exact SQL (lint) or exact detail (EQP), and reason. Stages are independent;
// a lint waiver cannot bypass the native guard. Waivers are immutable manifest
// data for explicit PR approval; the current baseline inventory is empty.
//
// Each cohort retains the full EXPLAIN VM and EQP without removing opcodes,
// IDs or operands. Planner statistics are evidence, not permission for a
// different serving access path.
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
