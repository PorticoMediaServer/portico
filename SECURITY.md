# Security

## Reporting a problem

If you find a security problem, please report it privately, not in a public issue:

- [Report a vulnerability on GitHub](https://github.com/PorticoMediaServer/portico/security/advisories/new), or
- email [admin@getportico.tv](mailto:admin@getportico.tv).

Include your Portico version, operating system, how you installed it, the steps to reproduce it and what an attacker could do with it. Strip passwords, personal details, media file names and private addresses from any logs.

While I look into a report, please don't publish the details, and don't test against systems that aren't yours. I'll respond when I can, but there's no guaranteed response time or fix date (see [SUPPORT.md](SUPPORT.md)).

Portico is prerelease. Security fixes normally go into the newest build only, so stay current.

## Things worth knowing before you run Portico

This section is about how Portico behaves by design, so you can set it up safely. Some of it is a choice you make as the owner.

### Network access

- Portico accepts plain HTTP from any address. Use TLS on any network you don't trust. Routes advertised through Portico Services always use TLS.
- You decide which networks count as your home network. Portico doesn't assume a network is trusted because it's Tailscale, behind a proxy or behind carrier-grade NAT.
- Forwarded headers are only trusted from proxies you've configured. See [INSTALLING.md](INSTALLING.md).

### First-run setup

Anyone who can reach a server that hasn't been set up yet can complete setup. There's no separate setup code. Browser setup only accepts same-origin requests, so another website can't do it for you. **Finish setup before you make the server reachable.**

### Passwords, PINs and sign-in

- Local passwords need at least eight characters, up to bcrypt's 72-byte limit. There are no complexity rules and no permanent lockout, but failed attempts are rate limited and slowed down.
- Profile PINs have their own temporary attempt limits. A PIN doesn't replace an account password.
- Local two-factor sign-in is available.
- Approving a TV sign-in code signs that TV in as you, with your role. If an owner approves their own code, that TV becomes an owner device. The device chooses its own name, so check the code on the TV you actually mean to approve.
- Cast devices only get playback access.

### Tokens and keys

Access tokens work like passwords: anyone who has a valid one can do whatever it allows. Refresh is tied to a device record and installation ID, but individual requests don't prove the request came from that device. The server checks revocation and current permissions.

### What's stored, and how

- The database, settings and backups aren't encrypted at rest.
- Passwords are stored as bcrypt hashes, and API key and personal token secrets as digests.
- Two-factor seeds, the server's identity and TLS keys, webhook secrets, mount credentials and some other recoverable credentials are stored unencrypted.

Protect the state directory and your backups with file permissions, and use disk encryption if you want it.

### Backups and restores

- Backups leave out live sign-in sessions and pending sign-in secrets, so restoring a backup signs everyone out.
- Passwords, two-factor seeds, approved devices, API keys and tokens go back to how they were when the backup was taken. Keys and tokens survive a restore on purpose, so a key you revoked after the backup will work again. Check and revoke anything you don't want after a restore.
- Certificate files stored outside Portico, and your media, need their own backups.

### Privacy between people on a server

- The owner can always see what was played, by whom and on which device. Pausing or clearing your own history doesn't hide it from the owner.
- If a member is deleted, their plays stay but their name is removed.
- DVR recordings are private to each profile. When several people record the same programme, they may share one stored file.

### Webhooks and AI assistants

- Webhooks send the events the owner picks straight to the HTTP or HTTPS address they choose, including private addresses. They can include titles, names, devices and sign-in addresses.
- Webhooks are HMAC-signed, redirects are refused, and certificate checks and size and queue limits still apply.
- An AI assistant connected over MCP acts as the member whose key or token it uses, limited by that credential's scopes and the member's current role.

### Media files

Portico limits how much size, depth, time and processing it spends reading media files. On Linux, an optional sandbox isolates the decoder where the host or container supports it. If it isn't available, playback continues with basic process restrictions. Set `PORTICO_DECODER_SANDBOX=required` to insist on the sandbox, or `off` to disable it. None of this makes hostile media risk-free.

### Release signing

Release manifests and artifact inventories are signed. That's separate from operating-system signing, notarization and installer trust, and preview packages may be unsigned. See [RELEASING.md](RELEASING.md) and [release signing](server/api/release-signing.md).
