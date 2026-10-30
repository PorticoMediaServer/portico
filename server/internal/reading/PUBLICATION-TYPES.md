# Explicit publication types

Root ruling 20 defines three descriptive values: `book`, `magazine`, and `newspaper`. This classification does not change the ebook/comic entity kind or the library kind.

Recognized automatic fields in classifier version 1:

- EPUB: `dc:type` children in the package metadata, with the exact Dublin Core namespace `http://purl.org/dc/elements/1.1/`.
- CBZ: a single direct scalar `Format` element in the unnamespaced `ComicInfo` root of the admitted `ComicInfo.xml` member.

Each value is trimmed and compared using ASCII case folding with the three exact semantic names. Conflicting recognized EPUB values make the type unknown. Multiple ComicInfo Format elements withhold classification. These are Portico interpretations, not a standardized three-value vocabulary; binding/presentation values such as HC, TPB, Web and Digital imply nothing. Generic `periodical` or `serial`, subjects, MIME, file extension, titles, folders, issue dates and cadence supply no type. PDF explicit-type extraction is not yet supported, so its parser supplies no automatic type.

The original format, field key and raw values are retained with the source revision and classifier version after real source inspection. Bounded incomplete evidence withholds classification. An accepted inspection without a supported type retires the previous automatic type for that source; unknown evidence is stored without a positive classification. Rule-version mismatches are excluded from accepted facts; the ready projection carries the rule version and refuses mismatches immediately. The existing bounded catalogue worker invalidates and rebuilds mismatched libraries. Effective type and source are copied into the same immutable ready members as author and series. EPUB package and metadata elements must use the OPF namespace. The file fact is adopted only after real source inspection. Its write transaction rechecks the original principal, item, source, inventory object and revision. The accepted automatic fact comes only from the current canonical primary publication and current inventory/root binding. Unsupported formats do not clear facts written by a later supported extractor.

An explicit locked owner value uses the existing metadata edit path and takes precedence. Empty, automatic or unlocked owner edits restore the current file fact or absence. An unknown type never creates a stored Book default, and a file inspection never overwrites the owner row.

This producer does not infer periodical groups or dates and does not make an unclassified ebook disappear from the base Books view. Periodical tabs, counts and groups require their own complete ready-snapshot producers. Official API/schema metadata generation belongs to the Release checkpoint batch.
