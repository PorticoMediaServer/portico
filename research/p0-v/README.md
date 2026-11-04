# P0-V temporary correctness research

Never merge this branch. Product source, contracts, goldens and runtime behavior stay unchanged. Only this directory and a research-branch-only workflow are added. The Go overlay places a read-only export test beside existing HTTP fixture tests; all changed fixture bytes live in caller-owned disk scratch. The independent Python evaluator imports no server code and reads no derived tables.

Run `python3 research/p0-v/run.py --out /absolute/disk/scratch` (under portico-heavy on mini-1), then `python3 research/p0-v/checks.py /absolute/disk/scratch/export.json --plans research/p0-v/plans.json`. `--shape smoke` is 1,308 playable source observations, 1,426 catalogue entities (books can have multiple files). Plan goldens qualify the Tiny fixture only, including bundled SQLite source ID, compile options, schema hash, planner pragmas and statistics. Missing baselines fail; explicit first recording uses `--record-plan`. Never re-record to make a changed server pass.

The prototype covers movies/episodes/songs, title/year/added/duration in both directions, complete seven-row cursor chains, owner/open/restricted library/rating/label visibility, movie genre/label/content-rating facets and paged own-title movie search membership. Aggregate reducers, provider precedence, translated article rules, contextual search and full ranking are build gates, not supported semantics. Reference.visible models the fixture's profile restrictions, not the entire authentication protocol.

`scale.py --size N` measures reference feasibility and internal consistency on a deliberately leaf-only fixture; it is not the realistic mixed S/Q corpus or a comparison with a new store. No 100k+ local runs. Run the workflow on GitHub-hosted Ubuntu for 100k/400k/1M. Record exact head/run IDs and measurements separately from pass/fail claims.

`ledger.py G-ROUTE-USAGE.csv /output` preserves the private route usage metadata and assigns every exact current OpenAPI operation to a recorded golden or named domain oracle. Run it with the internal input; its output belongs in the internal planning repository. Exact test-literal locations are review hints, never execution coverage proof. There are no inferred unused-route waivers.

Plan control: set `P0V_PLAN_DROP_INDEX=1` for a separate overlay export. It drops catalog_term_sources_term only in the disposable fixture. The ordinary checked-in plan comparison must reject that export. Also change an observed response ID, count, order or membership and require the independent comparison to fail.

Delete all caller-owned fixture and build scratch when done. Keep only compact results, immutable source pins and reviewed plan baselines. The final production statement registry and schema are owned by INT/S/Q; this five-statement oracle-export registry is not that future registry.

CHRONO-LEAF-INSTANT-ORDER is an accepted deliberate change: newest-first means true instant descending, then ID descending. chronology.json is the reviewed new-order golden; model.instant_ns parses variable/fixed-width UTC without truncating nanoseconds. TestP0VDVRLegacyOrderControl documents the old server's same-second exact-versus-fractional and fractional-prefix (.1Z/.11Z) TEXT anomalies through the DVR publication writer seam and real one-row HTTP continuations. That legacy control is not the release oracle.
