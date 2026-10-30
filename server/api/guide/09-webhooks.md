# 9. Webhooks

The owner can have the server tell another service when something happens. A
webhook is a URL, a list of events, and a signing secret. Deliveries are made
by the local server itself: nothing passes through Portico's own services.

Manage them in the console or with an owner's key that has the `admin` scope:

| | |
|---|---|
| List (and the events that can be chosen) | `GET /v1/admin/webhooks (portico.openapi.json, operationId list_webhooks)` |
| Add | `POST /v1/admin/webhooks (portico.openapi.json, operationId create_webhook)` |
| Change, turn on or off, replace the secret | `PATCH /v1/admin/webhooks/{id} (portico.openapi.json, operationId update_webhook)` |
| Remove | `DELETE /v1/admin/webhooks/{id} (portico.openapi.json, operationId delete_webhook)` |
| Send a test | `POST /v1/admin/webhooks/{id}/test (portico.openapi.json, operationId test_webhook)` |
| Delivery log | `GET /v1/admin/webhooks/{id}/deliveries (portico.openapi.json, operationId list_webhook_deliveries)` |

<!-- example: create-webhook file=portico.openapi.json operationId=create_webhook direction=request schema=@request /v1/admin/webhooks post -->
```json
{
  "name": "Home Assistant",
  "url": "https://automation.example.com/hooks/portico",
  "events": ["playback.started", "playback.paused", "playback.resumed", "playback.stopped"]
}
```

The URL may be any `http` or `https` address, including one on your own
network. It may not contain a user name or password. The answer to adding a
webhook, or to `{"rotateSecret": true}`, carries the signing `secret` once;
lists show only `secretHint`. You may supply your own `secret` (16–128
printable, non-space ASCII characters) instead. Up to 20 webhooks.

## What is sent

One `POST` per event, `Content-Type: application/json`, at most 64 KiB:

```json
{
  "id": "5b0c8f4e2a9d4c6f8e1a2b3c4d5e6f70",
  "type": "playback.started",
  "createdAt": "2026-10-13T19:04:11Z",
  "server": {"id": "srv_1", "name": "Living room"},
  "data": {
    "sessionId": "ps_7Yk2",
    "kind": "item",
    "state": "playing",
    "positionMs": 0,
    "accountId": "acct_2",
    "user": "sam",
    "profileId": "prof_4",
    "profile": "Sam",
    "deviceId": "dev_9",
    "device": "Living room TV",
    "platform": "tvos",
    "itemId": "kq3Jm0Zr1yQ8b2nWcP4d5A",
    "title": "The Matrix"
  }
}
```

Headers:

| Header | Value |
|---|---|
| `Portico-Event` | The event type, the same as `type`. |
| `Portico-Event-Id` | The same as `id`. It is the same on every retry of one event: use it to ignore a delivery you have already handled. |
| `Portico-Delivery-Attempt` | `1` for the first attempt, `2` for the first retry, and so on. |
| `Portico-Signature` | `t=<unix seconds>,v1=<hex>` — see below. |
| `User-Agent` | `Portico-Webhooks/<server version>` |

No payload ever contains a password, a token, a signing secret or a webhook's
address.

## Checking the signature

`v1` is the HMAC-SHA256, keyed with the webhook's secret, of the timestamp, a
full stop, and the **exact bytes** of the request body, written in lower-case
hex:

```
v1 = hex( HMAC_SHA256( secret, t + "." + body ) )
```

To check a delivery:

1. Read `t` and `v1` from `Portico-Signature`.
2. Compute the HMAC over `t + "." + <raw body>` with your secret.
3. Compare it with `v1` using a constant-time comparison.
4. Refuse the delivery if `t` is more than five minutes from your clock, so a
   captured request cannot be replayed later.
5. Remember recent `Portico-Event-Id` values and ignore one you have seen.

```python
import hashlib, hmac, time

def verify(secret: str, signature_header: str, body: bytes, tolerance=300) -> bool:
    try:
        pairs = [p.strip().split("=", 1) for p in signature_header.split(",")]
        if any(len(p) != 2 for p in pairs) or len(pairs) != 2:
            return False
        parts = dict(pairs)
        if set(parts) != {"t", "v1"}:
            return False
        t, v1 = parts["t"], parts["v1"]
    except (TypeError, ValueError, AttributeError):
        return False
    if len(t) > 20 or len(v1) != 64 or any(c not in "0123456789abcdef" for c in v1):
        return False
    if not t.isdigit() or abs(time.time() - int(t)) > tolerance:
        return False
    expected = hmac.new(secret.encode(), t.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, v1)
```

```js
import {createHmac, timingSafeEqual} from 'node:crypto';

export function verify(secret, signatureHeader, rawBody, toleranceSeconds = 300) {
  if (typeof signatureHeader !== 'string') return false;
  const pairs = signatureHeader.split(',').map(p => p.trim().split('=', 2));
  if (pairs.length !== 2 || pairs.some(p => p.length !== 2)) return false;
  const parts = Object.fromEntries(pairs);
  if (Object.keys(parts).sort().join(',') !== 't,v1' || !/^[0-9a-f]{64}$/.test(parts.v1 ?? '')) return false;
  if (!/^\d+$/.test(parts.t ?? '') || Math.abs(Date.now() / 1000 - Number(parts.t)) > toleranceSeconds) return false;
  const expected = createHmac('sha256', secret).update(`${parts.t}.`).update(rawBody).digest('hex');
  const given = Buffer.from(parts.v1 ?? '', 'utf8');
  return given.length === expected.length && timingSafeEqual(given, Buffer.from(expected, 'utf8'));
}
```

Verify against the raw body you received, before any JSON parsing or
re-serialising.

## Delivery, retries and the log

- Answer `2xx` within 15 seconds to accept a delivery. Any other answer, a
  timeout, or a connection failure is a failed attempt.
- **Redirects are not followed.** A `3xx` is a failed attempt; use the final
  address.
- A failed delivery is retried after 30 seconds, 2 minutes, 10 minutes, 30
  minutes, 1 hour, 2 hours and 4 hours. After the last retry it is given up and
  stays in the log as `failed`.
- Deliveries to one webhook are sent one at a time, in order. While a webhook is
  failing the server waits before trying it again, so retries can arrive later
  than the times above.
- Several changes to the same thing before the first attempt are one delivery
  (two edits to one title, two pauses of one session).
- At most 1,000 deliveries wait for one webhook. Events that arrive while its
  queue is full are not kept; the webhook's `dropped` count says how many.
- Turning a webhook off abandons what was waiting for it.
- The log keeps the 200 most recent finished deliveries of each webhook, with
  the event, the number of attempts, the HTTP status, how long it took, and a
  short reason for a failure: `timed_out`, `could_not_connect`,
  `certificate_not_trusted`, `redirect_not_followed`, `status_<code>`,
  `request_failed`.
- "Send a test" queues a `webhook.test` event for that webhook whether or not it
  is turned on; its result appears in the log a moment later.

## Events

| `type` | When | `data` |
|---|---|---|
| `title.added` | A film, show, episode, album or book is added. Sent about a minute after it appears, so it carries the matched title. | `itemId`, `kind`, `title`, `year`, `libraryId`, `parentId` |
| `title.updated` | A title's name, year or published details change. | as above |
| `title.removed` | A title is removed from the library (not merely missing from disk). | as above |
| `scan.started` | A library scan begins. | `jobId`, `libraryId`, `library` |
| `scan.finished` | A library scan ends. | `jobId`, `libraryId`, `library`, `outcome` (`complete`, `complete_with_warnings`, `failed`, `cancelled`), `processed` |
| `playback.started` | Someone starts playing. | `sessionId`, `kind`, `state`, `positionMs`, `accountId`, `user`, `profileId`, `profile`, `deviceId`, `device`, `platform`, `itemId`, `title`, `channelId` |
| `playback.paused` / `playback.resumed` | Playback is paused or continues. | as above |
| `playback.stopped` | Playback ends. | as above, with `reason` (`stopped`, `ended`, `completed`, `terminated`, `lease_expired`, `paused_timeout`) |
| `recording.finished` | A recording is complete (or ended early but is playable). | `recordingId`, `state`, `title`, `channelId`, `accountId`, `user`, `profileId`, `profile` |
| `recording.failed` | A recording could not be made. | as above, with `reason` |
| `member.signed_in` | A session starts for a profile on a device. Using an API key or token is not a sign-in. | `accountId`, `user`, `profileId`, `profile`, `role`, `deviceId`, `device`, `platform`, `app`, `address` |
| `backup.succeeded` | A manual or scheduled backup finishes. | `backupId`, `kind` |
| `backup.failed` | A backup fails. | `kind`, `reason` |
| `storage.low` | Free disk space crosses the owner's warning or critical level. | `severity`, `freePercent`, `freeBytes` |
| `update.available` | A newer version of the server is published. Sent once per version. | `version`, `currentVersion`, `channel`, `notesUrl` |
| `server.started` | The server has started and is answering. | `version` |

A field with no value is left out. A member's private recordings library is
never reported in `title.*` events. A change of quality or version in the middle
of playback is not a stop and a start. New event types and new fields may be
added; ignore what you do not recognise.
