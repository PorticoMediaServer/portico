# Home committed policy and content-source integration

Base: parent's integrated manifest `0759e04819866cd26f88d6449faa1c29a8aca205`,
including its own sealed principal ID32501 and saved-view integration ID32500.
Branch: schema-v1/p2-rb-h1-home-sources-r2. Only NEW rb_home_manifest_sources*
files and the new parser-output fixture follow this base. No completed manifest,
saved-view, principal, shared registry, schema, golden or parent test is edited.
The parent's full frozen-family race/Capture matrix is not repeated.

## Committed file policy

The accepted operations configuration bridge is the authority pattern:
prepareConfiguration imports the file's registered people.community_activity
value through writeStoredSettings, records source=file, and advances the console
settings revision in the SAME transaction. RuntimeSettings reads that normalized
stored document. A committed app edit wins while its per-field file projection
is pending; an imported file cannot replace that queued app value.

HomeManifestSourceReader.Manifest reads the actual normalized value, source,
console revision and exact server_configuration_pending field point. Both native
snapshots must agree on the entire underlying manifest and pending row. A typed
stored value with a positive console revision resolves as committed file import,
server value or unattributed old stored value. A valid pending field marks the
committed value as awaiting file projection; the pending revision cannot exceed
this field's console revision. No-source/no-value uses the accepted false registry
default. Missing/inconsistent revision/source/value facts remain explicitly
unresolved, rather than fabricating a policy. Community content stays unsupported
even when its actual committed policy enables the descriptor.

This corrects policy ONLY in a NEW result projection. It never changes a real
source=file witness into a synthetic source=server row to invoke the old
projector. The frozen source frame remains intact. All publication still uses
one original LIVE deadline, copy counter, fresh principal and complete source
comparison with one bounded stale retry. No additional principal query or
callback is introduced by this integration; the frozen reader inherits the
parent's actual ID32501 seam.

Lead ruling: LAST COMMITTED NORMALIZED DB POLICY is Home's authority.
CommittedOnly=true remains explicit. The native reader never reads disk; UO's
import/settings commands commit file changes. Disk freshness is not a reader
requirement or schema gap. No global file ACK is invented; exact per-field
pending facts are used only for the represented field. No DDL, file I/O or
settings write is added to native read.

## Qualified content and UO state

HomeManifestSourceReader.Content integrates actual current visible saved-view
rows with the frozen native scalar browse source and merged UOPersonalDefinitions
point. Row selection comes from the current complete server-owned manifest,
actual resource/share facts and actual stored definition/library; callers cannot
supply a permission predicate, profile, account, replacement query or source.
Hidden, revoked or unknown Home targets return no page. Unsupported families
return ErrUnqualified and ZERO output, never an empty successful shelf.

The business window preserves the frozen 64-position raw bound, age/retirement
admission, exact title ordering and honest continuation. It attaches actual sparse
favorite/watchlist/watched/completion/not-interested/rating state through the
merged token-derived UO point. A missing personal row legitimately supplies that
recipe's sparse defaults; a zero rating remains different from no rating. Item
and retained personal public identities must match the actual emitted identity.
The current native composite FK independently enforces the item/public pair.
No cross-profile state is borrowed.

Final publication re-reads the full actual manifest plus policy and EVERY emitted
personal point, including its public identity and native scalar values. Thus even
an unversioned personal change cannot attach stale state. A changed layout,
resource, grant, catalogue or pending policy invalidates the earlier draft. The
same original deadline/cumulative 4 MiB counter spans lookup, business, final
proof and one whole retry. Closed decoding plus a retained filter use one bounded
scratch hold; this is not another preparation/cache manager. Internal values-only
cursors bind the exact source frame, row, resource and browse fence.

This is a saved-view BROWSE projection with sparse personal state, not a claim
of playback/file eligibility or a complete public ContentEntry DTO. Parent owns
those adapters and wire cursors. The merged leaf ContinueWatching recipe does
not itself prove Home's container/on-deck/removed/resume policy, so it is not
relabeled as successful Home continue/listen/books content. RS recommendations,
family expansion, spotlight/community candidate ordering, and MG current-viewer
live authority/guide windows have no qualified merged Home source recipe in the
inspected base. Those remain explicit dependencies; no guessed ranking or live
permission callback substitutes for them. Larger library projections retain the
frozen explicit preparation/pending result.

## Native test graph and frozen parser output

ALL read and plan TEST graphs were checked with go list -deps -test; no catalog,
dbwork, persistence, identity or livechannels package is reachable. No native
source/test imports configfile/operations or a legacy parser. Oracle production
ran separately, in a temporary standalone program that was removed afterward.
It invoked accepted configfile.Loader.Reload + Resolve with the registered key,
default=false, no environment binding, on temporary TOML files only.

Fixture: testdata/rb-home-manifest-policy-oracle.json
Producer source blob: 8e09a3caefff32fc93db4753e884575c6cea18e2
SHA-256: 99315d068ed9397dab64e904aa328bc7e598a84bdd027d3271daa749c5e1e765
The native test pins these exact bytes and consumes frozen boolean/source output;
it does not reimplement TOML or invoke a parser through the native binary. This
follows root LC-r2's frozen-fixture/oracle pattern without modifying its files.

Only the two NEW source parents are selected for focused verification. Cases are
virtualized like the integrated controls, while actual native SQL/Capture/physical
cleanup still runs. Coverage includes imported true/false/default policy, pending
projection precedence/drift, missing revision refusal, actual saved-view/UO data,
zero-vs-unrated state, valid cross-profile isolation, unversioned personal drift,
hidden-layout refusal, unsupported-family refusal and empty raw-cap reachability.
Reader receipts bind the actual source/schema at run time; no existing goldens or
unchanged functional/race/Capture matrices are refreshed.

Parent current-main integration supplies explicit GoCopyBytes through the shared
saved-view session and preserves the original request ceiling, funding native
decode overhead without shrinking the original cumulative copy allowance.
