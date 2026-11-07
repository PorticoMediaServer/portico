# LC candidate read custody

This draft composes MG's background producer with RB's closed shared-channel
reader. `NewDriver` accepts the concrete reader, its exact governor/authority
pair, an immutable generation ID and an owner. It installs no product factory.

`WithScope` asks RB to prove the actual stored channel/generation/rule/library.
It transports no config or viewer as authority. The existing
`livechannels/library.Config` → `Normalize` → `Validate` → `json.Marshal` path
remains the accepted input adapter outside native writer/plan. The native test
uses that path; neither writer nor plan imports this package.

`Freeze` asks RB to measure the frozen graph while source output custody is
live, admits a distinct memory-only background grant for actual `ChargeBytes`,
then uses RB's owned clone. This includes opaque roots, scope and continuation.
The copy charge is RB's logical allocator envelope, not an RSS qualification.
The returned driver-owned handle can retain scope/continuation across read
turns. `With` lends its frozen graph only within the callback; consumers must
finish every alias use before returning, including on panic. Job cancellation
or expiry refuses subsequent use without refunding the retained graph. `Stop`
revokes new use; `Close` joins every active consumer before retirement. A
cancelled Close wait leaves active debt held until eventual consumer exit.
Composition must close every successful handle. `WithValues` provides that
ownership automatically for one synchronous consumer. The original output
belongs to its caller and can retire after Freeze completes.

`WithWindow` preserves RB's raw fetched/consumed/EOF and opaque continuation.
An empty eligible batch with a continuation is pending raw work. It cannot seal
an empty pool, infer EOF from item count or confer publication authority.

Candidate selection/staging, resumable worker orchestration, final RB
revalidation and its accepting writer fence, command receipts, admission
profiles, uncertain COMMIT and guide visibility to viewers allowed every named
library remain composition work. No prepared source identity is physical byte
verification or permission for cleanup.

The initial six-parent custody suite passed hosted Go 1.26.10 race at
`abc3c42fdd674b9d9fac5ad8c8de916071f86ecf`, run
[42410725828](https://github.com/PorticoMediaServer/portico/actions/runs/42410725828).
That proof belongs to that exact source; the fresh-main bridge and subsequent
scope method do not relabel it. RB separately owns its candidate producer,
publication adversaries and full Capture qualification.
