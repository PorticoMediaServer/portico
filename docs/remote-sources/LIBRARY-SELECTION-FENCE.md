# Remote folder selection at library acceptance

Library creation and library source writes accept an optional `expectedRemoteSource`
object with `sourceId` (1–256 characters) and positive safe-integer `generation`
(1–9007199254740991). Explicit `null` is rejected; only omission preserves the
legacy unfenced input. Both create and folder settings use the same non-pointer
empty-omitted input wrapper, retaining strict outer-body decoding. Requested `folders` or `path`
remain the exact selected paths. Local callers can omit the object.

The catalog writer repeats the existing owner/configuration authorization and
compares this identity with the exact `remoteSourceIDExpr` association for every
requested path in the accepting SQL transaction. Removal, reconfiguration,
replacement, or a new nearer source prefix refuses with the existing
`source_changed` 409. No library/folder/scan transaction commits on that refusal.
The existing filesystem SourceGuard and root identity rules remain in force.

The owner source status response publishes `assignmentFenceSupported: true` only
with this writer implementation. New phone assignment requires that explicit
flag; absence on an older server is unsupported, never assumed safe. The flag is
a compatibility gate, not a substitute for the accepting writer comparison.

Generated contracts follow the next authorized Release generation checkpoint.
No generated files are hand-edited or regenerated per item.
