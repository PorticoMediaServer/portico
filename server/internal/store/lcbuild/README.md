# LC candidate read custody

This draft composes MG's background producer with RB's closed shared-channel
reader. `NewDriver` accepts the concrete reader, its exact governor/authority
pair, an immutable generation ID and an owner. It installs no product factory.

`WithScope` asks RB to prove the actual stored channel/generation/rule/library.
It transports no config or viewer as authority. The existing
`livechannels/library.Config` → `Normalize` → `Validate` → `json.Marshal` path
remains the accepted input adapter outside native writer/plan. The native test
uses that path; neither writer nor plan imports this package.

`WithValues` asks RB to measure the frozen graph while source output custody is
live, admits a distinct memory-only background grant for actual `ChargeBytes`,
then uses RB's owned clone. This includes opaque roots, scope and continuation.
The copy charge is RB's logical allocator envelope, not an RSS qualification.
The consumer must finish all use before returning, including on panic. A
cancelled context refuses subsequent use while memory debt remains held through
consumer exit. The driver retires its retained output only; the original output
belongs to its caller and may be retired once the copied consumer is entered.

`WithWindow` preserves RB's raw fetched/consumed/EOF and opaque continuation.
An empty eligible batch with a continuation is pending raw work. It cannot seal
an empty pool, infer EOF from item count or confer publication authority.

Candidate selection/staging, retained cursors across worker turns, final RB
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
