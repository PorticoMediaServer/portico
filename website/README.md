<div align="center">
  <a href="https://getportico.tv">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/PorticoMediaServer/getportico-tv/main/public/brand/portico-wordmark-white.svg">
      <img src="https://raw.githubusercontent.com/PorticoMediaServer/getportico-tv/main/public/brand/portico-wordmark-black.svg" alt="Portico" width="420">
    </picture>
  </a>

  <h1>getportico.tv</h1>

  <p><strong>The public website and user documentation for Portico.</strong></p>

  <p>
    <a href="https://github.com/PorticoMediaServer/portico-server">Portico Media Server</a> ·
    <a href="https://getportico.tv">Visit the website</a> ·
    <a href="https://github.com/PorticoMediaServer/getportico-tv/issues">Report an issue</a>
  </p>

  <p>
    <a href="LICENSE"><img alt="GPL-3.0-or-later" src="https://img.shields.io/badge/license-GPL--3.0--or--later-59636e?style=flat-square"></a>
    <img alt="Prerelease software" src="https://img.shields.io/badge/status-prerelease-e09f3e?style=flat-square">
  </p>
</div>

---

> Portico Media Server is the heart of the project. Start with the [primary `portico-server` repository](https://github.com/PorticoMediaServer/portico-server) or visit the live site at [getportico.tv](https://getportico.tv).

This repository contains the public Portico website. It explains the project, hosts user documentation, and points visitors to verified downloads published through GitHub Releases.

## Technology

| Area | Technology |
| --- | --- |
| Site generator | Astro |
| Language | TypeScript |
| Styling | CSS |
| Content | Astro content collections and Markdown |
| Automation | GitHub Actions |

## Development

```sh
npm ci
npm run check
npm run build
```

The generated site is written to `dist/`. Deployment configuration is intentionally separate from Hosted Services and is documented only where it is safe and useful to users.

## Support and warranty

Portico is provided **"AS IS", without warranty**, to the extent permitted by applicable law. Support is voluntary and not guaranteed, including responses, fixes, maintenance, and updates. See [SUPPORT.md](SUPPORT.md) and [LICENSE](LICENSE) for details.

## Feedback and contributions

Website problems and documentation feedback are welcome through [GitHub Issues](https://github.com/PorticoMediaServer/getportico-tv/issues). Portico does not accept external code contributions. See [CONTRIBUTING.md](CONTRIBUTING.md).

Please report security issues privately as described in [SECURITY.md](SECURITY.md).

## License and trademarks

This repository is licensed under `GPL-3.0-or-later`. See [LICENSE](LICENSE). The license does not permit unofficial sites or builds to be represented as official Portico services; see [TRADEMARKS.md](TRADEMARKS.md).

---

Made with ❤️ in Nova Scotia, Canada | Developed by [Justin Ehler](https://ehler.ca)  
Copyright © 2026 Justin Ehler  
Portico Media Server is free software licensed under the GNU General  
Public License, version 3 or, at your option, any later version.
