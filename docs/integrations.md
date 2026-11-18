# Integrations

Outside tools use Portico's own API: `/v1`, described route by route in
[`server-next/API.md`](../server-next/API.md) and generated as
[`server-next/api/openapi.json`](../server-next/api/openapi.json). Portico is pre-release; the API's
compatibility promise starts with the first stable release.

## Credentials

- **Server API keys** (Settings → Integrations, owner only) carry scopes: `read`, `playback`,
  `library` (scans and changed files) and `admin`. A key's secret is shown once.
- **Personal tokens and app passwords** belong to one person and profile (`/v1/me/tokens`,
  `/v1/me/app-passwords`). An app password has no scopes: its tool may do what that person may.
- Every key, token and app password is turned into its person and profile in one place. It is
  refused wherever that person could not sign in: outside viewing hours, away from home for an
  account limited to home, from a blocked network, or for an account that is turned off. No key
  can make or revoke keys, change a password, PIN or sign-in setting, change the owner or an
  administrator, or restore a backup.

## Changed files and scans

- `POST /v1/admin/libraries/{id}/scan` with `{"folder": "<path>"}` scans one folder of a library
  (`allFiles` rescans every file). It answers `202` with a job, and joins a scan already asked
  for. It needs a key with `library`, or the owner or an administrator.
- The assistant address's `report_changed_files` tool and the compatible
  `/Library/Media/Updated` route do the same for a list of paths: each library folder that holds
  a path is scanned, and a path outside every library is ignored.

## Webhooks

The owner adds up to 20 (`/v1/admin/webhooks`), each with its own events, address and secret,
and can send a test. Nothing is sent anywhere the owner did not add.

## Assistants (MCP)

With **Assistants** on (Settings → Integrations, off by default), a key may use `POST /v1/mcp`.
The tools offered: find and read titles, see what is playing, mark watched, scan and report
changed files, list unmatched titles, and read the guide and recordings. With `admin` they can
also read status, storage and recent messages. Nothing deletes or reconfigures.

## Jellyfin-compatible routes

For companion tools that only know Jellyfin, such as Sonarr, Radarr, Bazarr, Seerr and Home
Assistant. They are off by default. The owner turns them on with `compatibleRoutes` in the
integrations settings (`PUT /v1/admin/settings/integrations`); the apps have no switch for it
yet. Turning them off ends every sign-in made through them.

- They are a fixed list of 30 operations, answered at `/` and `/mediabrowser`, in any
  capitalization. Each one runs the ordinary `/v1` route as the tool's person, so every
  restriction applies.
- Sign in with the person's username and an **app password**, never their real password. A
  tool that then asks for a key gets one with the same access.
- Before sign-in they report Jellyfin version 10.10.0 (product name "Portico"), so tools'
  version checks pass. Portico's own build is never shown before sign-in.
- Nothing can be played through them: media facts are given, never a media address.
- `/Library/Media/Updated` with paths scans the folders those paths are in, `/Library/Refresh`
  scans every library, and `/Items/{id}/Refresh` scans that title's library.
- `/Sessions/{id}/Playing/Stop` stops a stream. Pause, resume and seek of another device are
  not built yet.

For a tool, select its **Emby / Jellyfin** (or **Jellyfin**) connection and enter Portico's
address, the username and the app password. The tool must see media at the same paths Portico
does; set up path mapping in the tool if not.

None of these recipes is qualified against a released tool yet; see
[integration-qualification.md](integration-qualification.md).

## DLNA

One switch in the `remote-access` settings, off by default. It answers on its own UPnP addresses
outside `/v1`, to the home networks only and never through a proxy, and serves original files
with no sign-in.
