<div align="center">
  <a href="https://getportico.tv">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="website/public/brand/portico-wordmark-white.svg">
      <img src="website/public/brand/portico-wordmark-black.svg" alt="Portico" width="420">
    </picture>
  </a>

  <h1>Portico Media Server</h1>

  <p><strong>Portico is a free personal media server that turns your media collection into a refined streaming experience across all of your devices.</strong></p>

  <p>
    <a href="https://getportico.tv">Website</a> ·
    <a href="https://github.com/PorticoMediaServer/portico/releases">Releases</a> ·
    <a href="https://github.com/PorticoMediaServer/portico/issues">Report an issue</a>
  </p>

  <p>
    <a href="LICENSE"><img alt="GPL-3.0-or-later" src="https://img.shields.io/badge/license-GPL--3.0--or--later-59636e?style=flat-square"></a>
    <img alt="Prerelease software" src="https://img.shields.io/badge/status-prerelease-e09f3e?style=flat-square">
  </p>
</div>


---

Portico is a media server you run yourself. Point it at your movies, shows, music and books, and it gives you a streaming app on your phone, TV and browser that feels like the services you already pay for, without the subscription or the ads.

It's free. No app feature is locked behind a payment. You don't need a Portico Account either: local accounts and direct connections work on their own.

Portico is still prerelease, so expect rough edges and read the release notes before you install or upgrade.

## Getting started

- [Install Portico](INSTALLING.md)
- [Build it from source](BUILDING.md)
- [Download a release](https://github.com/PorticoMediaServer/portico/releases)

Once it's installed, open `http://127.0.0.1:32500/` on the same machine and finish setup before you make the server reachable from anywhere else. Then add the folders that hold your media. Portico's service account needs permission to read them.

Not every platform has a signed or store build yet, even where the source supports it. The release notes say what's available.

## What's in here

| Folder | What it is |
| --- | --- |
| `server/` | The media server and its API, written in Go |
| `web/` | The web app every server includes |
| `apps/native/` | Phone, tablet, Apple TV, Android TV and Fire TV apps |
| `apps/vega/` | The app for Amazon's Vega TVs |
| `apps/desktop/` | The desktop app |
| `packages/` | Code the apps share: client logic, API types, design, text and players |
| `apikit/` | The Go toolkit behind the API |
| `website/` | getportico.tv |

The Portico Services infrastructure lives in a separate private repository. The Roku app isn't in this repository.

## Building on Portico

Owners can create API keys, and members can create personal tokens where the owner allows it. The local API covers title lookups, changed-path reports, webhooks, an event feed and an MCP endpoint for AI assistants. None of it needs Portico Services.

- [API guide](server/api/guide/README.md)
- [Libraries, naming and matching](docs/libraries.md)

## Bugs, ideas and contributions

Found a bug? [Open an issue](https://github.com/PorticoMediaServer/portico/issues). Ideas and questions go in [Discussions](https://github.com/PorticoMediaServer/portico/discussions). If you'd like to send code, read [CONTRIBUTING.md](CONTRIBUTING.md) and [CLA.md](CLA.md) first. Small, obvious fixes are welcome; anything bigger should start with an issue.

Portico is built and maintained by [Justin Ehler](https://ehler.ca), who makes the final call on what goes in.

## Support, security and licence

Portico comes **as is, with no warranty**. Help is offered when I can, not guaranteed; see [SUPPORT.md](SUPPORT.md). Report security problems privately, as described in [SECURITY.md](SECURITY.md).

The code is licensed under the GPL, version 3 or later ([LICENSE](LICENSE)). Third-party components keep their own licences ([THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)). The Portico name and logo are covered separately in [TRADEMARKS.md](TRADEMARKS.md).

Copyright © 2026 Justin Ehler.
