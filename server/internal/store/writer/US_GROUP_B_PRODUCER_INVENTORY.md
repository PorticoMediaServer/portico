# Group B producer inventory (CHANGE-015)
Census at public main `9c7df4a2a5f5c4a94edcb209cf79d9096422f1b5` (8 Nov 2026). Schema group B is adopted but not embedded, so this file records the producers only; it changes no schema.
Group B adds `authorization_session_families.security_kind INTEGER NOT NULL` with no default (0 ordinary, 1 credential), `identity_devices.retired_ms`, restrictive foreign keys from `authorization_family_security_fences` and `security_credential_secrets`, and the `identity_refresh_receipts` family uniqueness rule. Every producer below must choose a concrete kind when the embed PR lands. Credential producers write 1; ordinary producers write 0. Nothing may infer a kind or rely on a default.
## How to read and maintain this table
- `v1-native`: the statement runs against the store's own schema opener (`server/internal/store/...`) and is exercised by `store/*` tests. These are the sites the P05 and P06 prep tasks update.
- `legacy`: the phase-1a persistence baseline (`identity`, `access`, `persistence`, `testauth`, the baseline-schema script) and its fixtures/cutover references. Legacy stays unchanged; it is listed so the inventory is complete.
- `ordinary` / `credential` / `ordinary+credential` records which kind of family, device or credential row the site creates or removes.
- `server/internal/store/writer/us_group_b_producer_inventory_test.go` (prefix `TestUSGroupBProducerInventory`) enforces this table: every Go statement site in the server module is matched by file, operation, table and the first 160 bytes of whitespace-collapsed SQL. A new unlisted site fails the test; a changed statement shows the new SQL in the failure message. Update this table and the allowlist together.
- The census scans every server Go file, production and test, including inactive build tags. It skips its own file so its prefixes are not counted twice.
- `TestUSGroupBProducerInventorySchemaGuards` enforces the P08 guard list below: it scans text files under `server/` and `docs/` for the three known schema SHAs and for the writer crash schema guard references, and fails on an unlisted guard carrying the 014 SHA.
- Pending entries are matched by file, not by SQL prefix, while their candidate branch is unmerged (see below); the merging task replaces each one with exact statement entries.

## Sites

| # | Site | Op | Table | Class | Purpose | Fixture | Note |
|---|---|---|---|---|---|---|---|
| 1 | `server/internal/access/access_test.go:1250` | insert | `identity_devices` | legacy | ordinary | - | Legacy access fixture device |
| 2 | `server/internal/access/access_test.go:1355` | insert | `identity_devices` | legacy | ordinary | - | Legacy access fixture count device |
| 3 | `server/internal/access/access_test.go:1372` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy access fixture member family |
| 4 | `server/internal/access/credentials.go:279` | insert | `api_credentials` | legacy | credential | - | Legacy top-level API credential issue |
| 5 | `server/internal/access/credentials.go:454` | delete | `api_credentials` | legacy | credential | - | Legacy expired/revoked API credential sweep |
| 6 | `server/internal/access/integration_credentials.go:123` | insert | `api_credentials` | legacy | credential | - | Legacy integration credential issue |
| 7 | `server/internal/catalog/play_history_facts_test.go:20` | insert | `identity_devices` | legacy | ordinary | - | Legacy catalog play-history fixture device |
| 8 | `server/internal/catalog/play_history_facts_test.go:21` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy catalog play-history fixture family |
| 9 | `server/internal/dbwork/authority_test.go:29` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy dbwork authority fixture family |
| 10 | `server/internal/dbwork/authority_test.go:33` | delete | `api_credentials` | legacy | credential | - | Legacy dbwork authority fixture credential cleanup |
| 11 | `server/internal/dbwork/authority_test.go:117` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy dbwork minimal fixture family |
| 12 | `server/internal/hosted/trust_revocation_test.go:55` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy hosted trust fixture family |
| 13 | `server/internal/httpapi/playback_v1_queue_review_test.go:202` | delete | `identity_devices` | legacy | ordinary | - | Legacy playback review fixture device cleanup |
| 14 | `server/internal/httpapi/restriction_matrix_test.go:1127` | insert | `identity_devices` | legacy | ordinary | - | Legacy restriction matrix fixture device |
| 15 | `server/internal/identity/api_credentials.go:163` | insert | `identity_devices` | legacy | credential | - | Legacy credential device birth |
| 16 | `server/internal/identity/api_credentials.go:168` | insert | `authorization_session_families` | legacy | credential | - | Legacy credential family birth |
| 17 | `server/internal/identity/api_credentials.go:217` | delete | `identity_devices` | legacy | credential | - | Legacy credential device cleanup |
| 18 | `server/internal/identity/credential_security_test.go:356` | insert | `identity_devices` | legacy | ordinary | - | Legacy credential-security fixture device |
| 19 | `server/internal/identity/device_contract_test.go:57` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy expired-family fixture copy (INSERT-SELECT) |
| 20 | `server/internal/identity/devices.go:147` | insert | `identity_devices` | legacy | ordinary | - | Legacy interactive device birth (upsert) |
| 21 | `server/internal/identity/devices.go:502` | delete | `identity_devices` | legacy | ordinary | - | Legacy device removal |
| 22 | `server/internal/identity/families.go:168` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy ordinary family birth |
| 23 | `server/internal/identity/list_limits_test.go:29` | insert | `identity_devices` | legacy | ordinary | - | Legacy list-limit fixture device |
| 24 | `server/internal/identity/list_limits_test.go:33` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy list-limit fixture family |
| 25 | `server/internal/identity/member_erasure.go:191` | delete | `identity_devices` | legacy | ordinary+credential | - | Legacy member-erasure device sweep (includes credential devices) |
| 26 | `server/internal/identity/profile_purge_test.go:737` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy profile-purge fixture family |
| 27 | `server/internal/identity/prune_sessions.go:132` | delete | `authorization_session_families` | legacy | ordinary+credential | - | Legacy session prune family delete |
| 28 | `server/internal/identity/service.go:286` | delete | `identity_devices` | legacy | ordinary | - | Legacy retired-device cleanup with live-family guard |
| 29 | `server/internal/mediaanalysis/target_test.go:34` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy media-analysis fixture family |
| 30 | `server/internal/persistence/owner_completion_schema_test.go:32` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy owner-completion schema fixture family |
| 31 | `server/internal/persistence/owner_completion_schema_test.go:89` | delete | `authorization_session_families` | legacy | ordinary | - | Legacy owner-completion fixture cleanup |
| 32 | `server/internal/persistence/trigger_plan_test.go:25` | delete | `identity_devices` | legacy | ordinary | - | Legacy trigger/plan statement key |
| 33 | `server/internal/playbackdisc/family_continuation_test.go:22` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy playback continuance fixture family |
| 34 | `server/internal/playbackdisc/family_continuation_test.go:25` | insert | `identity_devices` | legacy | ordinary | - | Legacy playback continuance fixture device |
| 35 | `server/internal/playbackdisc/family_continuation_test.go:94` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy playback continuance second-family copy (INSERT-SELECT) |
| 36 | `server/internal/playbackv1/query_capture_test.go:130` | insert | `identity_devices` | legacy | ordinary | - | Legacy playback query fixture device |
| 37 | `server/internal/playbackv1/queue_sweep_race_test.go:14` | insert | `identity_devices` | legacy | ordinary | - | Legacy queue-sweep fixture device |
| 38 | `server/internal/reading/session_limit_test.go:30` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy reading session-limit fixture family |
| 39 | `server/internal/store/internal/readfixture/source.go:206` | insert | `identity_devices` | v1-native | ordinary | P06 | V1 readfixture device |
| 40 | `server/internal/store/internal/readfixture/source.go:207` | insert | `authorization_session_families` | v1-native | ordinary | P06 | V1 readfixture ordinary family |
| 41 | `server/internal/store/read/rb_filter_personal_cursor_fence_test.go:28` | insert | `identity_devices` | v1-native | ordinary | P06 | V1 RB personal-fence fixture device |
| 42 | `server/internal/store/read/rb_filter_personal_cursor_fence_test.go:29` | insert | `authorization_session_families` | v1-native | ordinary | P06 | V1 RB personal-fence fixture ordinary family (INSERT-SELECT) |
| 43 | `server/internal/store/read/rs_detail_composed_test.go:170` | insert | `identity_devices` | v1-native | ordinary | P06 | V1 RS detail-composed profile-q fixture device |
| 44 | `server/internal/store/read/rs_detail_composed_test.go:171` | insert | `authorization_session_families` | v1-native | ordinary | P06 | V1 RS detail-composed profile-q fixture family (INSERT-SELECT) |
| 45 | `server/internal/store/stmt/plan/autoindex_fences_test.go:96` | insert | `identity_devices` | v1-native | ordinary | P06 | V1 plan autoindex fence fixture device |
| 46 | `server/internal/store/stmt/plan/autoindex_fences_test.go:97` | insert | `authorization_session_families` | v1-native | ordinary | P06 | V1 plan autoindex fence fixture ordinary family (INSERT-SELECT) |
| 47 | `server/internal/store/writer/crash_security_linux_test.go:204` | insert | `authorization_session_families` | v1-native | ordinary | P05 | V1 WC security crash fixture ordinary family copy (INSERT-SELECT) |
| 48 | `server/internal/store/writer/us_authority_fixture_test.go:83` | insert | `identity_devices` | v1-native | ordinary | P05 | V1 US owner-authority fixture device |
| 49 | `server/internal/store/writer/us_authority_fixture_test.go:86` | insert | `authorization_session_families` | v1-native | ordinary | P05 | V1 US owner-authority fixture ordinary family (security_kind explicit) |
| 50 | `server/internal/store/writer/us_authority_test.go:87` | insert | `api_credentials` | v1-native | credential | P05 | V1 US owner-authority credential marker fixture |
| 51 | `server/internal/store/writer/us_authority_test.go:95` | delete | `api_credentials` | v1-native | credential | P05 | V1 US owner-authority credential marker drain |
| 52 | `server/internal/store/writer/us_pin_fences_test.go:57` | insert | `authorization_session_families` | v1-native | ordinary | P05 | V1 US PIN fence fixture ordinary family (security_kind=0) |
| 53 | `server/internal/store/writer/wc_progress_fixture.go:38` | insert | `authorization_session_families` | v1-native | ordinary | P05 | V1 WC progress fixture ordinary family birth |
| 54 | `server/internal/store/writer/wc_security_008_native_test.go:17` | insert | `authorization_session_families` | v1-native | ordinary | P05 | V1 WC security native fixture ordinary family |
| 55 | `server/internal/testauth/session.go:22` | insert | `identity_devices` | legacy | ordinary | - | Legacy generic test-session device |
| 56 | `server/internal/testauth/session.go:23` | insert | `authorization_session_families` | legacy | ordinary | - | Legacy generic test-session family |

## Pending v1-native fixtures (unmerged branches)

These files exist only on candidate branches and use test-only B subsets. The census matches them by file until their PR merges, so the embed work does not fight a moving branch. All are P05; each merging task replaces its pending entry with exact op/table/prefix entries. A pending file may gain sites before it merges, which is why the marker is per file.

| File | PR | Sites at that PR head | Purpose at embed |
|---|---|---|---|
| `server/internal/store/writer/us_family_birth_test.go` | #1809 | device insert, two api_credentials inserts plus their deletes, ordinary family insert | ordinary family birth; credential marker fixtures write 1 |
| `server/internal/store/writer/us_family_fence_drain_test.go` | #1810 | device insert, ordinary family insert, three family deletes | ordinary family and fence drain |
| `server/internal/store/writer/us_credential_secret_drain_test.go` | #1812 | family insert, api_credentials insert, three api_credentials deletes | credential row and its family write 1 |
| `server/internal/store/writer/us_refresh_receipt_compat_test.go` | #1813 | one family insert reused for two families | ordinary refresh families write 0 |

## SCHEMA SHA GUARDS (P08-owned, hash-only edits)

Ownership: P05 and P06 change only the INSERT/producer fixtures above. The schema-guard edits are applied by P08 (embed owner) after P05/P06, in the embed PR. No producer task edits these files.

At this census head the public embedded schema pins `9d9aa39d...` (retained schema012+013). CHANGE-014's adopted schema SHA `782904d7...` is carried by PR #1803, which is not on this base; after it merges the same guard sites carry `782904d7...`. P08 then rewrites them to group B's `44411dfe...` (`44411dfefac6bf6f285040302676807cb4e9d14dc28a0cb6a7468cdca98d7a97`). The census test accepts any of the three literals at a listed site, so it stays true across both embeds, and fails if any unlisted file gains the 014 SHA. None of these files is edited by this task.

| Site | Old literal / reference | Guards | Hash-only at P08? |
|---|---|---|---|
| `server/internal/store/schema/schema.go:11` | `9d9aa39d...` (`const SHA256`) | embedded `schema.sql` SHA | yes |
| `server/internal/store/schema/SHA256SUMS:1` | `9d9aa39d...` | `schema.sql` checksum line | yes |
| `server/internal/store/schema/INT-LARGE-INDEXES.json:2` | `9d9aa39d...` (`ddl_sha256`) | large-index contract; the index set also moves with group B foreign keys | no |
| `server/internal/store/schema/README.md:208` | `9d9aa39d...` (written as `SQL9d9aa39d`) | prose pin of the embedded schema | no |
| `server/internal/store/writer/QUALIFICATION_POPULATION.md:21` | `9d9aa39d...` | `canonical-movies-v1` shape pin | no |
| `server/internal/store/writer/qualification_population_seed_test.go:86` | `9d9aa39d...` | qualification population shape guard | yes |
| `server/internal/store/writer/scan_run_lifecycle_plan_test.go:36` | `9d9aa39d...` | run/lifecycle plan capture guard | yes |
| `server/internal/store/writer/scan_fresh_plan_test.go:57` | `9d9aa39d...` | scan fresh plan capture guard | yes |
| `server/internal/store/writer/crash_progress_linux_test.go:19` (message at 91) | `const progressCrashSchema010 = 9d9aa39d...`; `require merged010/#1267` | progress crash registry schema guard | no (constant name and message) |
| `server/internal/store/writer/crash_reclaim_linux_test.go:186` | `progressCrashSchema010`; `require schema010` | reclaim crash registry schema guard | no (message) |
| `server/internal/store/writer/crash_security_linux_test.go:121` | `progressCrashSchema010`; `require schema010` | security crash registry schema guard | no (message) |
| `server/internal/store/stmt/plan/testdata/canonical.json:23` | `"sha256": 9d9aa39d...` | canonical plan evidence | no (evidence refresh) |
| `server/internal/store/stmt/plan/testdata/issuer-source-octets.json:23` | `"sha256": 9d9aa39d...` | issuer source octet evidence | no (evidence refresh) |
| `server/internal/store/stmt/plan/testdata/issuer-library-root-octets.json:23` | `"sha256": 9d9aa39d...` | issuer library-root octet evidence | no (evidence refresh) |
| `server/internal/store/stmt/plan/testdata/n1-record-octets.json:23` | `"sha256": 9d9aa39d...` | N1 record octet evidence | no (evidence refresh) |
| `server/internal/store/stmt/plan/testdata/ia-audio-octets.json:33` | `9d9aa39d...` in the implementations note | IA audio octet evidence | no (evidence refresh) |

## Non-Go sites

| Site | Op | Table | Class | Note |
|---|---|---|---|---|
| `server/scripts/check_p10_sqlite.py:58` | insert | `authorization_session_families` | legacy | Python baseline-schema test fixture; leave unchanged |
| `server/internal/store/writer/testdata/wc-progress-composition.json:17172` (vm-capture evidence) | insert | `authorization_session_families` | generated evidence | Generated WC progress capture; its call site is `wc_progress_fixture.go`, so it is not a producer |

## Disjoint fixture lists for prep tasks

Each list is disjoint. A prep task updates only its own files; no product schema is edited here.

### P05 - WC and native writer fixtures

- `server/internal/store/writer/wc_progress_fixture.go` - ordinary family birth (line 38)
- `server/internal/store/writer/wc_security_008_native_test.go` - ordinary family fixture (line 17)
- `server/internal/store/writer/crash_security_linux_test.go` - ordinary family copy, INSERT-SELECT (line 204)
- `server/internal/store/writer/us_authority_fixture_test.go` - device and ordinary family fixture (lines 83, 86; family already names `security_kind`)
- `server/internal/store/writer/us_authority_test.go` - credential marker insert and drain (lines 87, 95)
- `server/internal/store/writer/us_pin_fences_test.go` - ordinary family fixture with `security_kind=0` (line 57)
- `server/internal/store/writer/us_family_birth_test.go` - pending, lands with PR #1809
- `server/internal/store/writer/us_family_fence_drain_test.go` - pending, lands with PR #1810
- `server/internal/store/writer/us_credential_secret_drain_test.go` - pending, lands with PR #1812
- `server/internal/store/writer/us_refresh_receipt_compat_test.go` - pending, lands with PR #1813

### P06 - read, plan and readfixture fixtures

- `server/internal/store/internal/readfixture/source.go` - device and ordinary family fixture (lines 206-207)
- `server/internal/store/read/rb_filter_personal_cursor_fence_test.go` - device and ordinary family fixture (lines 28-29)
- `server/internal/store/read/rs_detail_composed_test.go` - device and ordinary family fixture (lines 170-171)
- `server/internal/store/stmt/plan/autoindex_fences_test.go` - device and ordinary family fixture (lines 96-97)

## Notes for the embed task

- The P05/P06 fixtures are the only v1-native sites at this head. Credential fixtures exist only in `us_authority_test.go` at present; US-11 adds the first accepting credential producer.
- `internal/testauth/session.go` is legacy today: no `store/*` test calls it. If a v1 fixture later adopts it, add a v1-specific helper that writes `security_kind` explicitly rather than inferring it (CHANGE-015 producer-impact rule).
- The family `INSERT-SELECT` sites copy an existing family row, so they inherit the source kind. They still must stay listed because the copied row may be ordinary or credential.
