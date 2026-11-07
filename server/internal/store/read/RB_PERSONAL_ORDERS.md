# Personal browse orders

Harness-only complete-small-set reads for `personalRating`, `lastPlayed` and
`playCount`. The current token determines the profile; callers cannot name a
different profile. Current catalogue membership and canonical public identity
join the personal facts. The normal library/authority/restriction/publication
fences and LIVE/output custody apply.

Positive values use their typed numeric or stored timestamp key, then entity ID
in the requested direction. Integer play counts retain all 64 bits. Missing or
zero values use the accepted title key under SQLite ASCII NOCASE, then entity ID
ascending in both directions. The missing run precedes valued rows ascending
and follows them descending. Article-policy variation is currently refused.

`PersonalOrderExact` reads the complete raw library/kind set, including retired
and restricted rows, with a 257-row exhaustion probe. A set above 256 raw rows
returns `ErrNotReady` with zero payload. Only a fully exhausted set is sorted in
Go and paged with an exact total. Each page rechecks the complete current set;
its opaque cursor binds the whole comparable fence, sort and anchor. There is
no SQL aggregate, offset, sort, per-profile catalogue or durable result table.

Admission needs at least 262 raw-row slots for five common fence rows plus the
257-row source bound, and the ordinary prospective native/Go/output byte and VM
budgets. Actual strings are charged by the common reader before retention.
Initial/source/publication use 11 statements, carrying the existing statement
budget debt tracked in #1357. Default container and unsupported-label refusals
remain in force. Larger exact producers/cache, aggregate-container personal
semantics, filtered composition and complete API DTO projection remain pending.

Functional controls use virtual time and cover all three sorts/directions,
NULL/zero values, ASCII versus Unicode titles, close-to-MaxInt64 play counts,
other-profile facts, native public-identity FK enforcement, raw hidden-tail
refusal, revocation/viewer-revision drift, cursor binding and actual LIVE debt.
Both native statistics profiles require source-bound Capture receipts. This
draft carries no latency, capacity or large-library qualification claim.
