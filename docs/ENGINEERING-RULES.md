# Engineering rules

Short rules that keep Portico from repeating old bugs. Each one says how it's enforced. Where a check only stops things getting worse, its counts live in `ci/engineering-rules-baseline.json` as upper bounds. PR and merge-queue checks also scan the actual comparison base: no per-file count may grow, even below a stale-high bound. Decreases pass without editing the baseline; lowering it with `python3 ci/engineering_rules.py --update` is optional.

Both ratchets share the planner's comparison range. PRs use their merge base with the target; queue groups also compare with their immediate predecessor. Every first-parent entry is checked against its parent, so a later removal cannot hide an earlier increase. The client API ratchet compares only the total direct calls outside `packages/client-core`; see [client-api-ownership.md](client-api-ownership.md). Engineering rules below keep their per-file upper bounds, lowered with `python3 ci/engineering_rules.py --update`.

## Sign-in and authority

- **Bind to the sign-in, never the token.** An operation that spans several steps (a setup, an owner move, an email change, a deletion) keeps the account, its auth epoch and its session family. A routine refresh rotates the token inside the same family, and the operation carries on. An epoch change, a revocation, or another family, account or mode refuses it.
  - Server: a renewal rotates the refresh token inside the same sign-in; an older refresh token used again ends it, and a token that is no longer good is answered `401` (`server-next/API.md`, "Sign-in").
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
  - Read server documents through the viewer query cache in `packages/client-core/src/query`. Direct server calls outside client-core are counted by the client API ratchet.
  - Checks: no new fence comparisons (ratchet `fence-compare`), page memory (ratchet `page-memory`) or per-screen lease hooks (ratchet `lease-layer`), and the privacy tests in `packages/client-core/test/viewer-queries.test.ts`, `web/test/viewer-queries-privacy.test.ts` and `apps/native/test/next-shell-freshness.test.ts`.

## API contracts

- **Contracts come from the server's route table, never from hand edits.** `go run ./cmd/portico-contracts` in `server-next` writes `packages/contracts/src/next/api.ts` and `server-next/api/openapi.json` from the routes and their Go types.
  - Check: `TestContractsAreCurrent` (run in merge CI with `CONTRACTS_CHECK=1`) fails when the committed files differ from what the route table makes.
  - The older files beside them in `packages/contracts/src` (`routes.ts`, `schemas.ts`, `types.ts` and the rest) are a frozen copy from the previous server that nothing regenerates. Use `next/api.ts` for anything new.

## Strings and privacy

- **Every user-facing string goes through the catalogue** (`packages/i18n`). New keys land in en-US only; other languages follow.
  - Check: `lint:strings`, including raw-error and glossary checks.
- **No external fonts or CDNs.** Nothing Portico ships may make a viewer's browser contact a font service or a public CDN. The only exception is Google's Cast SDK.
  - Check: `scripts/check-no-external-fonts.mjs` against the built web bundle in the web static job (`ci/run.py`).

## Database

- **Only `internal/db` runs SQL, through one writer.** People's actions go ahead of background work, and one request is one transaction (`server-next/RULES.md`, rules 1-9).
  - Check: `server-next/budget_test.go` holds the table, trigger, `CHECK`, file and data-layer budgets. The one-writer and SQL-only-in-`internal/db` rules are kept by review.

## Git and CI

- **Branch CI must be green before merging.** Merges go through the protected queue, rebase only, and nothing bypasses it.
  - Check: `ci/required` and branch protection.
- **Never force-push.** Rebased work goes to a new branch.
  - Check: branch protection.
- **Quarantine needs an owner and an expiry.**
  - Check: `ci/quarantine.json` validation; an expired entry fails CI.
- **No AI authors, co-authors or generated-by lines in commits.** If an AI tool helped, say so in the pull request description, as [CONTRIBUTING.md](../CONTRIBUTING.md) asks.
  - Check: `ci/commit_bylines.py` in the planner job. It looks only at authors, committers and trailer lines, never at what a message says.
