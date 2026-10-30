# Security policy

Portico is prerelease software. When security fixes are published, they normally
target the newest build; older builds may not receive patches. Support, responses,
and fixes are voluntary and not guaranteed. See [SUPPORT.md](SUPPORT.md).

## Report a problem

Use GitHub private vulnerability reporting on the affected repository's Security
tab, or email [admin@getportico.tv](mailto:admin@getportico.tv). Include the
version, operating system, installation method, steps to reproduce and impact.
Remove credentials, personal information, media filenames and private addresses
from attached logs. Do not access other people's systems or publish sensitive
details while a report is being reviewed. There is no promised response-time SLA.

## Choices the owner controls

Plain HTTP is accepted from every address. TLS is recommended on untrusted
networks but is not mandatory. Hosted-advertised routes use TLS. Home networks
is the owner's complete definition of local versus remote access; Portico does
not infer trust from Tailscale, a proxy or carrier-grade NAT. Forwarded headers
are trusted only from configured proxy peers. See [INSTALLING.md](INSTALLING.md).

First-run setup can be completed by anyone who can reach the unclaimed server;
there is no separate local setup code. Browser setup requires same-origin JSON
requests and rejects cross-site setup. Finish setup before exposing the address.

Local passwords require eight characters, with a 72-byte bcrypt limit. There
is no password-complexity rule or permanent account lockout. Failed attempts
are rate limited and delayed. Profile PINs have their own temporary attempt
limits; a PIN is not a replacement for an account password. Local two-factor
sign-in is available.

Approving a TV code signs that device in as the approver, including their role.
An owner approving their own code makes that TV an owner device. Device names
are supplied by the requesting device. Review the code on the device you mean
to approve. Cast targets receive playback-only sessions.

## Secrets, storage and privacy

Access tokens are bearer secrets: anyone holding a usable token can act within
its authority. Device records and installation IDs bind refresh to an
installation, but access requests do not prove possession of a device private
key. Revocation and current permissions are checked by the server.

The database, settings and backups are not encrypted at rest. Passwords are
bcrypt hashes, not plaintext. API key and personal token secrets are stored as
digests. Two-factor seeds, server identity/TLS keys, webhook secrets, mount
credentials and some recoverable credential responses are stored without
at-rest encryption. Protect the state directory and backups with filesystem
permissions and your own disk encryption if desired.

Built-in backups remove live sign-in sessions and pending sign-in secrets;
restore signs people out. Account passwords, two-factor seeds, approved devices
and API keys/tokens return to their backup-time state. Keys and tokens deliberately
survive restore: a key revoked after the backup can work again. Review and revoke
unwanted credentials after restoring. External certificate files and media need
separate backups.

The owner can always see what was played, by whom and on which device. Pausing
or clearing a viewer's own history does not hide the owner's play history.
Deleting a member retains their plays without their name. DVR is private per
profile; the storage file may be shared when several people record a programme.

Webhooks send owner-selected events directly to chosen HTTP/HTTPS receivers,
including private addresses. They can carry titles, names, devices and sign-in
addresses. They are HMAC signed; redirects are refused. TLS certificate checking
and payload/queue bounds still apply. An MCP assistant acts as the member whose
key or token it holds and is bounded by that credential's scopes and live role.

## Media and releases

Media readers apply size, depth, time and process bounds. On Linux the optional
decoder sandbox depends on host/container support; playback can continue with
baseline process restrictions when it is unavailable. `PORTICO_DECODER_SANDBOX=required`
requires it, and `off` disables it. This does not claim hostile media is risk-free.

Release manifests and artifact inventories have signatures. Operating-system
package signing/notarization and installer trust provisioning are separate;
preview packages may be unsigned. See [RELEASING.md](RELEASING.md) and
[release signing](server/api/release-signing.md).
