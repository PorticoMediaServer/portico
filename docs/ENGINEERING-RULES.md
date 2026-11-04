# Engineering rules

Short rules that keep Portico from repeating old bugs. Each one says how it's enforced. Where a check only stops things getting worse, its counts live in `ci/engineering-rules-baseline.json`: a count may fall, never rise. When yours falls, run `python3 ci/engineering_rules.py --update` and commit the new baseline.

## Sign-in and authority

- **Bind to the sign-in, never the token.** An operation that spans several steps (a setup, an owner move, an email change, a deletion) keeps the account, its auth epoch and its session family. A routine refresh rotates the token inside the same family, and the operation carries on. An epoch change, a revocation, or another family, account or mode refuses it.
  - Server: `FamilyBinding` and `AuthorizeFamilyBindingTx` in `server/internal/identity/family_binding.go`.
  - Clients: `compareSessionAuthority` and `bindAuthority` in `packages/client-core/src/session-authority.ts`.
  - Check: no new `accessToken ===` or `!==` comparisons in client code (ratchet `token-compare`).

## User actions

- **Every action a person starts ends in something they can see:** done, an honest failure, or "taking too long" with Try again. Nothing spins forever, a cancelled action stays cancelled, and a late result never lands.
  - Use `createUserAction` (`packages/client-core/src/user-action.ts`) through `useUserAction`.
  - Show failures in the screen's drawn error slot, or with the kit's `ActionNotice`.
  - Check: the wrapper's tests. There's no reliable static check for this one, so reviewers look for it.

## Timers

- **Retry and poll through the timer helper** (`packages/client-core/src/timer.ts`): `repeat` for polls, and `nextDelay`, `wait` and `parseRetryAfter` for the rest. It uses a monotonic clock, keeps one run in flight, backs off with jitter, and never shortens a server's Retry-After.
  - Checks: no new `setInterval` (ratchet `set-interval`), and no Retry-After parsing outside the helper (ratchet `retry-after`).

## Cleanup

- **Cleanup never throws.** A failed dispose, close or release is logged, and the code carries on. It must not hide the error that led to the cleanup, or stop the releases after it.
  - Use `releaseQuietly`, `releaseAll` and `releaseThenRethrow` (`packages/client-core/src/cleanup.ts`).
  - Check: no new `catch (e) { x.dispose(); throw e; }` (ratchet `throwing-cleanup`).

## Server data on screens

- **A screen reads server documents through the viewer's query cache** (`packages/client-core/src/query`): keyed, read once however many owners ask, kept after the screen closes, and read again in place when the hub says something it was built from moved. Each viewer has its own cache, emptied with its reads aborted when the server, account, profile, role or privacy fence changes.
  - Use `useQuery` (web `platform/freshness.ts`) with keys from `queryKeys` and `meta.dependencies` from `dependsOn`. A change made on this device calls `moved()` with what it changed.
  - Don't compare fences between documents. A moved fence means "read again"; only a revision of the same entity is compared, for a write's precondition.
  - The ratchet doesn't count a shape or presence check (`typeof raw.viewerFence !== 'string'`, `=== undefined`) or a field read through a playback fence (`fence.sessionId`). A method or optional call on a fence (`viewerFence.trim()`) and a template built from one still count. A compare that must stay, such as a same-answer check, carries `// fence-compare: kept (reason)` as a real line comment on its own line. The reason is required, the marker inside a string, template or block comment spares nothing, and one marker spares one compare: a second compare on that line still counts.
  - Read a server document with a query function in `packages/client-core/src/query`, not with your own GET or a `new …Service(` outside it (ratchet `query-read`). The TypeScript checker does the counting (`ci/query_reads.mjs`, which needs the root `npm ci`; one run over a tree is shared by hygiene's unit test and check):
    - **Counted calls:** global `fetch`; a call resolved to a requester (a `request`, `requestRaw` or `requestConsole` with a `path` or `method` parameter, or any call with both); and an unresolved call with one of those names.
    - **How the method decides:** it's read from the `method` argument (`fetch`: `init.method`). Missing, `undefined` or `'GET'` in any case is a read. A literal, or a literal-typed value, naming another verb is a write and doesn't count. Anything not statically known counts as a read.
    - **Wrappers:** a wrapper counts once, at its own call to the requester, for all its callers. That's enough for a ratchet, which only has to stop new reads.
    - **Marker:** a read that must stay outside the cache (sign-in proving who the viewer is, the event feed, the cache's own transport) carries `// query-read: kept (reason)`, held to the same rules as the fence marker.
    - **Baseline:** the ratchet started at 887 reads in 366 files (step 7 slice 3), and each move into the cache lowers it.
  - Checks: no new fence comparisons (ratchet `fence-compare`), reads outside the query cache (ratchet `query-read`), page memory (ratchet `page-memory`) or per-screen lease hooks (ratchet `lease-layer`), and the privacy tests in `packages/client-core/test/viewer-queries.test.ts`, `web/test/viewer-queries-privacy.test.ts` and `apps/native/test/next-shell-freshness.test.ts`.

## API contracts

- **Responses decode open; unions are tagged.** Every response union has a `variant` (or `kind`) tag, unknown tags decode to a typed unknown, and requests decode exactly.
  - Check: apigen refuses an untagged response union, and refuses a grandfathered tag that no longer matches anything.
- **Generated files come from their scripts, never from hand edits.** That covers OpenAPI, contracts (including the development contracts) and route lanes.
  - Check: CI regenerates them and fails on any difference (`ci/run.py build`).

## Strings and privacy

- **Every user-facing string goes through the catalogue** (`packages/i18n`). New keys land in en-US only; other languages follow.
  - Check: `lint:strings`, including raw-error and glossary checks.
- **No external fonts or CDNs.** Nothing Portico ships may make a viewer's browser contact a font service or a public CDN. The only exception is Google's Cast SDK.
  - Check: `scripts/check-no-external-fonts.mjs` against a built web bundle. It isn't part of CI yet, because CI doesn't build the bundle.

## Database

- **Every write goes through the write gate and its budgets** (`server/internal/dbwork`).
  - Check: `TestWriteCensus` lists every write path and fails on an unreviewed one.

## Git and CI

- **Branch CI must be green before merging.** Merges go through the protected queue, rebase only, and nothing bypasses it.
  - Check: `ci/required` and branch protection.
- **Never force-push.** Rebased work goes to a new branch.
  - Check: branch protection.
- **Quarantine needs an owner and an expiry.**
  - Check: `ci/quarantine.json` validation; an expired entry fails CI.
- **No AI authors, co-authors or generated-by lines in commits.** If an AI tool helped, say so in the pull request description, as [CONTRIBUTING.md](../CONTRIBUTING.md) asks.
  - Check: `ci/commit_bylines.py` in the planner job. It looks only at authors, committers and trailer lines, never at what a message says.
