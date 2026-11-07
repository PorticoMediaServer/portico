# Exact foreground preparation

This is an opt-in, harness-only phase2 governor primitive. No runtime route or
existing qualification profile enables it. A nil `Config.Preparation` leaves
phase1 admission and accepting-transaction conflict checks disabled.

Trusted composition explicitly funds a `PreparationProfile`: at most four live
jobs, 64MiB aggregate, 16MiB/job, plus a separate nonloanable foreground-memory
floor. Limits can be lowered. Each job's reservation includes bookkeeping,
retained IDs/keys/counts, intermediates and copies. The 4096-byte bookkeeping
allowance is unavailable to payloads. Snapshot/native/output reservations stay
separate. These bounds do not qualify memory use, ready latency or1M capacity.

1. Drain conflicting admitted writer custody by retrying `BeginPreparation`
   after Conflict. A cancelled/expired writer is still physically active until
   retirement. Acquire the lease before the first capture quantum.
2. Store one canonical query/fence job per owner in bounded read composition.
   Attach identical polls to that SAME job. Pass the server-owned job context,
   not the HTTP poll context. There is no renewal operation. The lease deadline
   is min(original deadline, context deadline, admission+12s).
3. Between short independent snapshots, check the lease and bound every quantum
   by its original deadline. Check exact security and source revisions as well.
   Done signals observed invalidation/release; the immutable Deadline also
   bounds waits even when no new governor operation observes clock expiry.
4. Call `ReadyPreparation` after complete computation/cache insertion. This
   DOES NOT release publication protection. A parked client keeps protection
   only until the original absolute deadline.
5. In the first fresh authorized response snapshot, validate all dependencies
   and copy COMPLETE ready values, revisions and cursor into separately admitted
   output. Call `HandoffPreparation` to linearize the response. An intervening
   foreground publication invalidates the lease and makes handoff fail. Release
   the snapshot, then serialize the copied values with ordinary security checks.
6. On cancellation, no interested client, stale revisions or errors, cancel the
   job. Release stale buffers; any bounded retry is a new capture within the
   ORIGINAL preparation deadline. A read composition may retry foreground drift
   at most twice; it must not silently restart after expiry or denial.
7. Only after all retained buffers and owned consumers physically exit, call
   `RetirePreparation`. Cancellation, expiry, cache-ready and handoff alone do
   not refund memory or the four-job physical-debt bound.

Publication dependencies are fact-domain `Scope` values; zero is wildcard, and
no declaration means global conflict. Personal dependencies may conservatively
cover their entire domain. Background and ScanMinimum writers with overlapping
scopes get Conflict; they retain their durable next quantum for later admission.
Nonconflicting publication and bounded immutable staging with genuinely
disjoint declared scopes may proceed. Foreground and Recovery writers invalidate
conflicting leases on successful admission and do not wait for lease expiry.
Trusted security/media/capture composition can call `InterruptPreparations`
without a writer turn. It never waits for cancelled work to physically exit.
No second scheduler or unbounded waiter queue is introduced.

A phase2 closed writer command declares its conservative footprint BEFORE
admission in `Request.Footprint`, resolves actual scopes under fresh transaction
source/authority checks, then calls private `transaction.resolvePublication`.
The shared commit frame calls `CheckPublication` immediately before COMMIT.
Declared narrow scopes cannot publish broader resolved scopes: the frame rolls
back even if the handler ignores the resolution error. Drop custody, expand the
request and re-admit; do not widen an existing grant. An undeclared command is
conservatively global. Existing phase1 commands need no new call while disabled.

New implementation lives in `rb_preparation.go` and writer `rb_publication.go`.
Shared hunks are limited to governor Config/ledger initialization plus the two
admission hooks, one private governed-turn state pointer, and one commit hook.
The governor does not itself authorize viewers, execute SQL, cache results or
attest physical byte release. Its Authority remains trusted composition custody;
closed read/command drivers own those existing responsibilities.
