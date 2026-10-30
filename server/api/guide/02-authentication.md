# 2. Sessions and signing in

An app signs a person in. A script or assistant normally uses a deliberately
scoped [API key or personal token](07-keys-tokens-and-scopes.md). Local sign-in
needs no Hosted account or application registration.

## Installation headers

Generate and persist a random installation ID, 32–128 characters using letters,
digits, `-` and `_`. Keep it stable for this installation. Send it during sign-in
and subsequent requests:

```text
X-Portico-Installation-Id: my-player-6de72ae2874c46f89b8ffef61d29d0ca
X-Portico-Device-Name: Sam's laptop
X-Portico-Device-Platform: linux
X-Portico-App: My player
X-Portico-App-Version: 1.0
Content-Type: application/json
```

The installation ID is not a secret or proof of a device private key. The issued
refresh token is a secret and is bound to it. Access tokens are bearer credentials.

## Direct sign-in

`POST /v1/direct/sign-in`:

```json
{"username":"sam","password":"replace-with-your-password"}
```

Success returns the account/profile snapshot and `session`, whose `accessToken`
is the bearer credential. There is no separate wire `accountToken` anymore.
For one profile with no PIN and no required password change, the session can
already browse. Otherwise its role is `account` until a profile is selected;
it can use account routes but cannot authorize media. The response can instead
carry `deviceApprovalPending: true`, or a two-factor `challenge` without a session.

For two-factor, call `POST /v1/auth/two-factor/challenge` with
`{"token":"<challenge.token>","code":"<current code>"}`. This returns the sign-in
result after the factor is proven. Follow `passwordChangeRequired` before
trying to use an account-only session as a viewer.

Choose a profile from the returned `profiles`:

```text
POST /v1/direct/profiles/{id}/select
Authorization: Bearer <session.accessToken>
```

```json
{"pin":"1234","installationId":"my-player-6de72ae2874c46f89b8ffef61d29d0ca","trust":true}
```

For a profile without a PIN, omit `pin`. Selection returns a new `session` and
optionally `trustedSelection`; store the new credentials. Remembered trust can
skip a future PIN by sending its `trustToken` for the same installation.
`GET /v1/direct` reads the account snapshot; `GET /v1/me` reads the viewer.
The legacy session-creation sign-in route is removed. Source: `internal/httpapi/direct_identity.go`,
`internal/identity/direct_accounts.go` and `direct_profiles.go`.

A shell example for a one-profile account without a PIN or second factor:

```bash
BASE=http://127.0.0.1:32500
INSTALLATION=my-player-6de72ae2874c46f89b8ffef61d29d0ca
# Keep the password out of shell history and encode it as JSON.
read -r -p 'Username: ' USERNAME
read -r -s -p 'Password: ' PASSWORD; printf '\n'
SIGN_IN=$(jq -nc --arg u "$USERNAME" --arg p "$PASSWORD" '{username:$u,password:$p}' |
  curl -fsS "$BASE/v1/direct/sign-in" -H 'Content-Type: application/json' \
    -H "X-Portico-Installation-Id: $INSTALLATION" --data-binary @-)
unset PASSWORD
TOKEN=$(printf '%s' "$SIGN_IN" | jq -er '.session.accessToken')
curl -fsS "$BASE/v1/libraries" -H "Authorization: Bearer $TOKEN"
```

A full app must handle the account-only, PIN, two-factor, password-change and
approval cases above. Do not print or log the session envelope.

## Portico Account members

The server accepts a Hosted-signed identity assertion for membership it already
knows. Ask `POST /v1/direct/portico-challenge {"nonce":"<random nonce>"}` with
installation headers; verify the server's returned signed proof. Hosted signs
an assertion answering that challenge. Send it as `porticoIdentity` to
`POST /v1/direct/sign-in`, without username/password. The server verifies it
offline against its pinned Hosted trust. This server's own membership and role
still decide admission. A third-party Portico Account issuance flow is not
provided here; use local credentials or a TV code. Old Hosted ticket attachment
and offline proof sign-in routes are removed.

## Refresh and sign-out

Access lasts up to 15 minutes, bounded by the family authorization horizon. A
local family has a rolling 90-day sign-in window. Save the issued fields;
do not calculate expiry from assumptions about another account authority.

`POST /v1/auth/refresh` takes no access bearer:

```json
{"refreshToken":"<issued refreshToken>","installationId":"my-player-6de72ae2874c46f89b8ffef61d29d0ca","requestId":"refresh-20261013-1"}
```

It returns the session envelope directly, not under `session`. Save both new
tokens atomically. Retry a lost response with the same predecessor, installation
and request ID. The exact receipt remains recoverable until the successor is
used or its horizon expires. A predecessor used outside that exact retry can
revoke the family. Do not refresh independently in several parts of one app.
HTTP and HTTPS have the same credential semantics.

`DELETE /v1/sessions/current` signs out the whole session family and accepts an
authentic expired/retired family token so a stale device can still sign out.
Send no query or body. Device administration uses `/v1/devices`. Account managers can list
`GET /v1/direct/sessions`, revoke `DELETE /v1/direct/sessions/{id}`, or call
`POST /v1/direct/sessions/sign-out-everywhere` with their ordinary session. Disabling an account, changing its password or
blocking a device ends sessions. Ordinary permission changes apply live.

## TV code

1. The waiting device calls `POST /v1/quick-connect`:

   ```json
   {"requestId":"tv-20261013-1","deviceName":"Living Room TV","platform":"tvos","appVersion":"1.0"}
   ```

   The 201 response has `deviceCode` (secret), `userCode`, `verificationUri`,
   `verificationUriComplete`, `expiresAt`, `expiresIn` and `interval`. Codes
   currently last ten minutes; the initial poll interval is five seconds.
2. Display the user code and verification address. A signed-in viewer calls
   `POST /v1/quick-connect/review {"userCode":"<code>"}` to review the device,
   then `POST /v1/quick-connect/decision`:

   ```json
   {"requestId":"decision-20261013-1","userCode":"ABCD-1234","decision":"approve"}
   ```

   A decision is 204; `deny` refuses it. **The new device gets the approver's
   identity and role**, including owner authority when the owner approves.
3. Poll `POST /v1/quick-connect/token {"deviceCode":"<secret>"}` at the returned
   interval. Handle `authorization_pending` and `slow_down` with the returned
   `Retry-After`; stop on expiry or refusal. Success is the session envelope.
   Device approval policy and remote sign-in policy still apply.
4. `POST /v1/quick-connect/cancel` with the device code cancels an unused attempt.

Source: `internal/httpapi/onboarding.go`, `internal/identity/quick_connect.go`.
Store tokens privately. A browser page on another origin may call any route
with a token in `Authorization` and nothing to configure; without a token it
gets `403 origin_not_allowed`, so sign-in and setup stay with the server's own
web app. Native clients can omit Origin. Use returned verification URLs rather than hard-code Hosted.
