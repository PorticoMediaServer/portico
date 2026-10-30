# 6. Changes while a screen is open

`GET /v1/events` is a bearer-authenticated invalidation feed. It tells a client
which resources changed; the client rereads them while retaining its current
screen. It is local server traffic, not push notification or email delivery.

## Streaming and polling

Without `waitMs`, the response is SSE (`text/event-stream`). Send the last
received SSE `id` as `Last-Event-ID` on reconnect. Do not put `after` in a
streaming query. For example:

```bash
curl -N -fsS "$BASE/v1/events" -H "Authorization: Bearer $TOKEN" \
  -H 'Accept: text/event-stream' -H "Last-Event-ID: $LAST_ID"
```

The first idle connection emits `stream.opened` with its position. A
`stream.position` frame advances the resume point through events filtered out
for this viewer. Keep those IDs as well as the IDs of resource events.
Heartbeats arrive about every 25 seconds; a stream ends by 30 minutes or its
token deadline. Refresh the session and reconnect. One installation shares
one stream; a newcomer replaces its earlier stream.

A polling client uses
`GET /v1/events?after=$NEXT_AFTER&waitMs=25000`. Wait is 0–25000 milliseconds. The JSON answer
has `events`, `nextAfter` and `more`. Save `nextAfter` even when no visible event
was returned. While `more` is true, read again after at least a second;
otherwise park again. One account on one installation may have four parked
polls (`waitMs` above 0) open at once. A fifth gets `429 event_wait_limit` with
`Retry-After: 2`: keep the cursor, read with `waitMs=0` no more often than every
2 seconds, and try parking again after 30 seconds.

IDs and cursors are opaque tokens of at most 128 URL-safe characters. Never
build, compare or order them; store the last one and send it back. They belong
to this viewer on this installation and expire after a day. A malformed cursor
gets `400 invalid_cursor`.

Every read is one bounded page: at most 100 events and 64 KiB on the wire.
A client more than 100 feed positions behind, one whose cursor is from another
profile, installation, role or server run, and one whose cursor fell outside
retained history all get a single `stream.resync` instead of a replay: quietly
reread visible resources and continue from its id. A missed change is never
skipped silently. A resync must not make a client blank its cached screen,
restart its shell or stop playback.
Retry transient errors with backoff and the response's `Retry-After`.

## Resource hints

An event has `id`, `type`, `at`, a `resource` (`kind`, `id`), and where applicable
`revision` and `data`. Events are filtered by profile, device, library access
and owner/admin visibility. Known hints include:

- `notification.changed`: reread inbox and unread count.
- `library.scan.updated`, `library.changed`: reread relevant library content.
- `personal.changed`, `progress.changed`: reread viewer state and affected rows.
  `resource.id` is the library. When every change the hint stands for was
  about one title, `data.titles` names it, then its parent and grandparent
  (an episode, its season, its show): a title page not in that list can skip
  the read. Without `data.titles`, any title in the library may have moved.
- `session.updated`, `queue.updated`, `admin.sessions`: reread playback resources.
- `settings.changed`: reread the affected settings document.
- `receiver.inbox_changed`: reread a receiver's inbox.
- `operation.updated`: reread a background operation.
- `list.changed`: reread one list; `resource.id` names it. The owner and
  administrators hear `apiKeys`, `memberTokens`, `webhooks` (including the
  delivery log), `members`, `invitations`, `devices`, `clientReports`,
  `feedback`, `downloads`, `remoteSources`, `liveSources` (with refresh state),
  `tuners`, `recordingGroups`, `recordingStorage`, `libraryChannels` and
  `playbackProblems`. A profile hears `accessTokens` for its own tokens,
  `togetherGroups` for the Watch Together rooms it is in, `myRecordings` for its
  recordings and recording rules, and `myDownloads` for its downloads. Everyone
  hears `guide` (a TV guide was brought in again, or a source turned on or off)
  and `recommendations` (Home's Trending and Popular rows may have moved).

Treat hints as coalescible invalidations, not a complete change log or a payload
for mutating local records. Ignore unknown event types safely. Source:
`internal/httpapi/events_routes.go`, `internal/eventfeed/feed.go`,
`internal/apievents/` and producers in the relevant services.

## Notifications and other streams

`GET /v1/notifications/inbox` is a paged inbox with its own revision; unread
count is `GET /v1/notifications/unread-count`. Apply read/archive operations
with `POST /v1/notifications/inbox/actions`. Its revision is separate from the
feed position. [Notifications](../notifications.md) documents its envelopes.

Watch Together groups have their own `/v1/groups/{id}/events` protocol, and
administrators can tail `/v1/admin/logs/events`. These are separate streams with
separate cursor and authority rules. Outbound [webhooks](09-webhooks.md) are
for integrations, not a replacement for the viewer's invalidation feed.
