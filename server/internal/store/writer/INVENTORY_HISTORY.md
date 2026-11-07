# Inventory history and ACK composition

`AcceptInventory`, `ResolveScan` and `AckScan` use the same private runtime,
executor, mutex, governor/authority/WAL tuple and physical terminal path as
EffectiveStage. There is no public opener, capability constructor, registration
callback, second writer or worker-selected class/budget.

FIRST disclosure is an opaque internal ScanDriver scope bound to the Writer,
run, library identity/key, command incarnation and live lifetime. Current
permission is checked independently of caller cancellation before native scope,
after lookup, before terminal work and again before releasing data. Any error clears all returned payload, identity, floor and evidence. The private
frame retains actual physical truth; zero output is never rollback proof.

Native scope and history have **no retired/root/status/source/lease filter**.
The original immutable receipt, ordered N1 cohort and closed result digest are
resolved before fresh witnesses. Above-floor missing proof requires resolution;
the ACK floor precedes any potentially drained cohort. A retired library's saved
result still replays when current principal authority passes.

Only a proved miss with a concrete original prepared command reaches the
separate `scan.inventory_accept.fresh_library` read. A retired library refuses
with `ErrConflict` and no effects. Active fresh input still refuses as
unqualified: SC's complete fresh effect body, its native witnesses and a measured
whole-command recipe have not landed. No partial catalogue acceptance is enabled.

ACK is a same-turn scalar owner CAS with the original identity/digest checked
first. It advances only the actual floor, refunds no bytes and deletes no
receipt/result rows. Ambiguous COMMIT quarantines/fences the shared runtime;
committed safety-ceiling overrun retains the actual floor privately but returns zero data with
its error. A single shared deferred finish checks caller and protected lifetimes
after terminal work and WAL sampling on every release path. The exact protected
deadline remains live independently of the SQL context's internal cleanup cancel.

Every profile must carry matching Capture registration, and the primary receipt
must cover the complete combined compiled registry. Runtime binding controls
capture that full set on the actual asserted fixture connection. Registration
qualifies SQL/plans/sources, not principal authority, source provenance, resource
forecasts or physical service. Fault-only registries remain confined to the
reviewed ordinary-binary-refusing test adapter.

The fixtures use virtual time and synthetic resource forecasts. Production
opening/restart, trusted ScanDriver issuance, complete native/Go byte admission,
SC fresh acceptance, crash recovery and per-class hold/WAL/scale/service
qualification remain separate gates. No test fixture is a production grant.
