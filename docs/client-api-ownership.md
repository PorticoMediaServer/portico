# Client API ownership

`packages/client-core` owns app API policy: named domain operations construct
paths, methods, bodies and auth policy, parse the existing DTOs, and keep retry,
revision/cursor recovery, request budgets and cache/freshness semantics. App
screens own view state and actions and subscribe to core documents. A generic
`request(path, method)` wrapper in a screen does not satisfy this boundary.

Run `python3 ci/client_api_census.py`. The checked-in
`ci/client-api-baseline.json` is an upper bound: a move passes without editing it.
Optionally run `python3 ci/client_api_census.py --update` to lower it; the update command refuses additions. Use
`--report /path/to/report.json` for every exact file, line, enclosing owner,
callee, operation ID and expression, including approved exceptions. Source line
numbers are navigation aids; baseline identities survive line shifts outside reviewed guard/native method bodies. Identical
expressions are a multiset, so adding another identical call still fails.
`--base <sha>` also rejects checked-in baseline increases and unapproved exception changes
against the review base. Live checks also scan actual source at each comparison
base, so stale-high JSON cannot authorize additions. PRs compare with their merge
base; merge groups check both the target-tip aggregate range and the validated
immediate predecessor range. A later group cannot spend an earlier group's
removal credit; inherited growth also fails. Every first-parent increment is
additionally checked using that entry’s own immutable source against its parent,
so a clean final entry cannot carry an earlier failed entry. Incomplete queue
boundaries fail closed. Historical source uses current checker code and never
changes the live checkout or index. Historical source/metadata read
failures fail closed. Manual live checks require a genuine earlier comparison
base. IDs cannot overlap `sites` and `additionalSites`; their complete combined allowance multiset is checked, including duplicate occurrences. It runs on every PR/merge-queue hygiene job; nightly
checks the checked-in upper bounds. Periodic baseline-only refresh PRs lower
both count baselines on current main; exceptions and approvals remain exact per PR. `--initialize` is
only for the foundation: it refuses an existing baseline.

Live checks share invocation-scoped results keyed by exact Git blob IDs.
Historical trees come from `ls-tree` and `cat-file`, so archive attributes,
checkout filters and working files cannot substitute different history. Native
lexing and engineering pattern counts reuse unchanged blobs; typed counters keep
their compiler projects open and invalidate results through resolved imports,
global/ambient inputs and configuration. Unresolved dependencies bind the whole
project. No dependency install or `node_modules` link is created. Each queue
entry has its own historical HEAD for approval ancestry checks.

Historical dependency inputs must match the final install: lockfiles, npm
configuration and dependency/workspace fields in package manifests are bound
before any fresh or cached typed result is accepted. A differing environment
fails closed and requires entry-bound dependency installs; it cannot be judged
using the final checkout's declarations. Loaded installed package versions
must also match their install lockfile (npm build metadata is ignored).
Executable `.mts`, `.cts`, `.mjs`, `.cjs`, `.ts`, `.tsx`, `.js` and `.jsx`
sources participate. Production imports reaching excluded executable test,
script or generated-output areas bring those files into the inventory,
including through barrels; unresolved excluded imports fail closed.
Direct CommonJS `require()` calls are collected from AST nodes, including in
TypeScript. Relative executable targets from the exact tree are added to the
compiler inventory until their closure stabilizes; missing, dynamic or
unsupported targets fail closed. Tracked JSON and Metro bundled PNG loads are
data. Required-child changes invalidate cached callers across compiler scopes;
closure expansion is bounded and uses the same invocation deadline.
Source and configuration parsing diagnostics fail before cached or zero results;
an unsuccessful snapshot never leaves a reusable successful answer.

The internal deadline is 180 seconds per invocation; timeout, worker, object or
source-read failures fail closed. `PORTICO_RATCHET_BUDGET_SECONDS` may select a
positive deadline up to 360 seconds; the CI job timeout is unchanged. Reproduce
a measured six-entry walk with `portico-heavy ratchet-benchmark -- python3
ci/benchmark_ratchets.py --base <target-sha> --head <queue-head> --report
<report.json> --expect-entries 6 --verify`. Verification compares each tree with
a fresh, uncached counter outside the reported gate time.

The scanner starts from lane G's all-route evidence census and replaces literal
matching with construction/request detection. TypeScript's AST and resolved call
signatures identify reads **and writes**, including requester aliases, dynamic
paths, raw fetch, EventSource, XHR open and generated route builders. A nested
route builder inside a counted request is one site. DTO type literals, comments,
imports, plain documentation strings and route checks do not create requests.
The compiler project explicitly includes every selected production source, even
if an app's normal tsconfig omits a platform directory. Failure to load a source
is an error. Kotlin, Swift, Objective-C, Java and Rust use a lexical call/message
scanner covering native request construction, HTTP dispatch and composed API
URLs. Native comments, quoted examples, method declarations, Rust inline tests
and Objective-C debug proof blocks do not count. This is static ownership
inventory, not runtime reachability or proof of external API non-use. New native
transports or custom request factories must extend the scanner with a regression
test in the same PR; arbitrary source transformations cannot be proved by a
static census.

Scope includes web, native (including `platform/`, Android, Apple and TV
extensions), desktop TypeScript/Rust, Vega and shared player/reader packages.
Desktop also inherits web; Vega also inherits native. Test/build/script/dev
sources and generated contracts are excluded. Cast receiver and website source
are still scanned and listed as separate-product exceptions. Hosted is a
separate repository product, outside this inventory.

`ci/client-api-exceptions.json` pins **exact sites and multiplicities**, by file, with a category
and reason. Adding a domain request to the same file does not inherit an
allowance. Approved categories are:

- OS/player/image/background-transfer byte fetches of core-built scoped URLs;
- bounded out-of-process native extensions (Top Shelf, TV channels, widgets,
  notification extensions) using generated contracts;
- Android `PorticoListeningOwner.kt` and Apple `PorticoListeningOwner.m`,
  explicitly approved to own queue/session/item/user-mutation requests while JS
  is suspended;
- Cast receiver, website and Hosted as separate products;
- individually approved platform transport guards: core fully builds path, method,
  body and auth; the wrapper adds only the checks authorized by its linked ruling
  and constructs nothing. The scanner checks unchanged arguments, including the
  approved raw/Console, read-only, signal-only options and fifth-options shapes.
  Every allowance binds the SHA-256 of the complete reviewed guard function or
  enclosing policy, including captured API selection for upload/bounded ports. Any edit invalidates the pin and needs re-approval. Native identities include the full
  receiver/fluent construction chain and enclosing method body hash, so changes
  to URL, method, body or bound inputs invalidate byte-only allowances.
- separately ruled non-API platform IO: the exact reviewed span cannot reach a
  Portico or Hosted API. The development diagnostic sink is inert in the bound
  release configuration; the reader font is a same-origin bundled asset with
  omitted credentials and exact size/digest. These are not general fetch
  exceptions. Their `sourceHashes` also bind policy-bearing files outside the
  function (including configuration and asset bytes), so an unchanged call
  cannot survive a changed target, credential mode or release setting. Each
  bound path must reach a regular file without symlinks, including every parent
  directory below the source root; in-root relocation is rejected too.

The two separately approved metadata-artwork callbacks use an explicit
`guardSpan: "forwarding-function"` in both pin and ledger (`guardSpans` maps
primary pin IDs). Their complete callback/method hashes are recorded by the
ruling. An emitted `forwardingHash` never narrows a policy pin implicitly; changing
span scope requires an exact fresh ledger approval. CC-4 upload/bounded pins bind
the complete approved SettingsEntry policy, including the captured originalApi.

When one census ID covers distinct reviewed spans, an exception and its ledger
row use `file#siteID@fullGuardHash`. Each such binding has exactly one occurrence,
and the suffix must equal its complete `guardHash`. Paired and unqualified pins
for the same ID cannot coexist. This is an exact pair, not a hash family; another
hash remains DIRECT and cannot consume a different pair's allowance. Existing
explicit approvals for two identical occurrences of one span retain their
combined budget. During pair activation the gate also compares the complete
source occurrence total with the base, so conversion to exceptions cannot hide
an added call inside the old DIRECT budget. A paired re-hash needs a fresh exact
ruling; the legacy direct-identity re-hash schema does not broaden these pins.

Playback forwarding recognition includes a request or a copy of that same
request with only `signal` replaced, chosen conditionally, and positional
`farewell(path, method, body)`. Recognition alone grants no allowance: every
approved playback guard still binds its full enclosing policy and exact budget.
Adding/replacing path or body fields is not one of these forwarding shapes.
New JavaScript forwarders use a closed AST grammar in
`ci/client_api_forwarder_grammar.mjs`. It admits named guard calls from a finite
list, verbatim parameters to one named dispatch target, signal references and
`AbortSignal.any` composition, and response returns. Read-only predicates,
error exits, `const` bindings, and `try`/`catch`/`finally` support those forms.
There can be at most one dispatch on an execution path; guards cannot hide a
second dispatch. Unknown statements, calls, mutable locals, destructuring,
request aliases, computed calls, and writes have no production and are rejected.
The grammar does not attempt to prove a named guard's semantics: independent
verification and the exact full-span ruling still provide that boundary.

Previously approved complex policies remain as **frozen exact legacy cases** in
`ci/client-api-legacy-forwarders.json`. The packet names the immutable main
snapshot from which its bindings were taken, the original ruling and occurrence
budget, the file, full policy hash (and separately approved callback hash where
present), owner, exact dispatch hash/text and former shape. It is not populated
from the live approvals ledger. Existing abort-listener cleanup, event resync,
XHR execution and other richer policies can keep their reviewed bytes without
extending the grammar for future wrappers. Any changed identity misses the
frozen case; refreshing only a pin or ruling cannot admit that new shape.
Removing a frozen case is allowed. Adding a future case requires an explicit
new decision and a reviewed change to the frozen provenance check.
Recognition also binds the resulting canonical ID and spends the original
frozen occurrence budget independently of the current ledger. Increasing a
current allowance cannot give another occurrence legacy recognition. The closed
grammar governs all new pins; an edited legacy span must fit it or return for
an explicit new ruling and legacy-list update. No app refactoring is required
to preserve the existing approved spans.

Recognition reports `forwardingGrammar: "closed"` or `"frozen-legacy"`, and a
`grammarRejection` reason for spans outside the grammar. These fields do not
change site identity, span binding, ledger authority or occurrence accounting.
All references to implicit `arguments` remain rejected across the full bound
policy, including a policy which would otherwise match a frozen case.

Deliberately obfuscated evasions through eval, Function constructors or proxy tricks remain outside semantic verification by the recognizer; independent verification and a lead ruling on every exact complete-span hash remain the security boundary.

Native byte-transport executors which take an already constructed core request
are reported too; their migration disposition must preserve platform IO and
lifetime boundaries. This census never requires waking JS for background
listening, moving OS byte IO into JS, or placing auth/probes/events in the viewer
query cache. Native code that independently builds a domain API URL is counted
unless the precise site is an approved listening/extension exception.

Individually ruled native policies use explicit `guardSpan: "native-function"`
bindings to the complete canonical `constructionHash`, count one, and required
`sourceHashes` for their native source and reviewed policy dependencies. This is
an exact native judgment, separate from JavaScript forwarding recognition: it
does not claim that bounds, byte decoding, validation-only comparison paths or
dispatch of an already received response are signal-only transports. A null or
shortened span, missing native source binding, another language or an expanded
occurrence budget fails closed. Sibling helper edits invalidate the complete
file condition even if the constructor's own method is unchanged.
Kotlin standalone `= try { ... } catch (...) { ... } finally { ... }`
expressions bind the signature and every catch/finally clause. Chained, Elvis,
cast or unknown infix continuations remain unbound until their complete
expression is supported; the detector never pins a partial try-expression.
Following members are recognized through balanced annotations, use-site groups
and [Kotlin declaration modifiers](https://kotlinlang.org/spec/syntax-and-grammar.html).
Those prefixes stay outside the preceding function span. Incomplete annotation
groups and anonymous-function infix operands remain unbound.
Kotlin boundaries require a real function name after a receiver. Only lexical
newlines outside comments and literals can separate a following declaration;
comment newlines cannot terminate a policy-bearing continuation. Unicode letter
identifiers and backtick names follow the same declaration check. Nested string
interpolation, raw-string closing quotes and nested comments retain their full
source ranges. Compiling native-test fixtures cover those boundaries and their
URL-policy mutations; the Python model tests read the same fixture source.

The initial slice inventory is in [client-api-slices.md](client-api-slices.md).
Each slice migrates every surface for its domain, keeps the wire and behavior,
and lowers this baseline. No visual or interaction change. Device acceptance is
batched after all native changes. The existing query-read ratchet remains a
separate check; only its mechanically verified per-file decreases belong in
these PRs, with before/after counts stated.

Future exception changes require an entry in `ci/client-api-approvals.json` for
the exact site, category, source hash (guards), reason/condition, maximum approved
occurrence count and a GitHub comment URL containing the lead's ruling. The
review-base check rejects any changed/expanded allowance without that entry.
An approval for one occurrence cannot authorize a second. The ledger is itself
reviewed: every edit needs a fresh review verifying each entry against its linked
ruling. CI validates the binding/schema; it does not infer approval from arbitrary
source text or fetch comments during a build. Existing approved standing
categories and CC-0/CC-2/CC-3 sites cite the initial lead ruling.

Ledger entries can precede their reviewed source commit landing. They grant no
active allowance by themselves: the slice must install the exact matching pin
and lower its direct-site baseline when that source is present. The refreshed
CC-4 entries and verbatim native proof executors record their reviewed commit.

Detection improvements use `ci/client-api-detection-additions.json`, separately
from exception approvals. Each exact addition records its maximum occurrence
count, reason and lead-ruling URL, together with an immutable source commit.
For any baseline growth, CI re-runs the new detector on that commit and the PR
base. Only the minimum matching count in both snapshots is permitted. A request
added by the product patch, a larger duplicate count or a previously deleted
request cannot grow the baseline using a historical approval. Ordinary slice
PRs still only shrink it. Regenerate approved coverage with `--update --base
<base-sha>`; a bare `--update` never permits growth.

The detector resolves core HTTP methods and aliases, including multipart and
bounded requests and the core V1 request-object seam. Native URL readers require
java.net.URL bindings, async Swift reads require URLSession receivers (including
module-qualified bindings, inline constructors, grouping and call/property chains
for data/bytes/upload/download). Swift ordinary, multiline and extended-delimiter
string payloads stay opaque; executable interpolation is tokenized recursively,
including nested strings/comments, while enclosing request identities stay stable. Fluent
HTTP calls require known HTTP constructors/types/modules, including borrowed Rust
clients with mutable/lifetime-qualified references. Local files, model
methods, diagnostics and decoder streams are negative fixtures.

A byte-identical forwarding call whose enclosing function changes can use the
[standing re-hash ruling](https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646375725).
The editing PR carries the ledger record and complete function-span evidence,
and an independent review must confirm no added construction or selection.
`identity-rehash` records keep the site direct: they consume an old baseline
occurrence and replace only its identity, with no total increase. They cannot
approve an exception. A `platform-transport` re-hash must originate from an
already-approved transport with the same reason, condition and span scope.
Retained and replacement pins share the original approval’s occurrence budget,
including multiple replacement rows. Copying or splitting a guard cannot expand it.

Each new-ID record adds `previousSiteId`, `previousGuardHash`, `previousCommit`,
`reviewedCommit`, `pullRequest`, `reviewUrl` and `spanEvidence` to the usual
reason/condition/hash/count/ruling fields. `spanEvidence` names a checked-in
`docs/client-api-rehashes/*.json` with exact `oldSpan` and `newSpan` strings.
CI verifies their full hashes and immutable source certificates, identical complete
call bytes (including Objective-C receivers) as multisets, and occurrence conservation.
The old source commit must belong to the comparison base’s ancestry. A linear
rebuild may retain a non-ancestor independently reviewed commit only when that
commit contains the exact current full span and matching call multiplicity. The independent review URL must belong to
the editing PR. No re-hash is installed merely because an author calls a change
unrelated; the linked independent review must assess that condition.
