# Retired-device / revoked-family reader audit (issue #1913)

Scope: every canonical native read (`store/read`), writer read-before-write
statement (`store/writer`) and octet twin (`store/stmt/plan/*octets*.go`) that
reaches `identity_devices`, `authorization_session_families`,
`authorization_family_tokens`, `api_credentials` or a principal fence, on
origin/main and the RB, RS, UO and US train heads. Legacy `internal/{access,
identity,catalog,playbackv1,eventfeed,social,httpapi}` SQL is outside this audit.

## Live predicates (copy source)

- Device: `d.authority='local' AND d.retired_ms=0`
  (`usAuthorityDevice`, `usFamilyBirthDevice`, `usSecuritySessionSQL`,
  `usSecurityStatusSQL`).
- Family: `f.revoked=0` (schema `CHECK(teardown_due_ms IS NULL OR revoked=1)`, so a
  teardown-due family is always revoked and `revoked=0` covers it).
- Command 522 retires the credential's own API device and revokes its own family
  in one transaction; family birth requires `retired_ms=0`. A retired API device
  therefore never has a live family, but the device check is still required
  wherever a statement joins `identity_devices`.

## Classification

| # | Statement (file:line, RB/main) | Joins | Excludes retired / revoked | Class |
|---|---|---|---|---|
| 1 | `read.browse.principal` `browse_statements.go:58-70` | token, family, **device**, account, member, profile | revoked yes; **retired_ms was missing** | **MUST-FIX, fixed here** (`AND d.retired_ms=0`) |
| 2 | `foreground.mp.queue_head` `mp_queue_statements.go:14-21` | token, family (owner_id=f.device_id) | `f.revoked=0`, token retired/generation, pin_gate | OK-live: no device join; retired device implies revoked family (invariant above) |
| 3 | `foreground.mp.session_head` `mp_session_statements.go:13-22` | token, family (s.device_id=f.device_id) | same as 2 | OK-live (same reason) |
| 4 | RB home `library` `rb_home_statements.go:60-66` | token, family -> profile for viewer revision | none, derives profile only | OK-fenced: runs after #1 in the same snapshot (`publishPinnedAttempt`/`publishRead`) |
| 5 | RB saved-view `candidate` `rb_saved_view_statements.go:34-38` | token, family -> owner/share | none | OK-fenced (publishRead, fence compared first) |
| 6 | `uo_personal_statements.go:22` | token, family -> profile | none | OK-fenced (publishRead) |
| 7 | `uo_membership_statements.go:123`; twin `stmt/plan/uo_membership_octets.go:36` | token, family -> profile | none | OK-fenced (publishRead; twin is byte-identical sizing) |
| 8 | RS `rsSavedMembersSourceSQL`/`ShareSQL` `rs_saved_collection_statements.go:9,11`; `rsSavedDirectoryPermission{Source,Share}SQL` `rs_saved_directory_statements.go:34,36` | token, family -> owner/share | none | OK-fenced (all run inside publishRead/readBrowseFence, RS train) |
| 9 | UO activity head/personal/history `uo_activity_statements.go:18,39,63`; twins `uo_activity_octets.go:212,229` | token, family -> profile, membership/lifetime | none | OK-fenced (publishRead) (UO train) |
| 10 | UO operations token join `uo_operations_statements.go:15,72,92`; twins `uo_operations_octets.go:10,28,36,59` | token, family -> profile | none | OK-fenced (publishRead) (UO train) |
| 11 | UO operations devices keyset `uo_operations_statements.go:34-43`; twin `uo_operations_octets.go:18-21` | `personal_device_sequences` by profile (no `identity_devices` join) | none | **MUST-FIX unless desk rules it a historical sync-watermark ledger** (UO train; retention only drops a row when the device row is physically gone, so a logically retired device stays listed) |
| 12 | US `usSecuritySessionSQL/StatusSQL/StatusOctetsSQL` `us_security_statements.go:38,70,80`; twin `us_security_octets.go:18` | token, family, device | `d.retired_ms=0`, `f.revoked=0` | OK-live (US train) |
| 13 | US admin device page `us_security_statements.go:128,133`; twin `us_security_octets.go:42` | `identity_devices` by account | none (admin lists all) | being fixed separately on the US train; not touched |
| 14 | writer `usAuthorityFamily` `us_authority_statements.go:51`, `wcProgress` family `wc_progress_native.go:43` | token, family | `f.revoked=0`, token retired | OK-live |
| 15 | writer `usAuthorityDevice`, `usFamilyBirthDevice` | device | `retired_ms=0` | OK-live (reference predicate) |
| 16 | writer `usFenceDrainFamily`, `usSecretDrainSource`, `usCredential{Family,Device,Revoke}Read`, `wcSecurityCurrentDeny/HeaderBytes`, `wc_security_protocol` family/revoke reads | family/device/credential by id | deliberately read revoked/retired state | OK-historical: the revoke/drain/idempotent-replay protocol must see revoked rows |
| 17 | writer `mpWorkerOwner` `mp_worker_receipt_statements.go:29` | queue, family (f.id=?) | none | OK-historical: receipt reclaim for a device-owned queue must keep running after revoke |
| 18 | `usPolicyImageFamilyOctetsSQL` `stmt/plan/us_policy_image_octets.go:21` | family by id (sizing only) | none | OK-historical: sizing twin; liveness is proven by #14 in the same transaction |
| 19 | `rb_home_manifest_statements.go:25-33` | preference scope with `device_key` bound to `""` | n/a | OK: profile scope, no device |

All paged readers (#4-#10) run behind #1 in the same read snapshot; a continuation
re-runs the fence, so a retired device publishes nothing mid-traversal.

## Fix and proof (RB train)

- `browse_statements.go`: `read.browse.principal` now has `AND d.retired_ms=0`
  (keyed PK SEARCH on `identity_devices`, no new join, no octet twin exists for it).
- Test `retired_device_principal_test.go`
  `TestBrowsePrincipalExcludesRetiredDeviceAndRevokedFamily`: retired device with
  live family; revoked + teardown-due family with live device; the exact 522 state
  (both); a retired device's continuation mid-traversal. Each case also proves a
  sibling live device/token of the same profile still traverses the whole library
  and equals a plain-SQL oracle (`catalog_entities` of the library, not retired),
  at limits 40 and 7 (no hole, exact EOF). Plain-SQL fixtures only.
- Mutation check by reading: removing `d.retired_ms=0` makes the
  `retired-device-live-family` case return a page instead of `ErrDenied`.
- Goldens re-recorded: `read/testdata/browse-plans.json.gz` (`TestBrowsePlans
  -record-browse-plans`) and `read/testdata/native-full-search-plans.json.gz`
  (`TestNativeSearchPlans`, which embeds the same principal statement). Both are
  under the E golden hold and are re-recorded again after E lands.
