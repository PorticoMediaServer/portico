# Search bank compatibility and Go toolchain rebuild

The prepared search bank's `unicode_fingerprint` is derived from the Unicode
tables selected by the running Go build, not from a module version. A bank
prepared by one toolchain therefore does not compare current under another one,
even when the recipe, the SQLite engine and the `x/text` pin are unchanged.

This is a runbook for that mismatch and the only supported way to move a bank to
a new toolchain. It is a contract, not an implementation: the writer and
ingestion own the rebuild, the flip and native retirement.

## What the fingerprint covers

`internal/store/internal/searchpolicy` owns the compiled tuple stored on
`search_generations`:

| Column | Value |
| --- | --- |
| `recipe_version` | `A-CJK-uni-bi-v1` |
| `engine_version` | SQLite version, module version and exact source id |
| `unicode_fingerprint` | `portico-search-profile-sha256-v1:` and a SHA-256 over the recipe, engine descriptor, the compiled Go Unicode version, the compiled `x/text` normalization version, the `x/text` module and checksum, the preparation-source digest and the native-format digest |

`searchpolicy.Compare(stored, actual)` reads the actual physical engine and
returns `Current` only when the stored tuple equals the running build's
`Compiled()` tuple and the engine is attested. `Stale` and `Unavailable` are
routing evidence, never a permission.

## The toolchain pin

`server/go.mod` declares `go 1.26.10`, and `.github/workflows/lane-focused.yml`
runs tests on that toolchain. The pin stays until a reviewed rebuild path is
merged. Do not:

- rewrite `search_generations.unicode_fingerprint` (or any tuple column) to make
  an existing bank look current;
- run a new toolchain against the old bank and treat the empty result as an
  ordinary empty search;
- re-record a source or native pin without the owner's rebuild review.

`TestSearchBankUpgradeContractPinnedToolchain` fails when the `go` directive
moves, and `TestSearchBankUpgradeContractFingerprintAloneDoesNotPublish` proves
that an unbuilt generation cannot be activated by supplying the new fingerprint.

## Reading across a toolchain change

`read.nativeReadFence` answers `SearchUnavailable` when the active generation's
stored tuple is not `Current` on the actual engine. That page contains no cards
and makes no empty or exhaustive claim. `SearchPending` is reserved for an active
generation with outstanding subjects; a building generation is never readable.

After a Go upgrade, expect:

- the old bank to refuse with `SearchUnavailable` on the new build;
- the old accepted bank to keep serving only where a build whose `Compiled()`
  equals its stored tuple still runs against the compatible engine;
- no partial cards, counts, or "no results" while the new generation builds.

## The rebuild path

1. **Keep serving.** Leave the old accepted generation active and old build (or
   pinned toolchain) serving while the new generation is prepared. Search is
   degraded, not rewritten, on the new build.
2. **Request the owner rebuild.** Create one building generation with the new
   toolchain's `Compiled()` tuple. `search_revision.building_generation_id`
   carries the build; only one generation builds at a time, and every publisher
   command samples the engine from its own held, policy-verified connection.
3. **Bounded build and dual publication.** Prepare and publish subjects in the
   existing bounded commands. Input intents bind to the building generation
   (`search.publish.cas.input_sequence_building`) while it exists; head
   publication against a building bank is immutable prepared work and does not
   change the serving revision.
4. **Complete, then flip.** A generation is complete only when every expected
   subject is staged and verified (`search_image_seals`:
   `staged = verified = expected`, `sealed = 1`) and `pending_subjects = 0`.
   Only the owner's global activation may then flip
   `search_revision.active_generation_id` and retire the old generation. There
   is no flip for a partial, unsealed, or merely re-fingerprinted generation.
5. **Reader-pinned retirement.** The retired generation's publications,
   fragments and images are reclaimed in bounded native jobs only after every
   reader that pinned them has released and joined. Never reclaim the active
   generation or a pinned generation.

Restart is safe at every step: `Compare` is deterministic from the persisted
tuple, the actual engine and the running build, and the building generation
resumes from its persisted phase. A restart does not create a second profile.

## Downgrade and rollback

The refusal is symmetric. A bank prepared by the newer toolchain refuses under
the older one. To roll back, run the build whose compiled tuple matches the
old accepted generation again; do not edit the bank. If the new build cannot
finish, the accepted bank stays active and the new build keeps answering
`SearchUnavailable` until it either completes or the binary is reverted.

## Owner implementation gate

RS-19 records the contract and tests only. The following remain with the writer
and ingestion owners and are not provided here:

- the rebuild request and resumable bounded build on a new toolchain;
- the dual-publication binding and the global activation/flip after completion;
- reader-pinned native retirement and reclaim for the retired generation;
- any receipt, durability or command-ID surface those commands need.

No new SQL is introduced by this contract. `search_generations`,
`search_revision`, `search_publications`, `search_image_seals`,
`search_reclaim_jobs` and the existing publisher statements are sufficient.

## Test

`server/internal/store/search/bank_upgrade_contract_test.go` exercises the
merged `searchpolicy.Compare` against the real bundled engine and models the
upgrade, downgrade, mismatch, restart, complete-only flip and reader-pinned
retirement. It reads `server/go.mod` for the pin and is declared in
`ci/source-readers.json`.
