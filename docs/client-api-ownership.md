# Client API ownership

`packages/client-core` owns app API policy: named domain operations construct
paths, methods, bodies and auth policy, parse the existing DTOs, and keep retry,
revision/cursor recovery, request budgets and cache/freshness semantics. App
screens own view state and actions and subscribe to core documents. A generic
`request(path, method)` wrapper in a screen does not satisfy this boundary.

Run `python3 ci/client_api_census.py`. After a move, run
`python3 ci/client_api_census.py --update` and commit the smaller
`ci/client-api-baseline.json`. The update command refuses additions. Use
`--report /path/to/report.json` for every exact file, line, enclosing owner,
callee, operation ID and expression, including approved exceptions. Source line
numbers are navigation aids; baseline identities survive line shifts outside reviewed guard/native method bodies. Identical
expressions are a multiset, so adding another identical call still fails.
`--base <sha>` also rejects checked-in baseline increases and unapproved exception changes
against the review base. IDs cannot overlap `sites` and `additionalSites`; their complete combined allowance multiset is checked, including duplicate occurrences. It runs on every PR/merge-queue hygiene job; nightly
checks the checked-in baseline against all current sources. `--initialize` is
only for the foundation: it refuses an existing baseline.

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
  Every allowance binds the SHA-256 of the complete enclosing reviewed guard
  function. Any edit invalidates the pin and needs re-approval. Native identities include the full
  receiver/fluent construction chain and enclosing method body hash, so changes
  to URL, method, body or bound inputs invalidate byte-only allowances.

Native byte-transport executors which take an already constructed core request
are reported too; their migration disposition must preserve platform IO and
lifetime boundaries. This census never requires waking JS for background
listening, moving OS byte IO into JS, or placing auth/probes/events in the viewer
query cache. Native code that independently builds a domain API URL is counted
unless the precise site is an approved listening/extension exception.

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
java.net.URL bindings, async Swift reads require URLSession receivers, and fluent
HTTP calls require known HTTP constructors/types/modules. Local files, model
methods, diagnostics and decoder streams are negative fixtures.
