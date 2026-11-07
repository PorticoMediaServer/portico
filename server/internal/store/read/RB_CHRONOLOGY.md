# Date-added and recent-content source streams

`ChronologyWindow` is a harness-only, values-only foreground source reader. Its
DateAdded order uses fixed accepted `added_at`. ContentAdded selects monotonic
`last_content_added_at` for shows/seasons; other kinds retain their own accepted
added chronology. It supplies ordered source candidates for later Home/top-K
composition, not a completed Home row or show-to-episode target projection.

The canonical indexes include nullable dates. Missing dates form the undated
bucket (before dates ascending, after dates descending), with exact ID ties.
Rather than coalescing the indexed column or using row tuples/OR, the24 closed
statements select disjoint NULL equality tails, non-NULL equality tails and
next-key ranges. A descending dated continuation needs at most three seeks,
including its eventual undated suffix. Every fetched/rejected row consumes the
same64-position raw bound; pages hold at most40 values. Actual captures pin
SEARCH-only plans in both statistics profiles and verify present/empty cohorts.

Compose `RBChronologyDefinitions` with an actual Reader Capture receipt and use
`ConsumeForeground` for LIVE/output custody. Initial and fresh final fences,
exact private query-bound cursors, original deadlines and all zero-output denial
rules are inherited. No shared read file, schema or product route changes.

First pages use11 statements over two windows. Nullable continuations use up to
13 statements over two windows (at most8 in the first window including its five
fence reads). This exceeds B-READS' proposed10-statement browse ceiling and is
explicit unqualified debt. Plan/functional controls do not establish1M latency,
VM, paired foreground, maximum-body or2GB-memory qualification.

Existing fail-closed policy defaults remain. Restricted-container chronology
requires the separate eligible-child proof before Home composition can claim
support; global container timestamps alone never prove visible child arrivals.
Home last100-work caps, multi-library exact top-K merge, target selection and
ready cache composition remain subsequent work. Complete Reading/author kinds
use the shared policy-aware seam only after that owner's change merges.
