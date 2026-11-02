# Portico server API

[portico.openapi.json](portico.openapi.json) is the complete API contract. It is generated from the typed registry used by the production router, including conditional features and raw media, upload and event-stream operations. The server and first-party clients use the same API as external clients.

Regenerate the contract from `server/`:

```sh
GOFLAGS=-p=2 go run ./cmd/apigen
```

The same command updates `packages/contracts/src` and the generated admission table. The TypeScript contract exports typed route builders, parameter types, request and response types, schemas and decoders. Supply unescaped parameters to `routePath`; `encodedRoutePath` supports existing callers that have already encoded their parameters. Existing shared-client parsers retain their row-level tolerance and public service interfaces.

Each operation publishes its credential scope (`x-scope`), access class (`x-access`), work class (`x-lane`), database cost class (`x-cost`) and admission pool (`x-admission`). An API key or personal access token never widens the member's authority. Session-only operations cannot be invoked with those credentials. Media grants and other narrow protocol credentials retain their own handler checks.

Plain HTTP and direct access remain supported. Optional features still depend on the services configured by the owner. Read the operation's response schema and error envelope, respect `Retry-After`, and retain opaque cursors and revisions exactly as returned.

For external automation, see the [tool integration guide](guide/README.md). Old hand-authored specifications and retired playback documentation are no longer published.

Hosted account/connectivity contracts live in `packages/contracts/src/hosted`. After regenerating the Hosted registry in its own checkout, sync its generated contract directory with:

```sh
node packages/contracts/scripts/sync-hosted.mjs <hosted-generated-contract-directory>
```

This reads the Hosted artifacts and applies the same shared builder template; it writes only the local contract package. The local server generator does not describe Hosted endpoints.

This is a prerelease API with no compatibility promise. The current public
routes use `/v1`; the former `/v2` playback model has been removed. There are no
legacy sign-in shortcuts to preserve. Library and content screens are composed
by the server. Tools can use the additional bounded title query.

The cursor, precondition and idempotency packages in `apikit` are optional tools,
not global runtime guarantees. Catalogue cursors use a database-held signing key,
retain their traversal position across expiry/revisions and recompute cached totals.
Mutation families implement their own typed revision checks and replay receipts;
the generated route contract describes the accepted fields for each operation.

The SQL preparation check covers complete literal statements, including concatenated
literals. Runtime query constructors require their owning package's semantic and
query-plan tests; a passing literal check does not establish every generated shape
or every query's scaling behavior.

Development contracts are generated separately, from `server/`, with
`GOFLAGS=-p=4 go run -tags devtrust ./cmd/apigen -development`. They live in
`packages/contracts/src/development`; release builds register neither route.

There is no third, "optional" contract: the dormant remote-command and Hosted
profile protocols it once recorded had no handler anywhere and no first-party
client, so they were removed with their client code. The development module
does not enter the production OpenAPI document.

An initial library browse page may carry `wholeSelectionUnavailable` with
`reason: "source_changed"` when its optional exact whole-view selection loses
its content basis during composition. The ordinary snapshot page remains
usable; this state carries no selector, count or preparation handle. Reloading
can prepare a new selection. Current grants and visibility policy are still
checked before delivery; selection polls, held proofs and actions retain their
strict stale-basis refusals.
