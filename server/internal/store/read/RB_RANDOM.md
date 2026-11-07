# Seeded random source order

`RandomWindow` rotates the canonical stored32-bit random key, using the existing
public contract: ascending starts at key>=seed then wraps below seed; descending
starts at key<=seed then wraps above seed. Equal keys keep the selected direction's
stable entity-ID tie. No per-request rehash, corpus sort, OFFSET or row-tuple
predicate is used.

Ten closed statements implement first/wrapped halves, equality tails and next
keys, including the upper/lower seed bound after wrapping. Continuations carry
the exact seed and full browse fence. Each quantum fetches at most64 raw rows
before eligibility rejection and emits at most40 values. Hidden/retired positions
advance the same cursor; empty non-exhausted output is incomplete work. At most
three business seeks plus the five first-window fence calls fit the native
8-call bound. The fresh final fence and LIVE/output custody are unchanged.

`RBRandomDefinitions` is inert composition requiring actual Reader Capture.
Own comparison goldens cover both statistics profiles and present/empty cohorts;
functional traversal covers duplicate keys, both directions, wrapping and the
0/uint32-max boundary seeds. No shared reader list, schema or product route is
changed. The existing fail-closed policy envelope remains in force.

Up to13 statements over two windows exceed the proposed10-statement ready-page
ceiling and remain unqualified debt. These controls do not qualify ready latency,
VM, maximum-body,1M,2GB or foreground/background interference. Positional rank
still needs exact preparation; this source pager makes no rank/count claim.
