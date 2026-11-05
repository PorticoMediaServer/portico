// Package plan is the schema-v1 qualification harness for static statements.
//
// Serving hot paths use forced indexes or an asserted native primary-key seek,
// exact INT partial predicates and equality-tail/next-key cursors. Planner
// statistics are evidence, not permission for a different serving access path.
// Each registry cohort retains the full EXPLAIN VM and EQP without removing
// opcodes, IDs or operands. Missing goldens and unexpected scans, temporary
// B-trees, automatic indexes or tuple/subquery comparisons fail closed.
//
// Fixtures are immutable during capture. Unanalyzed and analyzed populations
// have separate reviewed baselines, including exact stat1/stat4 content hashes,
// engine/source/compile options, schema/index/predicate bytes and relevant
// connection pragmas. Capture never runs ANALYZE or optimize. Incidental planner
// maintenance is forbidden in foreground requests and accepting commands; any
// future governed statistics recipe requires its own qualification. SQL, schema,
// statistics, functions/collations or engine changes require reviewing the raw
// plan difference, semantic controls and affected scale measurements before a
// deliberate baseline update. A passing tiny plan does not prove bounded work
// or release latency at scale.
package plan
