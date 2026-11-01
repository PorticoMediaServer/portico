---
title: Webhooks
description: Receive events directly from your server and verify every delivery.
kind: guide
order: 2
---

## Set up a webhook

As the owner, open **Settings → Server → Integrations → Webhooks**, choose a destination and events, then save the signing secret. API setup uses an owner’s key with `admin` scope. These examples use the [terminal variables from the quick start](/developers/api/integrations/quick-start/#get-an-api-key).

Read the available event names:

```sh
curl --fail-with-body "$PORTICO_URL/v1/admin/webhooks" \
  -H "Authorization: Bearer $PORTICO_API_KEY"
```

Create a webhook with events from the response’s `events` list:

```sh
curl --fail-with-body "$PORTICO_URL/v1/admin/webhooks" \
  -H "Authorization: Bearer $PORTICO_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"name":"Automation","url":"https://automation.example.com/hooks/portico","events":["title.added","playback.started","playback.stopped"]}'
```

Replace the example URL with your receiver’s final address. Portico sends directly from your server; the receiver must be reachable from it. Local destinations are supported. Redirects are not followed. Payloads can contain titles or playback activity; choose a destination you trust.

Save the returned `secret` privately: webhook signing secrets are shown once. Lists show only a hint. If it is lost, replace it in Settings and update the receiver. A key and a webhook signing secret are different credentials.

## Events and payloads

Each delivery is a JSON `POST` with `id`, `type`, `createdAt`, `server` and `data`. Events cover title additions, updates and removals; scan start and finish; playback start, pause, resume and stop; recordings; sign-ins; backups; low storage; available updates; and server startup. Query `GET /v1/admin/webhooks` for the current event names rather than assuming this list is fixed.

`Portico-Event` carries the type. `Portico-Event-Id` matches the body’s `id` and stays the same on retries. `Portico-Delivery-Attempt` starts at `1`. Ignore new event types and fields you do not recognize.

## Verify signatures

`Portico-Signature` has the form `t=<unix seconds>,v1=<lowercase hex>`. Compute HMAC-SHA256 with the secret over the timestamp, a period and the **raw request body bytes**. Verify before parsing JSON.

```python
import hashlib
import hmac
import time

def verify(secret: str, signature: str, raw_body: bytes) -> bool:
    try:
        pairs = [part.strip().split("=", 1) for part in signature.split(",")]
        if len(pairs) != 2 or any(len(pair) != 2 for pair in pairs):
            return False
        fields = dict(pairs)
        if set(fields) != {"t", "v1"}:
            return False
        timestamp, given = fields["t"], fields["v1"]
        if not timestamp.isascii() or not timestamp.isdigit() or len(timestamp) > 20:
            return False
        if len(given) != 64 or any(c not in "0123456789abcdef" for c in given):
            return False
        if abs(time.time() - int(timestamp)) > 300:
            return False
        expected = hmac.new(secret.encode(), timestamp.encode() + b"." + raw_body,
                            hashlib.sha256).hexdigest()
        return hmac.compare_digest(expected, given)
    except (AttributeError, TypeError, ValueError):
        return False
```

Pass the header and untouched body from your HTTP framework to `verify`. Refuse invalid or stale signatures. After verification, deduplicate recent `Portico-Event-Id` values before acting; keep that record across receiver restarts if repeated actions matter.

## Test and handle retries

Choose the returned webhook ID:

```sh
export PORTICO_WEBHOOK_ID='replace-with-webhook-id'
curl --fail-with-body -X POST \
  "$PORTICO_URL/v1/admin/webhooks/$PORTICO_WEBHOOK_ID/test" \
  -H "Authorization: Bearer $PORTICO_API_KEY" \
  -H 'Content-Type: application/json' --data '{}'

curl --fail-with-body \
  "$PORTICO_URL/v1/admin/webhooks/$PORTICO_WEBHOOK_ID/deliveries" \
  -H "Authorization: Bearer $PORTICO_API_KEY"
```

The test queues `webhook.test`; inspect the **Delivery log** for the result. Return `2xx` within 15 seconds after accepting an event. Failed deliveries are retried, so make processing idempotent. The queue and log are bounded; inspect failing and dropped counts instead of treating webhooks as a complete historical ledger.
