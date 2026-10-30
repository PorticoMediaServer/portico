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

Portico organizes and streams the media on your own storage. It is free: no app capability is paywalled. It runs fully without Portico Services, using local accounts and direct connections.

This is prerelease software. Read the release notes before installing or replacing a build.

## What's in this repo

| Folder | Contents |
| --- | --- |
| `server/` | Go media server and API |
| `web/` | Server web app and Portico Services web app |
| `apps/native/` | Phone, tablet, Apple TV, Android TV and Fire TV clients |
| `apps/vega/` | Amazon Vega client |
| `apps/desktop/` | Desktop client |
| `website/` | getportico.tv |
| `packages/` | Shared client logic, generated contracts, design, strings and media cores |
| `apikit/` | Public Go API toolkit |

Portico Services infrastructure stays in a separate private repository. Roku is not included in this snapshot.

## Install and build

See [installation](INSTALLING.md), [building from source](BUILDING.md), [release packaging](RELEASING.md) and the [available releases](https://github.com/PorticoMediaServer/portico/releases). Availability and signing vary by platform; source support does not imply a store release is available.

Open `http://127.0.0.1:32500/` after installation and complete setup before exposing the address. Add media folders its service account can read. A Portico Account is optional.

- [Libraries, naming and matching](docs/libraries.md)
- [API developer guide](server/api/guide/README.md)
- [Security policy](SECURITY.md)
- [Third-party notices](THIRD-PARTY-NOTICES.md)

## Integrations and contributions

Owners can create API keys; members can create personal tokens where allowed. The local API offers bounded title queries, changed-path reports, webhooks, an event feed and an MCP endpoint. These work without Portico Services.

[Report bugs](https://github.com/PorticoMediaServer/portico/issues), or propose ideas in Discussions. Read [CONTRIBUTING.md](CONTRIBUTING.md) and [CLA.md](CLA.md) before submitting work. Justin Ehler is the sole maintainer and makes the final product and release decisions.

## Support and licence

Portico is provided **AS IS, without warranty**. Support, responses, fixes and updates are voluntary; see [SUPPORT.md](SUPPORT.md).

The public code is GPL-3.0-or-later; see [LICENSE](LICENSE). Third-party components keep their licences. [TRADEMARKS.md](TRADEMARKS.md) covers the Portico name and brand. Developed by [Justin Ehler](https://ehler.ca). Copyright © 2026 Justin Ehler.
