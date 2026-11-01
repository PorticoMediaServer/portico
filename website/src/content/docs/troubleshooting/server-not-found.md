---
title: Server not found
description: Get a missing or offline server back in a Portico app.
category: troubleshooting
order: 2
audience: Everyone
updated: 2026-10-18
---

## Check the server

1. Confirm that the server computer is on.
2. Confirm that Portico Server is running and its media storage is available.
3. On the server, open `http://127.0.0.1:32500/`. From another device, use the server’s local address and TCP port **32500**, or your configured port. Check the service and host firewall if neither works.

## Check the account

If the server works locally, check that the affected account still belongs to it. Confirm the account and its server membership before you sign out and back in. Replacing a valid session can hide the original error.

## Check remote access

Open **Settings → Server → Remote access**. For remote access, check the published address, public reachability, **Portico Account** connection and secure-connection status. A VPN, a changed router address, a firewall update or expired connection information can break only the remote path.

## If the server was restored or moved

Confirm that the original server data and identity were moved or restored. A new installation with an empty data folder has a different identity. Don't bypass an identity-mismatch warning or repeatedly resend credentials; check the address and restored data first. For logs, see [Connection troubleshooting basics](/docs/troubleshooting/connection-basics/).
