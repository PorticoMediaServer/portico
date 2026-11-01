---
title: Your first integration
description: A key, a server address and a few small requests.
kind: guide
order: 1
---

## Get an API key

As the owner, open **Settings → Server → Integrations** and create a named API key. Give each tool its own key and only the scopes it needs:

| Task | Scope |
| --- | --- |
| Libraries and recently added | `read` |
| Strict path notify and subtitle refresh | `library`, with owner authority |
| Own-account sessions | `read` and `playback` |
| Server-wide sessions and webhook management | `admin`, with the required owner or administrator authority |

Scopes do not replace account or library permissions. `admin` includes `read`, `playback` and `library`; the other scopes are independent. Owners can Reveal and Copy keys later and revoke them immediately. On TV, secrets are never displayed. Keys are recoverable in the server’s local database and backups.

Set the server address and your key in your terminal. Use an address the script can reach; `127.0.0.1` works only on the server itself. Use HTTPS across untrusted networks.

```sh
export PORTICO_URL='http://127.0.0.1:32500'
read -r -s -p 'Portico API key: ' PORTICO_API_KEY; echo
export PORTICO_API_KEY
```

The examples use Bash and curl. Save Python examples as files and run them with Python 3; they need no extra packages. Native requests work without turning on Integrations compatibility or contacting Portico Services.

## Notify a file or folder change

Use the absolute path **as Portico sees it**, including container mounts. Keep it narrower than a library source root.

```sh
curl --fail-with-body "$PORTICO_URL/v1/libraries/paths-changed" \
  -H "Authorization: Bearer $PORTICO_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"mode":"strict","paths":[{"path":"/media/Movies/Arrival (2016)","change":"changed"}]}'
```

Use `added`, `changed` or `removed`. A `202` queues targeted work; it does not mean scanning and matching have finished. Inspect the response’s `paths` and `jobs`.

Strict mode never falls back to a full-source scan. Source roots, unresolved paths, symlink-following sources and unsupported remote sources can be refused. Correct the path or source configuration. A busy or paused source can return `503` and `Retry-After: 2`; wait and retry the same narrow target. Do not replace a refused request with a full-library refresh.

For an import hook, save this as `notify.py`. It sends one exact file or folder, reports failures and leaves retry scheduling to the caller:

```python
import json
import os
import sys
from urllib.error import HTTPError
from urllib.request import Request, urlopen

if len(sys.argv) not in (2, 3):
    raise SystemExit("Usage: python3 notify.py /absolute/server/path [added|changed|removed]")
path = sys.argv[1]
change = sys.argv[2] if len(sys.argv) == 3 else "changed"
if not path.startswith("/") or change not in {"added", "changed", "removed"}:
    raise SystemExit("Use an absolute server path and added, changed or removed")
body = json.dumps({"mode": "strict", "paths": [{"path": path, "change": change}]}).encode()
request = Request(
    os.environ["PORTICO_URL"].rstrip("/") + "/v1/libraries/paths-changed",
    data=body,
    headers={"Authorization": "Bearer " + os.environ["PORTICO_API_KEY"],
             "Content-Type": "application/json"},
    method="POST",
)
try:
    with urlopen(request, timeout=30) as response:
        print(response.read().decode())
except HTTPError as error:
    print(error.read().decode(), file=sys.stderr)
    print("Retry-After:", error.headers.get("Retry-After", "not supplied"), file=sys.stderr)
    raise SystemExit(1)
```

```sh
python3 notify.py '/media/Movies/Arrival (2016)' changed
```

## List libraries

```sh
curl --fail-with-body "$PORTICO_URL/v1/integrations/libraries?limit=20" \
  -H "Authorization: Bearer $PORTICO_API_KEY"
```

Read the `items` array. When `nextCursor` is present, send it back unchanged with the same query and credential. Stop when it is absent or empty. Never infer the end from a short page.

## Recently added

Choose a library ID from the previous response:

```sh
export PORTICO_LIBRARY_ID='replace-with-library-id'
curl --fail-with-body --get "$PORTICO_URL/v1/integrations/items" \
  -H "Authorization: Bearer $PORTICO_API_KEY" \
  --data-urlencode "libraryId=$PORTICO_LIBRARY_ID" \
  --data-urlencode 'sort=added' --data-urlencode 'order=desc' \
  --data-urlencode 'limit=20'
```

The newest visible titles come first. Follow `nextCursor` with the same filters to read more. Ignore unknown optional fields. If a continuation expires, start the query again.

## Who’s watching

```sh
curl --fail-with-body "$PORTICO_URL/v1/integrations/sessions?limit=20" \
  -H "Authorization: Bearer $PORTICO_API_KEY"
```

For server-wide monitoring, use an authorized owner or administrator credential with `admin` scope. A limited key with `read` and `playback` sees only permitted own-account controls and activity. A key does not gain global visibility just because its creator is the owner. Follow `nextCursor` for more sessions.

## Receive webhooks

Portico can send events straight to your endpoint. Follow the [webhook guide](/developers/api/integrations/webhooks/) to create one, verify its signature and handle duplicate deliveries.

## Refresh subtitles

Use an item ID from the native items response and an owner key with `library` scope:

```sh
export PORTICO_ITEM_ID='replace-with-item-id'
curl --fail-with-body -X POST \
  "$PORTICO_URL/v1/integrations/items/$PORTICO_ITEM_ID/subtitles/refresh" \
  -H "Authorization: Bearer $PORTICO_API_KEY" \
  -H 'Content-Type: application/json' --data '{}'
```

This rereads eligible exact subtitle sidecars using stored media evidence. It does not probe media, enrich metadata or replace artwork. Missing or changed evidence is an explicit refusal. Fix the underlying path or evidence instead of broadening the operation.

For parameters, models and error codes, use the [native Integrations reference](/developers/api/integrations/).
