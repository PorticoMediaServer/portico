# Foliate paginator

Official source: https://github.com/johnfactotum/foliate-js/blob/78914aef4466eb960965702401634c2cb348e9b1/paginator.js

Upstream commit: `78914aef4466eb960965702401634c2cb348e9b1`.
Original SHA-256: `51cec81572d5d182e231fe68ce9c3b5489a0976b36172060516336ba893565ea`.
MIT licence retained in `LICENSE`; compatible with Portico's GPLv3.

This is the pinned engine's paginator, with lifecycle and compatibility fixes
for Portico's abortable mounted surface. It has no stock reader chrome. The EPUB parser and
CFI implementation still come from the exact pinned `foliate-js` dependency.

Changes from upstream:

- Disconnect both resize observers on close; upstream unobserves the wrong
  paginator element and assumes a first view already exists.
- Cancel a pending iframe load and detach its listener on close.
- Fence late resource, font and animation-frame continuations after disposal.
- Remove the book transform and media-query listeners on every close, including
  close before the first section resolves. Destruction is idempotent.
- Replace one CSS-prefix lookbehind expression and `findLastIndex` with
  equivalent local helpers so the paginator parses and navigates on iOS15.1.
  These changes do not establish whole-engine or native-host acceptance.
- Apply the trusted app document's style nonce, before connection, to the
  paginator shadow layout and its two renderer-owned section style elements.
  Publication styles and their sanitization/CSP are unchanged; publication
  markup is never consulted for a nonce.

- Opt the Portico adapter into canonical320ms page transforms, physical settle-spring touch release and system/app160ms reduced fading; cancel original motion on view/layout/session retirement. Suppress spatial dragging under reduced policy, fade out an already held drag before withdrawing it, and bind each commanded turn to its original caller signal. Original canceled frame/load callbacks cannot adopt a later turn. Legacy non-opted-in paginator input remains unchanged.

The shared installed dependency and immutable dependency seed are unchanged.
