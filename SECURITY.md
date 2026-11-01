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

- **Remote access** is On, Private only or Off. Tailscale's ranges (100.64.0.0/10, fd7a:115c:a1e0::/48) count as home under On and Private only, since nothing from the internet comes from them. Private only means your home network and your tailnet, nothing from the internet; Off means your home network only. (100.64.0.0/10 is also the carrier-grade NAT range; traffic from the internet can't arrive from it.) This is only about which connections are answered: sign-in rules and the remote bitrate limit still treat a tailnet client as away from home. Off and Private only also stop port mapping, and Off removes the Portico address.
- **Remote security** (while Remote access is On):
  - **Enhanced** (the default): from outside, Portico answers only HTTPS, and only at its Portico addresses and the custom addresses you add. Anything else is refused before Portico says anything: a TLS handshake for another name, or with no name at all (connecting by IP), fails with `unrecognized_name`, and plain HTTP is closed without a byte. Enhanced lists recently refused names that point at your server, so you can allow one.
  - **Standard**: from outside, Portico answers at any address, with or without TLS.
- At home, the "Secure connections" setting decides whether plain HTTP is allowed (Preferred, the default, allows it; Required refuses it). Routes advertised through Portico Services always use TLS.
- A custom address works over HTTPS only through your own certificate or a trusted reverse proxy. Behind a trusted proxy, Enhanced needs the forwarded host to be a custom address, and refuses only a proxy that says the request came over plain HTTP (`X-Forwarded-Proto: http`).
- For which connections are answered, loopback and every private and link-local range (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, fc00::/7, fe80::/10) always count as home, on top of the networks Portico detects and the ones you add. None of them can arrive from the internet, so scanners are refused as before. In Docker's bridge mode, where the container sees only its bridge network, devices on your LAN still count as home. First-run setup uses the same rule. Sign-in rules, the remote bitrate limit and the sign-in block exemption still use only your detected and configured home networks.
- Beyond that, you decide which networks count as your home network. Apart from Tailscale (above), Portico doesn't assume a network is trusted because it's behind a proxy or behind carrier-grade NAT.
- Forwarded headers are only trusted from proxies you've configured. See [INSTALLING.md](INSTALLING.md).

### What anyone can see before signing in

Before sign-in, Portico answers only what a client needs to find the server and sign in: its name and ID, how people sign in, whether setup is done, the API version, and facts about the connection itself (secure or not, home or away). It doesn't say which version of Portico it runs, which operating system it's on, where its files are, its home networks, its addresses or yours. A test pins the exact answer of every route that works without signing in.

Once signed in, anyone sees the server version (in About), and apps learn the owner's home networks, so a VPN or Tailscale range you added counts as home.

Integration tools that use the Jellyfin/Emby-compatible API are told a fixed version, 10.10.0, never the real one.

### Common setups (Remote access On)

| Setup | Enhanced | Standard |
| --- | --- | --- |
| Portico apps, at the server's Portico address | Works | Works |
| Portico Services checking that your server is reachable | Works | Works |
| A device at home, by IP over HTTP | Works | Works |
| Docker in bridge mode, a device on your LAN by IP over HTTP | Works (private ranges always count as home) | Works |
| A device at home, by IP over HTTPS | Fails: no certificate can name an IP. Use the Portico address, or HTTP at home | Same |
| Tailscale, by name or IP (Remote access On or Private only) | Works (the tailnet counts as home) | Works |
| cloudflared or another tunnel on the same machine | Works. The tunnel connects from the machine itself, so Portico sees it as home: sign-ins through it are never blocked by network, though the per-account limits still apply | Works |
| Caddy, Traefik or nginx at home, as a trusted proxy, for a custom address | Works, with or without X-Forwarded-Proto. Only an explicit `http` is refused | Works |
| A trusted proxy that sends no X-Forwarded-For | Works for a custom address (judged as from outside) | Works |
| A trusted proxy that replaces the Host with its own upstream address | Refused, and the proxy shows under Refused addresses. Have it pass Host or X-Forwarded-Host, or use Standard | Works |
| A trusted proxy forwarding a name you haven't added | Refused, and shown under Refused addresses: **Allow this address** fixes it in one tap | Works |
| Your own domain with your own certificate | Works | Works |
| A DDNS name with no certificate, over HTTP | Refused, and shown under Refused addresses: **Standard** fixes it in one tap (or add a certificate or proxy) | Works |
| A DDNS name with no certificate, over HTTPS | Fails the TLS handshake in both modes. Shown under Refused addresses in Enhanced; it needs a certificate or a proxy | Fails |
| Anyone from outside by raw IP | Refused, and not listed (scanners do this all day) | Works over HTTP; HTTPS by IP fails |

Refused addresses only lists names that point at your server, so scanner noise stays out.

### First-run setup

Anyone who can reach a server that hasn't been set up yet can complete setup. There's no separate setup code. Browser setup only accepts same-origin requests, so another website can't do it for you. **Finish setup before you make the server reachable.**

### Passwords, PINs and sign-in

- Local passwords need at least eight characters, up to bcrypt's 72-byte limit. There are no complexity rules and no permanent lockout, but failed attempts are rate limited and slowed down.
- A network that fails sign-in across many accounts is blocked for a while. After 30 failed sign-ins from one network (one IPv4 address, or one IPv6 /64), its sign-in attempts are refused for 15 minutes. Each new block doubles that, up to a day. The threshold follows the password-attempts setting: twice as many with the relaxed setting, half as many with the strict one. Failures fade by half every 15 minutes.
- Your home network is never blocked this way, and neither is a connection Portico can't place. A tailnet client counts as away here, as it does for the other sign-in rules, unless you add your tailnet to your home networks. Behind a trusted proxy, Portico counts the client address the proxy forwards, never the proxy's own, so one person guessing through your proxy doesn't lock out everyone else using it. The per-account limits above apply everywhere, home included.
- A blocked network is told how long to wait. The owner sees the block as a range (a /24 or /48), never the exact address, and can lift it early.
- The owner is alerted when a network is blocked (once per network per day), and when failed sign-ins spike across the server (50 within ten minutes, at most once an hour). Every sign-in from a new device sends a notice, whichever way the person signed in.
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
