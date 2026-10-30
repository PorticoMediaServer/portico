# 7. API keys, personal access tokens, scopes and limits

A script, a home-automation hub, a download manager or an AI assistant does not
sign in the way an app does. It carries a credential that somebody made for it:

| Credential | Made by | Where | Secret starts with |
|---|---|---|---|
| **API key** | the owner | the console, or `POST /v1/admin/api-keys (portico.openapi.json, operationId create_api_key)` | `pk.` |
| **Personal access token** | any member, for themselves | their settings, or `POST /v1/me/access-tokens (portico.openapi.json, operationId create_access_token)` | `pat.` |

Both are used the same way, on routes their scopes and role allow:

```
Authorization: Bearer pk.3q2-7w...
```

On a `GET` or `HEAD` the secret may instead travel in the address, as the
`apiKey` query parameter, so an address works on its own (artwork in another
app, a stream for a player, a download link):

```
GET /v1/...?apiKey=pk.3q2-7w...
```

The server takes the parameter out of the address before anything else reads
the request: it is never logged, recorded or sent back. On any other method it
is ignored and the request has no credential. Sending it in both the header and
the address is refused (`401`). An address that carries a key is as powerful as
the key: share it only where you would share the key.

A key or token **is the member who made it**. It sees the libraries that member
sees, under the same content restrictions, and can never do more than that
member could do signed in. On top of that it is held to the **scopes** chosen
when it was made.

## Scopes

| Scope | What it allows |
|---|---|
| `read` | Look at libraries, titles, the guide, and the member's own watch state, lists and notifications. |
| `playback` | Play, control playback, and change the member's own watch state, lists, preferences and recordings. |
| `library` | Scan, report changed paths, match, edit and delete what is in the libraries. |
| `admin` | Everything else the member may administer: settings, profiles (create, edit, order, pictures, PIN reset, delete), members (add, change, delete), devices and sessions (list, rename, approve, block, sign out), backups, logs, webhooks. Includes the three above. |

Scopes do not include one another (apart from `admin`): a credential that reads
and controls playback is given both `read` and `playback`. A download manager
that only reports new files needs `library` and nothing else.

Every operation declares the one scope it needs. In `portico.openapi.json` it is
the `x-scope` value of the operation. Two further values appear there:

- `open` — no credential is needed at all (server identity, health).
- `session` — only a signed-in session may call it: signing in and out,
  renewing and registering; changing a password, a second factor or a
  recovered PIN; creating, listing or revoking keys and tokens; ownership
  transfer; pairing a screen or approving a TV code; accepting an invitation;
  remembered accounts and choosing a profile; the networking claim. **A key or
  token can never create, widen or revoke a credential**, its own included, and
  can never change what proves who a person is.

Refusals:

| Status | `code` | Meaning |
|---|---|---|
| `401` | `unauthorized` | The secret is unknown, revoked or expired, or its member has been disabled or deleted. |
| `403` | `insufficient_scope` | The credential was not given the scope this operation needs; `reason` names the scope. |
| `403` | `session_required` | This operation is for signed-in sessions only. |
| `403` | `not_permitted` / `forbidden` | The scope is fine but the member's role is not (a member's token on an owner's route). |

## Creating and revoking

Credential-management routes require a signed-in session. A key or token, even
with `admin`, cannot list, create or revoke credentials. In the web app, owners
use Server → Integrations; other members use Personal access tokens in settings.

`POST /v1/admin/api-keys (portico.openapi.json, operationId create_api_key)`
and `POST /v1/me/access-tokens (portico.openapi.json, operationId
create_access_token)` take the same body:

```json
{
  "operationId": "create-key-2026-10-13-a",
  "name": "Home Assistant",
  "scopes": ["read", "playback"],
  "expiresAt": "2027-10-13T00:00:00Z"
}
```

`expiresAt` is at most ten years ahead and is optional; leave it out for a credential that lasts until it is
revoked. `operationId` identifies the request: sending the same one again does
not make a second credential.

The answer carries the secret **once**. The server stores only a digest of it
and cannot show it again; a retry of the same request returns the credential
without `secret`.

```json
{
  "credential": {
    "id": "Zr1kq0yQm3T8b2nWcP4d5A",
    "kind": "key",
    "name": "Home Assistant",
    "scopes": ["read", "playback"],
    "hint": "x9Kd2Q",
    "createdAt": "2026-10-13T12:00:00Z",
    "expiresAt": "2027-10-13T00:00:00Z",
    "state": "active",
    "member": {"accountId": "acct_1", "username": "sam", "profileId": "prof_1", "profile": "Sam"}
  },
  "secret": "pk.3q2-7wExampleExampleExampleExampleExample0"
}
```

Lists (`GET /v1/admin/api-keys (portico.openapi.json, operationId
list_api_keys)`, `GET /v1/me/access-tokens (portico.openapi.json, operationId
list_access_tokens)`, and for the owner every member's tokens at `GET
/v1/admin/access-tokens (portico.openapi.json, operationId
list_member_access_tokens)`) show `hint` (the last characters of the secret),
`lastUsedAt` and `state`:

| `state` | Meaning |
|---|---|
| `active` | Working. |
| `awaiting_approval` | The owner approves new devices by hand, and a member's new token waits for that approval like any new device. |
| `expired` | Past its `expiresAt`. |
| `revoked` | Revoked where it is managed. |
| `ended` | It stopped working without being revoked by hand; `endedReason` says why (`member_disabled`, `profile_deleted`, `not_approved`). |

Revoke with `DELETE /v1/admin/api-keys/{id} (portico.openapi.json, operationId
revoke_api_key)`, `DELETE /v1/me/access-tokens/{id} (portico.openapi.json,
operationId revoke_access_token)` or, for the owner, `DELETE
/v1/admin/access-tokens/{id} (portico.openapi.json, operationId
revoke_member_access_token)`. Revoking takes effect on the next request and is
safe to repeat.

`GET /v1/me/api-access (portico.openapi.json, operationId
get_api_access_options)` tells a client the scopes, the maximum number of keys
and tokens, whether the owner allows members to make tokens, and the request
rate.

### What ends a key or token

A key or token has a lifetime of its own. It stops working only when:

- it is **revoked** (by whoever made it, or by the owner);
- it reaches its **`expiresAt`**;
- its member is **disabled or deleted** (enabling the member again does not
  bring it back; make a new one);
- the profile it was made under is deleted.

It does **not** stop when its member changes their password, signs out
everywhere, or signs out a device, and it does not stop when the owner changes
the member's library access. Password changes and sign-out end signed-in sessions. Permission changes apply
live. All leave what the member has connected working, under current permissions.

A key or token always acts as its member **as they are now**. If the member's
role is lowered it can no longer do what that role allowed (the calls answer
`403`), and if their library access is narrowed it sees less; if the member is
promoted it can do more, but never beyond the scopes it was given.

**Restoring a backup.** Keys and tokens are part of the server's database, so a
restore brings back the ones that existed when the backup was taken, and they
keep working. A key that was revoked *after* that backup is therefore valid
again once the older database is restored: after restoring, check the lists and
revoke anything that should not be there.

## Limits

Each key or token is limited on its own, so one script cannot slow the server
for the people using it.

- **Rate.** 600 requests a minute by default (the owner can change the number).
  Every answer carries `RateLimit-Policy: "credential";q=600;w=60` and
  `RateLimit: "credential";r=<remaining>`. Over the limit the answer is `429`
  `rate_limited` with `Retry-After` in seconds.
- **Requests in flight.** 4 at once per credential (and 2 long-lived requests
  such as the event feed). A fifth is answered `429` `credential_busy` with
  `Retry-After: 1`. Media bytes are not counted here.
- **Sizes.** Pages are at most 100 rows (200 where a route says so); request
  bodies are bounded per route (`x-max-body-bytes` in `portico.openapi.json`);
  an unknown query parameter is refused rather than ignored on every typed route.
- **How many.** 100 API keys per server and 25 personal access tokens per member
  may be in use at once.

A `503` `server_busy` with `Retry-After` is the server itself being busy, not
your limit; retry after the stated delay.

A `503` `writer_capacity` on a change means the server's database writer
refused it before it began: nothing was saved. Send the same request again
after `Retry-After`, which is between 1 and 30 seconds. Any route that changes
something can answer it.

## Browsers

A key or token is a bearer secret. Do not put one in a web page other people can
load.

A page on any origin may call the API with a key or token in `Authorization`
(or a media URL carrying its grant); the server answers its preflight and its
request with `Access-Control-Allow-Origin` set to that origin. There is nothing
to configure. Cookies never authenticate a request here and are never allowed
across origins (no `Access-Control-Allow-Credentials`). A request from another
origin without a token, and any cross-origin first-run setup, is refused with
`403 origin_not_allowed`. CORS permission does not replace authentication,
scopes or library restrictions.
