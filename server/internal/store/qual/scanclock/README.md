# First filename visibility clock

This bounded, constant-memory harness collector retains one declared fresh N1
Applied observation and its original receipt reference and target IDs. It opens
no database, issues no authority or command, and supplies no receipt verifier or
reader factory. Unit records and timestamps are fabricated correctness controls.

When the live harness is wired, `New(start)` receives the owning process's
monotonic timestamp at the **actual offer of the scan's first supervised native
traversal operation**, before starting that helper request. Fixture generation,
probe completion, syscall return and arbitrary process startup are not that
event. The collector cannot observe or attest the externally supplied origin;
the owning harness must retain its raw offer evidence and run/clock linkage.
Manifest fields are declarations to join to retained fixture/source/binary/host/
profile evidence, not attestations performed here.

`applied_observed_ns` is origin-to-Applied-observation elapsed time, not physical
commit or writer hold time. `first_filename_visible_ns` is reserved for
origin-to-proven-reader-visibility elapsed time (`visibleAt - start`). That is
different from Applied-to-visible elapsed time (`visibleAt - appliedAt`); the
latter is not reported. Every interval must be strictly positive. Equal or
reversed observations are refused, remain null and carry a labelled reason.

**Filename visibility is currently always null.** A released browse row with
the same subject/library/database, title or public ID cannot join the original
object and asset at the accepted revision to current serving state, nor prove
the original receipt still resolves. The supported current public-facade witness
for that join is unavailable; `Browse` returns `ErrObservation` and the report
names that gap. Browse's `catalog_entities.revision` and the accepted asset's
`catalog_assets.info_epoch` are different domains and must never be equated.
No witness API or test authority is invented to fill this gap.

Reports are DIRECTIONAL with qualification=false and explicit one-target scope.
Whole-population filename visibility, both Basic clocks and completion remain
unmeasured: durable directory/frontier completion and Basic hooks are separate
gates. Do not report this target as a whole-library first/all milestone or turn
missing fields into zero.

The actual small fixture/HDD run still needs W's reviewed lifecycle-bound trusted
ingress, driver/composition, fresh N1 enable and actual reader calls. The public
SC constructor/body alone do not supply those gates. This is independent report
preparation; it does not produce a live time-to-filename-visible result yet.
