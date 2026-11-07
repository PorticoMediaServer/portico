# Start and job bounded worker storage

Mapped against held F packet `9f3384b406d3b35571e0faa9666351625eca8904`; no canonical schema or runtime wiring changes.

701–705 use `mp_start_command_receipts`; 741–744 and 746–748 use `mp_job_command_receipts`. W supplies its original immutable payload and digest. These adapters add no codec or public owner. Native generated debt must equal owner UTF-8 bytes + 96 fixed BLOB bytes + payload length. Admission bounds rows/bytes and native sequence. Counter CAS repeats incarnation, owner revision, last/ACK and both debt counters, increments owner revision and records reclaim due. Original replay performs point octet preflight, requires actual admitted bytes before copy, validates purpose/request/operation and leaves digest/decode to W. No mutable response reconstruction.

700/740 accept typed `mpStartJobUserBirth` historical root/member/profile-lifetime data and return complete typed native effects to UO/W. Native incarnation is independently supplied; last/ACK/rows/bytes start at F's zero defaults and owner revision at one. No MP birth header, table, lookup or lifetime owner remains. Fresh completion and prepare functions retain typed validation and draft commitment schemas.

745 retains distinct version custody and exact cleanup request; 746 refuses version deletion. Parent owns version storage/749. Existing physical exit, execution/generation/lease, mutable stop and cleanup fences remain.

Tiny local verification: Go 1.26.10, GOMAXPROCS=2, -p 1, `^TestMPPrepDraft` and shared finish guard (results in handoff). Native cases are prepared/unrun: `TestMPStartJobNativeOriginalPayload` checks zero counters, exact historical tuple, saved original after owner drift, actual debt/admission and request mismatch; existing native effect cases retain source/lease/custody negatives. `TestMPStartJobNativeFSQL` prepares current definitions against full F. Fixtures explicitly skip absent F and source exact current receipt DDL and root ALTERs; no `mp_start_births` dependency.

Parent runner target after publication: Go 1.26.10 on standard ubuntu-24.04 with exact `PORTICO_MP_F_PACKET`, `go test -p 1 -race ./internal/store/writer -run '^TestMP(PrepDraft.*|StartPrepNative.*|StartJobNative.*|TranscodeNative.*)$' -count=1`. No native, race, Capture, performance, live-provider, complete authority, W codec/admission bridge or COMMIT qualification is claimed locally.
