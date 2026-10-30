---
title: Enable remote access
description: Publish a verified route only after the server works correctly on the local network.
category: remote
order: 2
audience: Server owners
updated: 2026-07-12
---

Finish local setup first. At least one normal viewer should be able to sign in and play an item from the same network as the server.

## Link the server

1. Open **Settings → Connectivity**.
2. Under **Portico claim**, select **Start claim** if the server is marked **Local only**.
3. Select **Continue at Portico** and approve the claim with the Portico Account that owns the server.
4. Return to Connectivity. The route summary should now show **Claimed**.

## Turn on direct access

1. Under **Direct access**, switch on **Enable remote access**.
2. Set **Remote sign-in** to **Portico account** for normal remote use.
3. Leave **Public port** set to **Automatic** and **Router automation** on unless your network requires a manual port forward.
4. Save the changes.

The route summary should read **Direct remote access is available** and show the public HTTPS address. If it says **Direct route is not currently reachable**, open [Read connection status](/docs/remote/read-connection-status/) before changing other settings.

## Before inviting remote viewers

- Confirm the server clock is correct.
- Confirm **Certificate** reports a valid managed or owner-managed certificate.
- Confirm the router and firewall allow the selected public port.
- Test from a network that is not your home Wi-Fi.
- Keep the owner account protected with strong authentication.

Do not publish an administrative operating-system service or broad network share as part of Portico remote access.
