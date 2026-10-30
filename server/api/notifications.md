# Notifications and viewer feedback

A guide for third-party client authors. The normative contract is
the notification handlers and generated registry contract; this file explains
how to use them. The hand-written YAML is historical.

Requests need `Authorization: Bearer <access token or scoped credential>`.
Owner administration routes require owner authority and the route’s scope;
session-only operations still refuse keys and tokens.

## Revisions and stream positions

Every inbox carries a monotonic `revision`. Use it as `expectedRevision` when
changing the inbox. It advances for new notices, read receipts, archives and
retention prunes. A batch that changes nothing does not advance it.

The shared `/v1/events` feed has a separate decimal event position. Resume SSE
with `Last-Event-ID`, or use `?after=<event-id>&waitMs=25000` for the long-poll
fallback. A `notification.changed` hint carries the inbox revision; quietly
reread the inbox and counts. Never use the inbox revision as the feed position.

## Reading the inbox

```
GET /v1/notifications/inbox?audience=all&state=unread&limit=40
```

One request returns the page, the counts, the revision, the retention window and
which audiences you may read. There is no second call to render an inbox.

```json
{
  "scope": { "serverId": "…", "viewerFence": "…" },
  "data": {
    "revision": 41,
    "audience": "all",
    "audiences": ["profile", "account-admin"],
    "state": "unread",
    "counts": { "unread": 2, "read": 6, "archived": 3, "total": 11 },
    "items": [
      {
        "id": "8f2c…",
        "audience": "profile",
        "severity": "info",
        "source": "downloads",
        "category": "download.finished",
        "title": "Download ready",
        "body": "The Third Man finished downloading and is ready to play offline.",
        "arguments": { "title": "The Third Man", "downloadId": "dl-19" },
        "actions": [
          { "kind": "command", "label": "Open", "command": "open-download",
            "arguments": { "downloadId": "dl-19" } }
        ],
        "dedupeKey": "download:dl-19",
        "revision": 41,
        "createdAt": 1789564800000,
        "updatedAt": 1789564800000,
        "expiresAt": 1805116800000,
        "readAt": null,
        "archivedAt": null,
        "read": false,
        "archived": false,
        "cursor": "12"
      }
    ],
    "nextCursor": "",
    "observedAt": 1789564800123,
    "retentionDays": 180
  }
}
```

Page with `cursor=<nextCursor>`. A cursor is opaque; do not build one.

`GET /v1/notifications/unread-count` is the same counts and revision with no
items, for a badge.

## Audiences

`profile` is the viewer's own inbox. `account-admin` is the administrators'
inbox for an account — it is keyed by account, not by profile, so any
administrator sees and can clear it.

`audience=all` (the default) merges both when you are an owner, and is just your
profile inbox otherwise. A member asking explicitly for `account-admin` gets
**401**, not an empty list, so you can tell "not allowed" from "nothing there".

An identifier from an inbox you may not write reports `not-found`. The server
never confirms that an identifier exists somewhere else.

## Actions

An action is one of exactly two things.

```json
{ "kind": "navigate", "label": "Open library",
  "target": { "view": "library", "entityId": "lib-1", "libraryId": "lib-1" } }
```

```json
{ "kind": "command", "label": "Try again", "command": "retry-job",
  "arguments": { "downloadId": "dl-19" } }
```

The command allowlist is exactly six identifiers, and it is the whole set:

`retry-job`, `open-download`, `dismiss-conflict`, `review-device`,
`open-feedback`, `run-scan`.

Views are allowlisted too; `GET /v1/notifications/capabilities` publishes both
lists along with the audiences, severities, states, producer sources, paging
limit, heartbeat interval and maximum wait. Read it once per session and validate
against it.

**If you do not recognise a command or a view, render the action disabled.** Never
synthesise behaviour from the string: an unknown value means the server is newer
than you, not that you should guess.

Localising clients should key on `category` plus `arguments` and treat
`title`/`body` as the server's fallback rendering.

## Writing: one batch, fenced and idempotent

There is no per-notification endpoint. Everything goes through one request.

```
POST /v1/notifications/inbox/actions
```

```json
{
  "operationId": "inbox-2026-09-16-a",
  "expectedRevision": 41,
  "audience": "all",
  "operations": [
    { "action": "read",    "ids": ["8f2c…", "91ab…"] },
    { "action": "archive", "ids": ["4410…"] }
  ]
}
```

```json
{
  "data": {
    "revision": 42,
    "audience": "all",
    "applied": 3,
    "receipts": [
      { "action": "read",    "id": "8f2c…", "outcome": "applied" },
      { "action": "read",    "id": "91ab…", "outcome": "unchanged" },
      { "action": "archive", "id": "4410…", "outcome": "applied" }
    ],
    "counts": { "unread": 0, "read": 8, "archived": 4, "total": 11 }
  }
}
```

* Actions: `read`, `unread`, `archive`, `unarchive`, `read-all` (which takes no
  `ids`). Up to eight operations and 200 identifiers per request.
* `archive` also marks read.
* Outcomes: `applied`, `unchanged` (already in that state), `not-found`.
* A stale `expectedRevision` is **409** with `error.currentRevision`. Refresh and
  retry.
* `operationId` is an idempotency key. Retrying a request whose response you lost
  returns the recorded answer rather than applying it twice. Reusing the key with
  a *different* body is 409.
* `expectedRevision: 0` skips the fence. Use it only before you hold a revision.

## Staying live

Share the device's authenticated `GET /v1/events` server-sent stream. A
`notification.changed` event names the inbox and its revision; reread the inbox
or unread-count document quietly. Event ids belong to the unified feed, and are
independent of inbox revisions. Resume with `Last-Event-ID`.

A platform without streaming uses `GET /v1/events?after=<nextAfter>&waitMs=25000`.
Its response contains `events`, `nextAfter` and `more`; ids and cursors are
opaque. `stream.resync` means reread the visible documents. The
[events guide](guide/06-events.md) has the paging and parked-poll limits. Heartbeats are comments every 25 seconds and do
no database work. Streams end at token expiry or after 30 minutes; revocations
wake them for a live authorization check. One stream per installation replaces
an older connection on reconnect.

## Owner broadcast

```
POST /v1/admin/notifications/broadcast
```

```json
{
  "operationId": "maint-2026-09-16",
  "audience": "profile",
  "severity": "warning",
  "dedupeKey": "maintenance-window",
  "title": "Scheduled restart tonight",
  "body": "This server restarts at 02:00 and playback will stop.",
  "actions": [],
  "expiresInDays": 2
}
```

Re-sending the **same `dedupeKey`** edits the existing notice in every inbox —
new text, moved back to unread — rather than posting a second copy. That is how
you correct a maintenance window instead of repeating it. Use a *new*
`operationId` when you mean to edit, and the *same* one when you are retrying.

`expiresInDays: 0` applies the server's retention window. Bounded to 500
recipients.

## Producers

Notifications are raised by the server, never posted by a client (except the
owner broadcast). The complete set:

| Source | Category | Audience | Deduped by |
| --- | --- | --- | --- |
| `downloads` | `download.finished`, `download.failed` | profile | the download |
| `dvr` | `dvr.conflict` | account-admin | the recording |
| `security` | `security.new-device` | profile | the device |
| `security` | `security.password-change` | profile | the moment |
| `scan` | `scan.failed` | account-admin | the source |
| `networking` | `networking.certificate-expiring`, `…-expired` | account-admin | the certificate scope |
| `storage` | `storage.low` | account-admin | the volume |
| `feedback` | `feedback.received` | account-admin | the report |
| `feedback` | `feedback.updated` | profile | the report |
| `broadcast` | `server.message` | either | the owner's key |

A producer re-raising the same key updates the record in place — new text, new
severity, refreshed expiry, moved back to unread — so a condition that keeps
recurring is one live notice whose revision moves, not a pile. A standing
`storage.low` notice is withdrawn automatically when the volume recovers.

Report text never enters the inbox: `feedback.received` says a report is waiting
and links to it.

## Retention

`retentionDays` is reported by `GET /v1/admin/notifications/settings`, but it is
**not** owned there. `retentionSettingsField` names its authority — the settings
registry field `notificationDays` — which is written through
`PATCH /v1/admin/console/settings`. There is one value, not two.

The storage and certificate thresholds *are* owned by this feature and are
written through `PATCH /v1/admin/notifications/settings`, fenced by
`expectedRevision`. `storageCriticalPercent` must be strictly below
`storageWarningPercent`.

Retention runs on the server's maintenance tick. Reading an inbox never prunes,
so a GET is a read.

---

# Feedback

## Ask before you draw the form

```
GET /v1/feedback/capabilities
```

```json
{
  "data": {
    "revision": "e1.0",
    "canSubmit": true,
    "kinds": [
      { "id": "playback", "label": "Playback", "categories": [
        { "id": "buffering", "label": "Buffering or stalling",
          "description": "Playback starts but keeps pausing.",
          "wantsPlaybackSession": true, "wantsItem": true }
      ]}
    ],
    "maxMessageLength": 2000,
    "minMessageLength": 8,
    "diagnosticsSupported": true,
    "diagnosticsOptional": true,
    "duplicateWindowHours": 24,
    "retentionDays": 180,
    "statuses": ["open", "in-progress", "resolved", "closed"],
    "diagnosticsDecisions": ["attached", "unavailable", "declined", "not-requested"],
    "perProfileHourlyLimit": 10,
    "reporterName": "Sam",
    "reporterAuthority": "local"
  }
}
```

`canSubmit: false` comes with `submitBlockedReason` — a restricted profile or a
rate-limited one. Disable your control and show the reason rather than letting
someone write a message that will be refused.

Each category declares `wantsPlaybackSession` and `wantsItem`, so you know when
to offer the diagnostics prefill and when to ask which title is affected.

Cache the document against `revision`, which changes only when the taxonomy does.

## Submitting

```
POST /v1/feedback/reports
```

```json
{
  "operationId": "fb-2026-09-16-a",
  "kind": "playback",
  "category": "buffering",
  "message": "It pauses every minute or so on my TV.",
  "itemId": "",
  "playbackSessionId": "sess-77",
  "attachDiagnostics": true
}
```

```json
{
  "data": {
    "report": {
      "id": "b31d…",
      "revision": 1,
      "status": "open",
      "kind": "playback",
      "category": "buffering",
      "message": "It pauses every minute or so on my TV.",
      "itemId": "item-42",
      "reporter": { "name": "Sam", "authority": "local", "role": "member", "self": true },
      "diagnostics": {
        "decision": "attached",
        "reference": "playback-session:sess-77",
        "detail": "The server-held record for this playback session is linked to the report."
      },
      "duplicates": 0,
      "thread": [
        { "sequence": 1, "at": 1789564800000, "revision": 1,
          "status": "open", "reply": "", "actorClass": "viewer" }
      ]
    },
    "duplicate": false,
    "created": true
  }
}
```

### Diagnostics prefill

`playbackSessionId` is a *hint*, not an upload. The server resolves it against
the playback record it already holds and records the decision:

| `decision` | Meaning |
| --- | --- |
| `attached` | Resolved; `reference` names the record. The affected title is filled in for you. |
| `declined` | A session was named but `attachDiagnostics` was `false`. |
| `unavailable` | The session is gone, or belongs to another viewer. |
| `not-requested` | No session was named. |

A decision is always recorded, so a reviewer can tell a refusal from a failure. A
session identifier from someone else's playback always resolves to `unavailable`.

### Duplicates

If the same profile already opened a report with the same kind, category and
item, whose message normalises to the same text, within 24 hours, you get that
report back:

```text
{ "data": { "report": { "id": "b31d…", "duplicates": 1, … },
            "duplicate": true, "created": false, "duplicateOf": "b31d…" } }
```

Normalisation folds case, punctuation and runs of whitespace, so "It won't
play!!" and "it wont play" are one report. Show the existing report and its
thread rather than pretending a new one was filed.

Limit: 10 reports per profile per hour, then 429 with `Retry-After`.

## Following a report

```
GET /v1/feedback/reports            # your own, with counts
GET /v1/feedback/reports/{id}       # one, with its full thread
```

The thread is the reporter-visible history: every status change with the
administrator's reply where one was left. When a report moves you also get a
`feedback.updated` notification in your inbox.

## Triage (owner only)

```
GET  /v1/admin/feedback/reports?status=open&kind=library&reporter=sam
POST /v1/admin/feedback/reports/{id}/status
```

The list carries `statusCounts` alongside the page, and those counts honour every
filter **except** `status` — so filtering to `kind=library` gives you the
open/in-progress/resolved/closed split within library reports, and the badges
agree with the list. `reporter` is a prefix match on the display name, for
type-ahead.

```json
{ "operationId": "tri-1", "expectedRevision": 1,
  "status": "in-progress", "reply": "Looking at the source now." }
```

Fenced by `expectedRevision` (409 with `currentRevision` if another administrator
moved it first) and idempotent by `operationId`. The reply is visible to the
reporter. The thread is capped at 200 entries.

## Errors

| Status | Code | What to do |
| --- | --- | --- |
| 400 | `invalid_console_request` | A field is outside its bounds; `error.fields` names it. |
| 401 | `unauthorized` | Sign in, or you lack the authority for this audience or route. |
| 404 | `not_found` | Not in your scope. |
| 409 | `console_conflict` | Refresh to `error.currentRevision` and retry. |
| 429 | `console_capacity` | Respect `Retry-After`. |
| 503 | `console_unavailable` | Retry with the **same** `operationId`. |

Every write here is idempotent by `operationId`. On a 503 or a dropped
connection, retry with the same key: you will get the recorded answer, not a
second effect.

Broadcast fan-out uses pages of at most 50 profiles, releasing the database writer
and checking owner authority between pages. An interruption preserves its cursor
and counts for fourteen days; resend the same operation and payload to continue.
Changing that payload with the same operation ID is refused. The final response
keeps the existing counts and at most 50 sample notice IDs. The initial last profile
bounds the traversal; profiles created after the broadcast starts are not promised
that notice. Portico Account sign-in uses the server's local membership/profile
identity, so these inboxes also receive its broadcasts.
