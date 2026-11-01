---
title: Authentication architecture
description: Choose API keys, personal access tokens or server sessions, and implement server account or Portico Account sign-in.
category: developers
order: 3
audience: Developers
updated: 2026-10-18
---

## Choose a credential

An interactive app uses a server session. A script or assistant normally uses an API key or personal access token.

| Credential | Created by | Portico Web location |
| --- | --- | --- |
| API key (`pk.`) | Server owner | **Settings → Server → Integrations → API keys → Create a key** |
| Personal access token (`pat.`) | A member, when allowed by the owner | **Settings → Account → Profile & account → Personal access tokens → Create a token** |

Choose only the scopes the integration needs. `read`, `playback` and `library` are separate; `admin` includes all three but still can't exceed the creator's role or current library access. The secret is shown once. Store it privately.

With a signed-in session, API creation uses `POST /v1/admin/api-keys` or `POST /v1/me/access-tokens`. Both accept a unique `operationId`, `name`, `scopes` and optional `expiresAt`. For example:

```json
{
  "operationId": "create-library-reader-1",
  "name": "Library reader",
  "scopes": ["read"]
}
```

## Send an API key or token

Use the `Authorization` header, not an `X-API-Key` header:

```http
GET /v1/libraries HTTP/1.1
Authorization: Bearer <your-key-or-token>
```

On `GET` or `HEAD`, `?apiKey=<secret>` is also accepted. Don't send both forms. The server removes the query credential before its handlers and logging, but browser history, proxies and other tools can still see the original URL. Treat that URL as a secret. Other HTTP methods require the header.

Keys and tokens work from any browser origin through CORS. Cookies don't authenticate Server API requests and aren't allowed as cross-origin credentials. A cross-origin request without an allowed bearer credential or resource grant is refused; setup requires the same origin as Portico Web bundled with the server.

Keys can't perform session-only operations. These include sign-in and refresh, passwords and two-step verification, credential management, ownership, claiming, profile selection, invitation acceptance and pairing. Check the operation's `x-scope` in `server/api/portico.openapi.json`. A `403 session_required` means signing in is required; `403 insufficient_scope` means the key lacks the operation's scope.

Changing a password or signing out doesn't end keys or tokens. Revoke them separately. Expiry, disabling or deleting the member, or deleting the issuing profile also ends them. Permission changes apply live. Restoring an older database can restore a key revoked after that backup.

## Server account sign-in

No Portico Account is required. Call `POST /v1/direct/sign-in` with a JSON `username` and `password`. Send a stable random `X-Portico-Installation-Id` of 32–128 letters, digits, hyphens or underscores, plus descriptive `X-Portico-Device-Name`, `X-Portico-Device-Platform`, `X-Portico-App` and `X-Portico-App-Version` headers.

Handle device approval, required password changes and two-step challenges before assuming sign-in is complete. Complete a two-step challenge at `POST /v1/auth/two-factor/challenge` with its `token` and the authenticator or recovery `code`.

Success returns a `session` containing an `accessToken` and refresh credentials. With several profiles, a PIN or a required password change, the initial session can be account-only. Select an allowed profile with `POST /v1/direct/profiles/{id}/select`; don't use an account-only session for media.

## Portico Account sign-in

An account-based app starts with a Portico Account session, lists eligible servers and verifies signed connection information. It verifies the selected server's identity before exchanging account identity evidence for a server-scoped session.

The server's `POST /v1/direct/portico-challenge` supplies a challenge for a hosted identity assertion. Submit the assertion as `porticoIdentity` to `POST /v1/direct/sign-in`. The server verifies the signed identity and its existing local membership. This isn't permission to send a Portico Account bearer token to the server. Use Portico's shared connection and identity services for this sequence; external server integrations can use server account credentials or scoped keys.

## Refresh and sign-out

Server access tokens last up to 15 minutes. Refresh with `POST /v1/auth/refresh`, sending `refreshToken`, `installationId` and a unique `requestId` in JSON. Store both replacement tokens atomically. Serialize refreshes; retry a lost response with the same request ID and predecessor token.

`DELETE /v1/sessions/current` ends the session family. HTTP and HTTPS use the same session rules; HTTP doesn't encrypt the tokens in transit. Apple apps restrict plain HTTP to local-network addresses.

## Handle each failure state

Handle account expiry, removed membership, an offline server, invalid routes and expired server sessions separately. Repeating a request with the wrong credential won't recover the session.
