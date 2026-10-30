# Integrations

Portico exposes one versioned native API, with two documentation tags:

- **Integrations** covers automation, notifications, catalogue facts, permitted
  session monitoring and native remote controls. These routes are intended for
  third-party tools as well as Portico's clients.
- **Clients** covers the full Portico client experience, including playback,
  account/profile selection and presentation-specific operations.

The tags do not grant access. Every request still uses the same account, profile,
credential scopes, library restrictions, admission and revocation checks. The
route registry is the source for both generated contracts and OpenAPI tags.

Within `/v1`, documented Integrations operations preserve their methods, paths,
field meanings, IDs, paging, error meanings and authorization scopes. Additive
optional fields are allowed; consumers must ignore unknown fields. Breaking
changes require a new major API version and a documented transition. Clients
routes also use `/v1` contracts, but may add client capabilities and negotiated
presentation versions more frequently; their documentation identifies those
negotiations. Reliability fixes may reject previously accepted invalid requests.
Portico is pre-release: these promises start with the first stable release.

## Integrations compatibility

The owner enables **Integrations compatibility** in Settings → Integrations.
It is off by default. It translates a limited set of existing tools' requests to
Portico's native API; it is not a player API. It does not register with another
service, discover public accounts, contact Portico Services, or send metadata to
a vendor. Local accounts remain local. Tools you configure receive the facts
permitted by the chosen credential and can store them themselves.

Create a dedicated named API key for tools that accept keys. For tools that ask
for a username and password, create a named integration app password for one
Portico account/profile and the necessary scopes. Use that account's username
and the app password in the tool. Actual account passwords, profile passwords,
PINs and owner API keys are not accepted as compatibility passwords.

The owner can Reveal and Copy keys and app passwords later. These secrets are
recoverable in the local database and its backups; actual account/profile
passwords and PINs remain one-way hashes. TV shared screens never display these
secrets. Revoking an app password also revokes its derived integration tokens.
Disabling compatibility prevents those tokens from being used.

Setup instructions sometimes name the connection type offered by another tool.
That label describes which option to select in that tool, rather than changing
Portico's accounts or API model.

### Sonarr and Radarr

Select the tool's **Emby / Jellyfin** connection, use Portico's URL and a dedicated
API key with `read` and `library` scopes. Paths must match the paths visible to
Portico, including container mounts; configure path mappings in the tool when
necessary. Optional administrator notifications also require native owner
authority and `admin` scope. Disable that option when the key lacks it.

Library updates report the specific changed paths. Portico accepts work only
when it can preserve strict targeting; it never substitutes a full-library scan.

### Lidarr

Select its **Emby / Jellyfin** connection. Its legacy `/mediabrowser` URL prefix
is accepted. The inspected Lidarr version can send a collection label such as
`music` instead of an actual path, or request an untargeted refresh. Those requests
are refused: Portico has no verified cheap no-path change check to substitute.
Use the native changed-path recipe below for reliable notifications from that
version. A successful connection test does not establish that updates are targeted.

### Bazarr

Use its server connection and a dedicated `read`/`library` key. Item/provider
lookup and numbered-season episode lookup use native catalogue identities.
Validation-only refresh rereads eligible subtitle sidecars without enriching
metadata or replacing artwork. Unavailable stored media evidence is an explicit
refusal, not permission to run a broader scan or media probe.

### Jellyseerr

For the initial administrator wizard, select its **Jellyfin** connection and enter
Portico's URL, username and a dedicated app password bound to a genuine owner or
administrator. Give the grant only the scopes needed by the configured features.
The wizard can create and retrieve a key within that grant's authority; it cannot
retrieve a different grant's keys or acquire broader scopes.

Pre-issued-key configuration remains an alternative where the tool provides it.
For end users, use Jellyseerr's own local accounts. Signing in to Jellyseerr with
Portico account passwords is not supported. Quick Connect is not provided.

### Home Assistant

Choose the **Jellyfin** integration and enter Portico's URL, username and dedicated
app password with `read` and `playback` scopes. Monitoring and native transport
commands are available only where the bound account/profile has authority.
Server-wide monitoring or administrative controls require native administrator
authority and appropriate scope. Pause, resume, seek and stop translate to native
controls with their revision checks. Unsupported commands return an error.

Volume/mute control, starting playback on another player, media-source streaming
and third-party player setup are outside this adapter's supported tier.

### Jellystat

Configure Portico's URL and a suitable scoped API key. Use REST polling:
`JF_USE_WEBSOCKETS=false`. A server-wide statistics credential requires native
administrator authority; a limited grant must not acquire global sessions or
counts. Polling observes activity from the time Jellystat is configured and may
miss activity between polls. It does not import a complete historical ledger.

No Playback Reporting plugin, plugin SQL endpoint or WebSocket protocol is
provided. The plugin list is empty. Read-only media facts are not playback URLs.

## Native changed-path notifications

Send a dedicated key with `library` scope to the native endpoint:

```http
POST /v1/libraries/paths-changed
Authorization: Bearer <key>
Content-Type: application/json

{"mode":"strict","paths":[{"path":"/media/Movies/Arrival (2016)","change":"changed"}]}
```

Use absolute paths as Portico sees them. Changes can be `added`, `changed` or
`removed`. A successful response queues targeted background work; it does not
mean matching and analysis have completed. Keep the reported directory narrow.
A source root, unresolved path or unsupported source is not a request to broaden
the scan. Handle a refusal explicitly, correct the path or source configuration,
and retry according to the response. Do not fall back to a full-library scan.

For outgoing events, use the existing native webhook endpoints documented in
OpenAPI. Webhooks are configured by the owner; no third-party destination is
added automatically. Treat payloads as permitted server metadata and configure
the destination accordingly.

## Boundaries

The compatibility routes cover integration setup, permitted catalogue reads,
strict path notifications, sessions and native controls. They do not implement
Plex authentication/server discovery, proprietary plugins, third-party players,
streaming, transcoding or downloads. Larger applications should use Portico's
native Clients and Integrations routes and their generated contracts.
