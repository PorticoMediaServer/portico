# Typed facade contract

This contract is intentionally non-executable. Every entry returns a zero result
and `ErrNotImplemented`, including with nil/zero receiver, context, command or
disclosure. It performs no admission, SQL, allocation, receipt lookup or event
publication. It neither constructs Applied nor fabricates execution evidence.

| First command | Execute | Historical resolve | ACK |
| --- | --- | --- | --- |
| InventoryAccept ordinary batch | `AcceptInventory` | `ResolveScan` | `AckScan` |
| Publisher hidden image BEGIN | `StageSearchImage` | `ResolveSearchImage` | `AckSearchImage` |
| Enrichment hidden EffectiveStage BEGIN | `StageEffective` | `ResolveEffective` | `AckEffective` |

The three command interfaces are sealed request values, not SQL/execute callbacks.
SC and PB add their concrete immutable request implementations in writer-owned
domain files. EN's existing private BEGIN command implements its marker. Domain
constructors must use W's complete original request encoder; there is no public
purpose, class, budget, handle, callback or registration override. Markers and
externally embedded/wrapped interfaces authorize nothing: the implementation
must match only its explicitly compiled concrete request types, never invoke a
marker or accept an arbitrary interface implementation.

The separate disclosure types have no public minting API. Zero/nil/foreign values
are invalid; only W's trusted composition will issue live scoped capabilities.
FIRST scan permission is internal ScanDriver only. Current disclosure precedes
all receipt access; fresh root/source/lease/profile/overflow checks follow a
proved historical miss. Above-floor missing proof is ResolveRequired; ACKed
results return ResultRetired before decoding a possibly drained cohort. ACK
refunds no bytes. A committed outcome may accompany a terminal error and must
never be retried under a new identity merely because an error was returned.

The result projections are values, not their canonical encoders. Inventory uses
the agreed full ordered ID/info_epoch cohort. Publisher image BEGIN uses actual
post-CAS job revision and saved image/cursors/totals/seal. EffectiveStage uses the
existing closed scalar BEGIN result. These are neither serving/publication nor
Basic/evidence-completeness certificates. Search's schema has no result-digest
column; this contract invents none.

`ExecutionEvidence`, when implemented, describes THIS call's physical custody,
actual COMMIT attempt/return/outcome, class/durability/asserted native policy and
timestamped WAL observations. Missing evidence stays nil/zero. Replay cannot
invent the original call's physical times. Physical return ends the writer hold;
later WAL sampling and API return remain distinguishable. Evidence is outside
the immutable result digest and is not permission or durable replay proof.

Still omitted deliberately: startup/config and disclosure issuance, command input
constructors not yet fixed by their domain, production resource profiles, public
fixture creation, EvidenceStage's new result wire, final EnrichAccept, fragment/
seal/head/reclaim/People/Reading commands, and 006 retirement. QL can bind these
signatures and evidence fields, but must wait for a reviewed executable command,
trusted composition and actual fixture producer on main. No SQL/grant/facade
clones or synthetic effects are authorized by this contract.
