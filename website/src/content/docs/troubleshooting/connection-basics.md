---
title: Connection troubleshooting basics
description: Work out whether a connection problem is in the app, the account, the local network, the remote route or the server.
category: troubleshooting
order: 1
audience: Everyone
updated: 2026-10-18
---

## Start with the local network

Test the shortest path first.

1. Confirm that Portico Server is running.
2. On the server computer, open `http://127.0.0.1:32500/`.
3. From another device on the same network, open `http://<server-address>:32500/`, using the server's local address. Use your configured port if you changed it.

The bundled Portico Web and API share TCP port **32500** by default. HTTP and HTTPS use the same listener. Check the service, host firewall and Docker port mapping if neither address works. `127.0.0.1` always refers to the device opening the page.

## If local works but remote doesn't

Open **Settings → Server → Remote access**. Record the status message, **Portico Account** connection, published address, secure-connection status and last reachability check. Then read [Read the connection status](/docs/remote/read-connection-status/) and [Enable remote access](/docs/remote/enable-remote-access/).

Apple apps require HTTPS for addresses outside supported local networks. A remote HTTP address working in a browser doesn't establish that an Apple app can use it.

## If one account fails

If another account works, ask the owner to open **Settings → Server → People**. Check the affected person's status, role, profiles and assigned libraries, and whether the device is waiting for approval.

## If one device fails

If the same account works elsewhere, update the failing app, check the device clock, record the error and try signing in again. A browser loading an HTTPS page can also block requests to a plain HTTP server; test the server's bundled Portico Web directly.

## Collect details before you restart

1. Write down the exact error and the time it happened.
2. As an owner or administrator, open **Settings → Server → Troubleshooting** and inspect **Logs** around that time. Owners can also inspect **Server records**.
3. Note the server version under **Settings → Server → General** and the affected app version.

## Find the log files

The active file is `logs/messages.log` inside the server data folder. Rotated files use names such as `messages.1.log`.

| Installation | Default active log |
| --- | --- |
| Linux package | `/var/lib/portico-media-server/logs/messages.log` |
| Docker | `/var/lib/portico/logs/messages.log` inside the container's persistent state volume |
| macOS package | `/Library/Application Support/Portico Media Server/logs/messages.log` |
| Windows service | `%ProgramData%\Portico Media Server\logs\messages.log` |
| Portable package | `state/logs/messages.log` beside the launcher |

`PORTICO_STATE_DIR` changes the data folder. For service startup failures on Linux, run `journalctl -u portico-media-server -n 100`. For a container named `portico`, run `docker logs --tail 100 portico`.

Logs can contain private paths and media details. Remove them and any credentials before sharing excerpts.

## Where to go next

- Local works, remote fails: go to [Enable remote access](/docs/remote/enable-remote-access/).
- One device fails: read [Why controls differ between apps](/docs/clients/client-differences/).
- Sign-in fails everywhere: go to [Sign-in problems](/docs/troubleshooting/sign-in-problems/).
