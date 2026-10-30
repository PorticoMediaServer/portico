# Complete visible series summary

The existing signed series page optionally publishes a summary of that exact canonical eligible series: actual library-scoped series ID, accepted label, whole-visible-member count and the original profile's read count. The count uses the same personal complete-state expression as Discover series. It never derives completion from a resident page or a progress fraction.

The summary query reuses the current series page's original transaction, principal-derived SeriesScope, immutable ready generation, current canonical-primary publication and Visible predicate. It runs after existing cursor/anchor validation but before page seek/LIMIT. Labels retain owner spelling priority, frequency and binary ties. Hidden members participate in neither names nor counts.

Author is optional and conservative: every eligible member must have an identical accepted author value and provenance. Missing or mixed values withhold the author rather than sampling the loaded page. Existing Reading author provenance is reused; this creates no author/person identity.

Cursor framing, scope, original expiry, ordering, source/reading-session admission and all playback/progress mutations are unchanged. This adds no query, route, DB schema, job or background authority. Official response generation belongs to Release16 checkpoints.
