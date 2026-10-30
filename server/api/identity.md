# Profiles, restrictions, devices and account

This document covers the identity surface a client actually has to reason about:
what a profile may see, how a device is recognised and signed out, and how a
direct server handles second factors, self-registration and the tvOS Top Shelf.
Source-registered routes and their shapes are in `internal/httpapi/foundation.go`
and `device_contract.go`; other identity handlers remain in `direct_identity.go`
and `identity_routes.go`. The historical YAML is stale.

## 1. Session phases and credentials

[Authentication](guide/02-authentication.md) documents sign-in and refresh.
Direct sign-in returns `session.accessToken`. For accounts needing profile
selection or a password change, it initially has account scope; after selection
it is a viewer session. A single profile without a PIN can receive viewing
scope directly. There is no separate wire `accountToken` or profile step-up token.
The ordinary session also serves permitted account-management calls.

A Top Shelf token is a separate limited feed credential, and API keys/personal
tokens are separate scoped integration credentials. Neither is a substitute for
a signed-in session on session-only account routes. Access bearers do not prove
a device private key; refresh is bound to the approved device and installation.

## 2. Start with capabilities

```
GET /v1/auth/capabilities
```

Read this before drawing any sign-in screen. It reports the server's name, whether
setup is still required, which sign-in methods exist (`password`,
`quick-connect`, `hosted`, `self-registration`), whether two-factor is supported,
and the owner's self-registration mode.

It takes no username, and it never will. A pre-authentication endpoint that
answered questions about a specific account would be an account-existence oracle.

## 3. Content restrictions

A profile carries eight restriction fields. Three of them decide **visibility**:

- `maximumAgeRating` — a code from one named rating system (`ratingSystem`).
- `allowUnrated` — whether content this server cannot resolve to a known rating
  may be seen.
- `blockedLabels[]` — labels whose content is hidden.

Four further fields gate features rather than visibility: `allowDownloads`,
`allowLiveTv`, `allowDvr`, `allowWatchTogether`. Each is answered by the
feature that owns it.

### One predicate, every surface

The three visibility fields are enforced by a single predicate the catalogue
applies. There is exactly one implementation
(`internal/catalog/restrictions.go`), and it is appended by the browse engine,
facet counts, home rows, search, library content, the single-item read and
playback. A client must not re-implement it:

- A title the predicate refuses is **absent** from every list, and the count next
  to that list is the filtered count, not the real one. A list and its total
  always agree.
- Opening or playing such a title is refused or hidden (ordinary catalogue reads
  answer `404 not_found`). Knowing an id
  is not a way around a list that filtered it.
- The same code is used everywhere, so a client can say one clear thing.

Do not hide titles locally using `maximumAge`. The server's rule and a client's
approximation of it would drift, and a client that filtered a list the server
already filtered would only hide the wrong things.

### How ratings from different bodies are compared

A library mixes rating systems; a restriction names one. Every rating is therefore
normalised to a single ordered scale: **the minimum admission age its issuing body
attaches to it**. `GET /v1/rating-systems` publishes the tables, each ordered
ascending by that age. Render them in the order given.

The normalisation is deliberately coarse and deliberately conservative:

- Region qualifiers are stripped as a fallback (`US:PG-13` → `PG-13`), but only as
  a fallback, so a bare spelling like `N/A` is not read as its last segment.
- A rating this server does not recognise — including every spelling of "not
  rated" — is **unrated**. It never resolves to age 0, which would read as "all
  ages".
- `maximumAge: 0` is a real ceiling admitting only all-ages content. `-1` means no
  ceiling. A client that treated 0 as "unset" would quietly widen a child profile.

### Containers

A show, season, artist, album, book, author or collection carries no rating of its
own. It is visible when it holds at least one item the profile may see, or when it
holds no items at all. A series whose every episode is adult disappears as a unit;
an empty shelf stays visible, because it holds nothing to restrict.

### Changing restrictions

```
PUT /v1/direct/profiles/{id}/restrictions
```

The body is a **full replacement** guarded by `expectedRevision`. A partial patch
is not offered: a document read, shown and re-submitted whole cannot lose a field
a newer server added. Parse the document, change fields on the parsed value, and
send the whole thing back.

On success the revision advances and the new restrictions apply to the
profile's next request. Nobody is signed out: every catalogue read and every
playback start reads the live document.

The restriction identity is also folded into the viewer fence, so a cursor or a
cached page presented across a restriction change fails with
`stale_continuation` rather than being answered from the old rule.

## 4. Account changes

The normal account session manages profiles, restrictions, PINs, devices and
sessions. A selected child profile has limited account-management authority; some
device and TV-code operations remain available to restricted profiles. Only changing the account email, password or two-factor settings
requires the current password and, when enabled, a two-factor code in that same
request. There is no profile step-up proof or account token.

## 5. PIN recovery

A profile PIN separates viewing profiles within one account. An account manager's
normal session may clear or replace it without a second password prompt.

```
POST /v1/direct/profiles/{id}/pin-reset   → {"pin":"1234"} or {"pin":""} to clear
```

A reset retires the profile's remembered device trust and its viewing families: a
device trusted under the old PIN does not stay trusted under the new one.

## 6. Avatars

```
POST   /v1/direct/profiles/{id}/avatar     multipart "image", or raw bytes
GET    /v1/profiles/{id}/avatar?v=&size=
DELETE /v1/direct/profiles/{id}/avatar
GET    /v1/direct/profiles/avatars         every profile's version in one read
```

The declared content type and the file name are ignored. The bytes are sniffed
(JPEG, PNG, WebP only), the image is decoded, cropped to its largest centred
square, and re-encoded to PNG at 64, 160 and 512 pixels. Only those renditions are
stored, so EXIF, colour profiles and trailing data never survive an upload. The
original may not exceed 4 MiB, 8000 pixels on a side, or 24 million pixels.

Reading an avatar requires a viewing session on the same account: a profile
picture is personal data, not public artwork. Pass `v` (the version from the
avatar document) to get an immutably cacheable response; a stale `v` answers
`404 avatar_not_found` rather than serving a different picture under the URL the
client asked for.

## 7. Devices

A device record is what a person recognises on a sign-out screen — "the TV in the
living room" — rather than a token family id.

```
POST   /v1/devices                     register or refresh at sign-in
GET    /v1/devices                     the list
PATCH  /v1/devices/{id}                rename; last profile; remember-account
POST   /v1/devices/{id}/approval       owner approves or denies
POST   /v1/devices/{id}/sessions/bind  bind a new viewing family
DELETE /v1/devices/{id}/sessions       sign this device out
DELETE /v1/devices/{id}                forget it entirely
POST   /v1/direct/sessions/sign-out-everywhere
```

**Installation id.** Generate 256 bits of randomness once per install and keep it.
It is not a fingerprint and grants nothing, but it is the only thing standing
between a stranger and this installation's remembered-account list, so the server
refuses anything shorter than 32 characters. Regenerating it makes the device look
new to the owner's approval policy and orphans its remembered accounts.

**Registration is an assertion.** Everything in the body is what the client says
about itself. The peer address follows the owner’s trusted-proxy policy; arbitrary
client-supplied forwarding headers are ignored. Re-registering the same installation refreshes the record
rather than creating a second one, and it does **not** overwrite a name a person
gave the device.

**Approval.** When the owner sets `deviceApproval` to
`owner-approves-new-devices` (the same value as the settings switch
`deviceApprovalRequired`), a member's newly seen device is stored `pending` and
registration answers `403 device_approval_pending`. The owner's own devices are
never held, and a screen paired as a Cast target is vouched for by the session
that paired it. The owner and administrators see every member's devices at
`GET /v1/admin/access/devices`. The record is still written —
the owner cannot approve an arrival that was rolled back. A pending device cannot
bind a viewing family. Denying a device also signs it out, so turning the policy
on does not leave the very device the owner objects to signed in.

**Binding and sign-out.** Token families remain the unit of revocation. Sessions are bound when issued. The optional
`sessions/bind` call verifies that existing binding; it cannot move the family to
another device. Signing a device out revokes only its families and keeps
the record, so the person keeps seeing the device and signing back in on it does
not look like a new arrival.

**Sign out everywhere** needs no password: the session is the authority. It revokes every viewing
family, forgets every remembered browser entry, and bumps the account epoch, which
sweeps account sessions and remembered profile trust.

## 8. Remembered browser accounts

A browser cannot keep a list of signed-in accounts the way a native app can, so
the server keeps the *descriptors* — who has used this installation, what to call
them, which picture to draw — while the browser keeps the credentials.

```
POST   /v1/auth/remembered-accounts              {installationId}   (unauthenticated; lists)
POST   /v1/direct/remembered-accounts            {installationId, automaticSignIn}
DELETE /v1/direct/remembered-accounts/{accountId} {installationId}
```

The installation id always travels in the body, never in the address: it is
what a device renews its sign-in with, and addresses end up in proxy and access
logs. For the same reason `GET /v1/devices` marks the caller's own device from
its session and shows an installation id for that device only.

Nothing in the list is a credential, and choosing an entry signs nobody in: it
tells the client which stored token to present, and that token is verified
normally. At most ten accounts are kept per installation and at most one may sign
in automatically. Setting `rememberAccount: false` on the device record forgets
the entry, so the switch is a real withdrawal rather than a flag the list ignores.

## 9. Two-factor on a direct server

```
GET    /v1/direct/two-factor
POST   /v1/direct/two-factor/enrol    password → secret, otpauth URI, 10 recovery codes
POST   /v1/direct/two-factor/verify   a code puts it in force
DELETE /v1/direct/two-factor          password
POST   /v1/auth/two-factor/challenge  finish a challenged sign-in
```

The enrolment material is returned **once**. A client that loses it must re-enrol.
The factor is not in force until a code from it verifies, so a mistyped secret
cannot lock the account holder out of their own server — and while enrolment is
pending, sign-in is not challenged.

A code is accepted once, even inside its own thirty-second window, so a code a
bystander read off a screen cannot be replayed. One step of clock drift either way
is tolerated.

Once a factor is in force, `POST /v1/direct/sign-in` returns a `challenge` instead
of a `session`: the password has been proven and nothing else has. Exchange
the challenge and a code at `POST /v1/auth/two-factor/challenge`. Five wrong
attempts retire the challenge, and after that even a correct code is refused —
start a new sign-in. A recovery code is accepted anywhere a code is, and is
consumed on use.

## 10. Self-registration

```
GET /v1/direct/registration-policy   (owner)
PUT /v1/direct/registration-policy   (owner)
POST /v1/auth/register
```

The owner's policy is `off` (default), `invite-only` or `open`. An unset or
unrecognised stored value always reads as `off`: a policy nobody chose must never
open a server to the network it sits on. The policy is re-read inside the
registration transaction, so an owner closing registration cannot be raced.

A self-registered account is always a **member with no libraries**. Reaching the
server is not evidence of what its owner wants shared; an owner grants libraries
afterwards. Under `invite-only` a valid invitation code is required, and the
invitation — not the request — decides the libraries. Invitations belong to the
administration surface; while no redeemer is installed, `invite-only` refuses
everything rather than degrading to `open`.

## 11. Mid-playback profile switch

```
POST /v1/direct/profile-switch   { installationId, outgoingProfileId }
```

Call this **before** selecting a new profile on a device that may be playing
something.

Portico **ends** the outgoing profile's active occurrences on that device. It does
not hand them over, and this is a deliberate choice rather than a limitation: a
handover would carry one profile's position, restrictions and personal state into
another's session, which is exactly what profile separation exists to prevent, and
a child profile inheriting a parent's occurrence would inherit content its own
restrictions forbid.

Ending runs through the family revocation hook, so occurrences, queues and
physical readers retire through the current playback service rather than being
orphaned. The switch is local to one device: the same profile playing on another
screen is untouched.

## 12. Top Shelf

```
POST /v1/topshelf/token   { installationId }   → a limited feed token (read expiresAt)
GET  /v1/topshelf                              → the feed
GET  /v1/topshelf/art/{id}?token=              → one entry's poster
```

The feed is built from the profile's home rows, under the same library permissions
and the same content restriction every other read applies: at most four sections
of at most ten entries, each carrying the shape of the row it came from.

The token is bound to the account, the profile and an approved device. Denying or
forgetting the device takes the feed away without a separate sweep. Because tvOS
image loading cannot set an Authorization header, artwork accepts the token in the
query string — and the library permission and the restriction are both re-proven
on that route, so the token cannot reach artwork the feed itself would not have
listed.

`imageUrl` values are valid only for the life of the token. Refetch the feed
rather than caching an image URL past `expiresAt`.

## 13. Error codes

| Code | Status | Meaning |
| --- | --- | --- |
| `not_found` | 404 | A title is absent or hidden from this profile. |
| `restrictions_changed` | 409 | `expectedRevision` is stale; re-read and retry. |
| `avatar_unsupported` | 415 | Not a JPEG, PNG or WebP. |
| `avatar_not_found` | 404 | No avatar, or a stale `v`. |
| `two_factor_required` | 401 | A code is needed and none was supplied. |
| `two_factor_invalid` | 403 | Wrong code, or a spent recovery code. |
| `two_factor_enrolled` | 409 | A factor is already in force. |
| `two_factor_missing` | 409 | No factor to verify or disable. |
| `device_not_found` | 404 | No such device on this account. |
| `device_approval_pending` | 403 | Waiting for the owner. |
| `device_denied` | 403 | The owner refused this device. |
| `registration_closed` | 403 | Self-registration is off. |
| `registration_invitation_required` | 403 | `invite-only` and no valid code. |
| `username_taken` | 409 | Choose another username. |
| `topshelf_token_expired` | 401 | Mint a new feed token from the app. |
| `authentication_busy` | 503 | Password-hashing capacity exhausted; retry shortly. |

Sessions are bearer credentials associated with an installation and refresh family. The server does not require proof of a device private key; the installation identifier is not cryptographic sender constraint. Remembered-account lookup sends that identifier in a POST body and returns descriptors only, never credentials.
