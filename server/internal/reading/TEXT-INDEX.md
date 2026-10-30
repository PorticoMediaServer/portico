# Persisted Reading text index

The Reading text index is EPUB-first private derived text. It exists so Books
search can answer across a library without rescanning a publication per query,
and so the reader's own local search remains a fallback rather than the only
producer. Nothing here is a catalogue card, a cover, a title or a client-visible
body field.

## Producer

`PrepareTextIndex` runs in the existing `TextCountPreparation` worker step under
the same metadata-refresh admission, wake tables, bounded window, cursor and
rest as the text count. There is no second scheduler, queue table or polling
loop. The candidate window selects at most one bounded window of canonical
publications in item order and returns "already determined for this observation
and rule" as a column, so a library whose documents are all
indexed/evicted/excluded/unsupported is examined in bounded windows rather than
walked in one statement looking for pending rows. The cursor advances over every
examined candidate, extraction opens only the undetermined ones, a clean
completed pass waits for the next catalog/source wake, and a transient failure
is retried after the pass rest.

Only a local book EPUB in the canonical primary publication position is
eligible. The producer resolves and rechecks the same exact
item/asset/inventory-object/revision tuple as the text count, opens the source
through the registered background authority, and then reads only admitted
`application/xhtml+xml` spine members through the existing complete text walker
(`countXHTMLTextObserved`), which excludes head/style/script content and applies
the same block boundaries as the word counter. A script that the walker cannot
segment makes the document unsupported for this producer rather than a partial
or estimated index.

The stored text follows one closed extraction contract per section
(`xhtml-body-blockspace-fields-v1`): only admitted spine XHTML body character
data is used; head content and script/style/template subtrees are excluded; one
ASCII space is inserted at the start and the end of every exact `textBlock`
element (address, article, aside, blockquote, br, dd, div, dl, dt, figcaption,
figure, footer, h1, h2, h3, h4, h5, h6, header, hr, li, main, nav, ol, p, pre,
section, table, td, th, tr, ul — the same list as `textBlock` in `text_count.go`), so block
siblings do not concatenate and inline boundaries do concatenate; finally every
whitespace run is collapsed to one ASCII space and both edges are trimmed with
`strings.Fields`. This is deliberately not DOM `textContent` and no raw-DOM
UTF-16 offset is claimed.

Offsets, excerpts and hit ranges are expressed against that normalized text; a
client that re-anchors a hit must reproduce the same extraction/mapping on the
opened exact source, verify `sourceRevision`, and validate the quote/range
before jumping — otherwise it declines the jump. `SectionIndex` is the
document's position in the publication spine, so textless spine documents leave
gaps rather than shifting later sections; `SectionPath` is the stable anchor
identity.

## Bounds and outcomes

- At most 8 MiB of derived text per document, at most 4096 indexed sections, at
  most 64 MiB of admitted spine source read per document.
- At most 1 GiB of derived text payload across all indexed documents. This is a
  logical payload budget, not a measured SQLite or FTS physical-disk cap. One
  `reading_text_index_budget` row holds the exact total, maintained by state
  triggers on insert, update and delete in the same transaction; no adoption
  scans the whole catalogue to add a document, and the budget CHECK aborts a
  transaction that would cache a wrong total.
- A document is indexed whole or not at all. `reading_text_index_state` records
  exactly one determined outcome for the exact observation:
  - `indexed`: the complete private text is present; `text_bytes` is the sum of
    the section text bytes and drives accounting.
  - `evicted`: a previously indexed document that was evicted for capacity. It
    keeps zero payload and its exact observation, so the background pass does
    not reindex it. Only a changed source/revision or rule (or an explicit owner
    reread) makes it pending again.
  - `excluded`: the document could not be admitted to the payload budget. Zero
    payload, exact observation, no retry loop.
  - `unsupported`: a determined refusal (malformed/oversized/textless/script)
    with a bounded `reason`. Zero payload. A session search with no exact state
    distinguishes `unsupported/source` (the retained observation is not on a
    local source) and `unsupported/version` (it is not the canonical-primary
    book EPUB the producer is scheduled for), so `pending` only ever means a
    selection inside the actual producer domain. An existing determined state is
    preserved even when the observation has since moved outside that domain.
- Transient acquisition, cancellation, timeout or source-change failures write
  no state row and are retried after the pass rest.

## Eviction

When a new document does not fit, the accepting transaction reads the single
budget row and the document's own old payload, then reads at most 64
whole-document eviction candidates in `indexed_at` FIFO order (ties by
item/asset). It evicts only if the candidates' measured payload bytes cover the
shortfall; each eviction's state update returns its bytes to the budget through
the trigger, and its section deletions remove the matching postings through the
section trigger. If the bounded candidate window cannot cover the shortfall,
every existing index is kept and the new document is recorded as `excluded`.
Nothing is evicted per section, no delete is unbounded, and no read path updates
`indexed_at` or performs maintenance.

## Deletion, cascades and postings

Publication and inventory-object deletion cascade into
`reading_text_index_state`, and foreign-key actions fire the state delete trigger
exactly like an ordinary DELETE (`recursive_triggers` governs recursive trigger
programs, not foreign-key actions). That trigger removes the document's sections
first — their `AFTER DELETE` trigger removes each exact external-content posting
— then returns the payload and bumps the revision. An explicit state deletion
(owner reread) takes the same path, and an update uses the same accounting
trigger. The section trigger is the only posting-deletion path, so no statement
double-deletes a posting, and no cascade can orphan private derived text or a
posting.

## Index revision and cursors

`reading_text_index_epoch` holds one global, monotonically moving revision. Every
accepting index transaction bumps it. A search cursor carries that revision, a
hash of the literal query and a hash of the caller's opaque viewer/scope fence;
any mismatch is refused instead of continuing over changed hits. The cursor also
carries the section rowid and the exact next rune position, so a page resumes
deterministically in `(section rowid, rune position)` order.

Source, primary-version and viewer changes are enforced on every page by the
exact observation/canonical-primary joins and by the mandatory authorization
callback, so no stale excerpt is served and no unauthorized hit is emitted even
when the index revision has not moved. A caller that also needs pagination to
refuse on a catalog change puts that revision in `Fence`; the cursor is then
refused instead of merging two scopes.

## Query seam

`Service.SearchTextIndex` is the private producer used by session and library
search. It requires the service visibility function as a mandatory
same-snapshot SQL scope over `s.item_id`, applied before `ORDER BY`/`LIMIT`, so a
hidden or restricted match cannot influence a page, a continuation or the work
accounting. A session caller pins that predicate to its one actual item; a global
caller supplies its actual authorized ready membership. It also requires a
per-item authorisation callback as defence in depth, called in the same snapshot
before any excerpt is returned. There is no raw-SQL input and no public handler
accepts SQL. It also requires a non-empty, valid UTF-8 `Fence` of at most 512
bytes: the caller supplies its current viewer, restriction, library and
session/catalog revision in that token and must change it whenever any of them
changes. The query must be valid UTF-8, trimmed, at most 1024 UTF-16 units,
and is treated literally: one token matches a whole Unicode word and several
tokens match a contiguous literal phrase. The FTS phrase only selects candidate
sections; every candidate is re-checked against the stored text, and excerpts,
UTF-16 offsets and match ranges come from that re-check, never from FTS token
offsets.

One request also has a work budget: at most 16 MiB of re-scanned section text,
2 seconds and 8 million examined positions, checked inside the position loop even
when a section produces no literal hit, plus the caller context. Spans are
produced incrementally, so no request materialises a multi-million-span slice.
When a page stops, the continuation carries the exact next unexamined rune
position in the current section, or the last fully consumed section. The next
page resumes there without rescanning the prefix, dropping a match or repeating
one. UTF-16 offsets are counted incrementally in match order against the stored
runes, with context checks; no hit rebuilds or re-scans its whole prefix.

Hits join the exact current observation (`inventory_object_id` and
`source_revision` must still be live and rooted), the canonical primary
publication and the publication's own `library_sources` row, with
`src.library_id` bound to that item's public library, `src.kind='local'`,
`e.kind=20` and `p.format='epub'`; an object with the same asset token in another
library cannot prove this publication. They require `outcome='indexed'`. A stale observation, an evicted document or a changed epoch
yields no hit or a refused cursor. The global Books DTO/composition is owned by
the catalogue layer and is not part of this seam.
