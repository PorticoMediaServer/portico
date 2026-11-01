---
title: Enable remote access
description: Link your server to your Portico Account and turn on direct access once it works on your home network.
category: remote
order: 2
audience: Server owners
updated: 2026-10-18
---

Set up remote access after the server works at home. Check that a normal viewer can sign in and play something on the server's network.

## Connect your Portico Account

1. Sign in as the server owner.
2. Open **Settings → Server → Remote access**.
3. In **Secure connection**, select **Turn on secure connection**.
4. Approve the server in the Portico Account window, then return to the server.
5. Wait for the certificate to be issued. Keep **Use a secure connection** on.

Portico obtains and renews the certificate for you. You can also use your own certificate. See [Check router and firewall settings](/docs/remote/manual-networking/).

## Allow access from outside

1. On the same **Remote access** page, turn on **Remote access**.
2. Under **Access from outside**, set **Who may sign in from outside** to **Anyone with an account** for normal viewer access. The other choices are **Only the owner** and **Nobody**.
3. Open **Advanced**. Turn on **Automatic port mapping** if you want Portico to ask the router to open a port. The available protocols are **Use PCP**, **Use NAT-PMP**, and **Use UPnP**.
4. Check **Public port** and **Secure connection port**. The default is 32500; both must describe the external port clients will use.
5. Select **Save**.

These settings apply to people who already have access to your server. They don't make your libraries public.

## Check the result

Refresh the status at the top of **Remote access** to run the reachability check again. A working route shows **Reachable from outside at** followed by the address. If it shows **Not reachable from outside.**, see [Read the connection status](/docs/remote/read-connection-status/).

Before you invite remote viewers, check the server clock, certificate expiry, router mapping and firewall. Test playback from another network and protect the owner account with strong authentication.

Remote access is for Portico. Don't publish an administrative operating-system service or a broad network share as part of it.
