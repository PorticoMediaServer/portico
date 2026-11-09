# UO bounded reads and class handoff (UO-22)

This file is the handoff for the UO read families added by UO-22. It adds no
mutation command ID and no product wiring. The code is harness-only and inert
until a later phase selects it.

## What this commit adds

- `read/uo_activity.go` + `read/uo_activity_statements.go`
- `read/uo_operations.go` + `read/uo_operations_statements.go`
- `stmt/plan/uo_activity_octets.go`, `stmt/plan/uo_operations_octets.go`
- `read/uo_activity_test.go`, `read/uo_operations_test.go`, `read/uo_read_plans_test.go`

Both windows use the existing `publishRead`/`liveRead` publication protocol. The
shared fence is re-read in the fresh final snapshot. Every variable payload has a
same-snapshot `octet_length` preflight whose exact statement is registered in
`stmt/plan`; the actual bounded octets are reserved against the caller's existing
output grant before any payload query. No statement is an INSERT, UPDATE or
DELETE.

## Read families

| Family | Source | Keyset | Raw cap | Delivered cap |
| --- | --- | --- | ---: | ---: |
| `UOActivityRecent` | `personal_current_recent` (`last_played_at`) | `(time DESC, item_public_id ASC)` | 64 | 40 |
| `UOActivityProgress` | `personal_current_activity` (`activity_at`) | `(time DESC, item_public_id ASC)` | 64 | 40 |
| `UOActivityCompleted` | `personal_current_completed` (`last_completed_at`) | `(time DESC, item_public_id ASC)` | 64 | 40 |
| `UOActivityHistory` | `play_history_admin_user` | `(occurred_at DESC, id DESC)` | 64 | 40 |
| `UOOperationsConflicts` | `personal_conflicts_profile` | `(profile_id, id ASC)` | 64 | 40 |
| `UOOperationsDevices` | `personal_device_sequences` PK | `(profile_id, device_id ASC)` | 64 | 40 |
| `UOOperationsJobs` | `personal_jobs` PK point | exact `id` | 1 | 1 |
| `UOOperationsJobDue` | `personal_jobs_governor_due` | `(job_due_ms, rowid)` | 1 | 1 |
| `UOOperationsSettings` | singleton + `server_configuration_pending` PK | `field ASC` | 64 | 40 |
| `UOOperationsDeliveryLog` | `webhook_deliveries_log` | `(created_ms DESC, id DESC)` | 100 raw (`65` bound) | 40 |

Every window binds `cap+1`, checks the raw count against the cap, and keeps a
stable values-only keyset. Personal families are scoped by the current viewing
token's family/profile and by the current library/kind fence; another profile,
library or kind consumes a raw position and never becomes an item. History rows
also have to match the current membership incarnation, profile lifetime and
activity generation, so a retired lifetime cannot disclose a retained occurrence.
Administrator families use a private principal-scope read recipe that requires
the current owner/admin role in both the lookup and the publication snapshot.

## Explicit producer refusals

Some sources have no landed writer on this train. Rather than derive a value from
an unrelated flag or timestamp, the public entry point refuses with
`ErrUOSourceNotLanded` and a `UOProducerPending` that names the task:

| Request | Missing writer | Error |
| --- | --- | --- |
| `UOActivityInput.EffectiveWatched` | UO-05 explicit watched intent | `UOProducerPending{Task:"UO-05"}` |
| `UOOperationsInput.IncludeOutcomes` | UO-08 per-item job outcomes | `UOProducerPending{Task:"UO-08"}` |

The effective-watched fold itself is a pure values-only helper
(`foldUOActivityWatched`) and is unit-tested without a producer. UO-06 (offline
conflicts) and UO-07 (activity/clear-history) rows are read as current schema
facts only; this task neither fabricates a conflict resolution nor a clear
generation. A clear generation is honoured only when the current
`personal_activity_revisions` row exists; an absent head is the ordinary
empty-head case.

## Class-target handoff

The shared class-target kit (`writer/CLASS_TARGET_KIT.md`) is the one way to
declare a qualification class. At this train head the closed registry contains
exactly one UO target:

| Merged UO artifact | Class target | Kind | Fixture |
| --- | --- | --- | --- |
| `read/uo_personal.go`, `UOPersonalDefinitions` | `uo.personal` | `foreground_read` | foreground fixture + live HN Capture |
| this commit's activity families | `uo.activity` (to be declared by the desk train run) | `foreground_read` | foreground fixture + live HN Capture; 25 definitions |
| this commit's operations families | `uo.operations` (to be declared by the desk train run) | `foreground_read` | foreground fixture + live HN Capture; 18 definitions |

The UO-03 flags command and the UO-12/UO-13 notice commands are merged
`normal_write` commands but do not yet have a canonical class target in
`writer/canonical_held_targets_test.go`. UO-22 does not invent mutation IDs or a
second class registry; adding those targets changes the shared writer test
registry and its hard-coded inventory, so it is left to the desk's train
handoff. No class target may be duplicated: each merged command owns exactly one.

## Phase4 retirement map

The read families here replace these legacy surfaces when phase 4 selects the
new layer. Nothing is deleted by this commit.

| Legacy surface | Replacement |
| --- | --- |
| `catalog/personal*.go`, `catalog/rec_feedback.go` activity/state reads | `UOActivity*` current families; `read/uo_personal.go` point |
| `personalstate/watched.go`, `catalog/personal_activity.go` history reads | `UOActivityHistory` + `foldUOActivityWatched` |
| `operations` settings/conflict/device/job reads | `UOOperationsConflicts/Devices/Jobs/JobDue/Settings` |
| `notify` inbox/acknowledgement reads | UO-13/UO-14 read families (`read/uo_notice_state.go`, `read/uo_inbox*`) |
| `apievents`/`eventfeed`/`httpapi` event adapters | UO-18/UO-19 (`read/uo_event_native*`, `read/uo_event_transport.go`) |
| webhook delivery engine/log | UO-16/UO-17 (`read/uo_webhook*`, `read/uo_service_outbox*`) |

The map is a retirement checklist, not a claim that the legacy code is gone or
that the new route is wired.

## Operations devices page: historical sync-watermark ledger (desk ruling, issue #1913)

`uo_operations_statements.go` lists `personal_device_sequences` by profile with no
`identity_devices` join on purpose. The table is the offline-replay watermark ledger
(legacy `catalog/personal.go`: `Sequence <= watermark` gives `ErrOperationExpired`), and
retention drops a row only after the device row is physically gone plus 30 days. A device
logically retired by command 522 (`retired_ms>0`) therefore stays listed exactly like a
legacy-removed device does today, and hiding it would hide a watermark that still guards
replays. The page exposes only an opaque device id and a sequence number scoped to the
fenced profile. Do NOT add a retired-device join here; if a product device LIST is wanted
it must come from an `identity_devices` reader that excludes `retired_ms>0`.
