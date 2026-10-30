# Private capture integration

This package retains bytes. It does not admit disc scan, playback, encryption,
source policy, or a viewer. Root52 permits only one complete finite ISO capture;
a member of a folder disc does not version its dependencies.

`New` requires the real source producer. `Capture(ctx, Request{Principal, AssetID})`
resolves its original actor/physical source through that producer, obtains one
reserved from-start stream, and returns only after finite EOF, successful actual
writes, immutable artifact sealing and committed receipt/source accounting.
`Persisted` acknowledges cumulative actual successful writes, never read estimates.
No acquisition is reopened or resumed. A new acquisition has a new capture/version.

Capture/Open/Collect run outside any carried SQL transaction. Resolve an exact
`Receipt` (capture ID plus original `Binding`) with `ResolveTx` when needed; there
is no latest capture or digest-authority lookup. `Open(ctx, exactReceipt)` commits
a bounded reader pin before exposing its fresh verified read-only descriptor.

`Reader` implements the captured-disc producer port: `File`, `CaptureIdentity`,
`Context`, `Check`, `CheckTx`. Merge the original reader context into the exact
helper/converter lifetime. Retain the complete reader until that process exits,
then `Close`; closing only its file is not releasing its row/source custody.
Before publishing derived facts, use that same reader's `CheckTx` in the original
accepting transaction, then bind every fact/grant/seek/replan to its capture ID
and complete scoped `Version.ID()`. A fingerprint or receipt conveys no content
permission. Other invocation, entitlement, policy and title guards remain required.

`MaxCaptureLifetime` is a 24-hour safety ceiling. The source producer shortens it
to the original actor/family/source policy expiry; local possession extends none
of them. New reads require the same original live source/config/actor/policy and
receipt, and a retirable source lease. There is a global 256-reader safety cap.

Run `Start(ctx)` and `Close` with the production service lifecycle. Retirement
cancels exact local readers, denies new ones, and waits for normalized pins and
physical helper/descriptor leases before deletion. Incomplete attempts are
removed, never resumed. Pending digest journaling precedes Seal so a crash cannot
turn an unreferenced sealed object into a usable receipt. Ambiguous cleanup keeps
source accounting charged. Source bookkeeping alone handles the prior Admit/no-
owned-row gap, under its original acquisition/reservation CAS.

The private root, staging and objects directories must retain their original
identity on one filesystem. The producer shares saved source quota and the actual
filesystem growth/floor reservation with the existing source cache. No positive
cache permission, complete reservation, floor, pacing or current authority means
refusal. Unsupported hosts and incomplete/folder paths stay unsupported.

These are source contracts, not runtime qualification or feature completion.
