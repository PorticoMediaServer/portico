# Automation qualification

Portico's compatibility adapter is a bounded automation vocabulary, not a
Jellyfin player server. The current 30 behaviours and four registered spellings
per behaviour stay in place. No smaller **qualified** minimum has been established.
A source inspection or adapter unit test is not a stock-tool acceptance test.

Before advertising a supported version, run its actual released HTTP client on
GitHub-hosted disposable infrastructure and retain a credential-free request
transcript. Pin the source commit plus release/package or container digest. A
moving branch is research evidence only. Every recipe must test restricted
libraries, least privilege, cancellation, overload/Retry-After and revoked grants.
Never publish app passwords, access tokens, media grants or real paths in evidence.

| Tool recipe | Required actual-client evidence | Current qualification |
| --- | --- | --- |
| Sonarr | Connection, provider/path lookup, import/rename/delete changed paths; no full-scan fallback | Pending released pin and trace |
| Radarr | Separate movie/provider/replacement/delete flow; exact targeting | Pending released pin and trace |
| Bazarr | Library/item/numbered-episode lookup, ValidationOnly sidecar publication; explicit library fallback refusal | Pending released pin and trace |
| Seerr or named Jellyseerr release | Dedicated owner/admin app-password wizard, derived-key POST/GET readback, bounded library/movie/show/season/episode sync; no cross-grant recovery | Pending released pin and trace |
| Home Assistant | Declared released Python dependency, setup and actual entity construction; playing/paused position/time; monitoring-only feature flags; account/global authority | Adapter field regression only; released-tool acceptance pending |
| Jellystat | REST setup/poll/users/folders/latest/hierarchy/media facts, websockets disabled, correct IDs/ticks and empty plugin handling | Blocked for the inspected mixed-case media-info request; see below |
| Lidarr Custom Script (if advertised) | Actual released tool invokes script for artist/album import, move/delete with its real environment paths; strict targeting and retry | Pending separate released-tool test |

Jellystat source `e0a38eeeff4c4bbaba60abad5f76e8a54415d1a8`,
`backend/classes/jellyfin-api.js:322`, requests `/Items/{id}/playbackinfo`.
The current adapter registers `/Items/{id}/PlaybackInfo` and the entirely
lowercase form (plus their `/mediabrowser` copies). The mixed-case request is
absent. Do not advertise that Jellystat version as qualified. Adding an endpoint
or treating routing as case-insensitive requires a separate decision; this work
adds neither. A REST-polling setup does not establish complete playback history.

The research inspected the following immutable upstream source pins, which are
**not** reviewed release qualification pins:

| Project | Research source pin |
| --- | --- |
| Sonarr | `c018578e4b79ee048575c02d7a8aa2b929e4b837` |
| Radarr | `479b6f2a17b5e5b0378659bc27401750f10829a7` |
| Lidarr | `137653eb6d2c470b3345155e1c76ff19b1a5bab0` |
| Bazarr | `bfe4593b04caa16eb4a09c2a233777b0bd7723b6` |
| Seerr | `886adac27597b45cd901099cd02a0b46d9d3cb7f` |
| Home Assistant | `e0f1a46e1e7f7c24d127274d727c567108a14050` (declares `jellyfin-apiclient-python==1.19.0`) |
| Jellystat | `e0a38eeeff4c4bbaba60abad5f76e8a54415d1a8` |

Do not remove unobserved aliases until these real released-tool traces establish
which spellings each supported recipe needs. Prefixes may come from configured
base URLs even when absent from source literals. Preserve every retained route's
access, audience, scope, lane, cost, admission, body, query and cancellation bounds.
No generic proxy, player endpoints, WebSocket or Playback Reporting plugin API is
part of this tier. Legacy Overseerr and Tautulli are Plex tools; Seerr is a
different current project. A Jellyseerr release needs its own named pin and trace.
