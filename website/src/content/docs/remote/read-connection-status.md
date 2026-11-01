---
title: Read the connection status
description: Use the local, route, public reachability and certificate checks to find which part of remote access failed.
category: remote
order: 3
audience: Server owners
updated: 2026-10-18
---

Open **Settings → Server → Remote access** as the owner. Check reachability and encryption separately: a valid certificate doesn't open a blocked port.

## Check reachability

The status at the top says whether remote access is off, being checked, reachable at an address, or **Not reachable from outside.** Refresh it to run another check. Read any warning underneath before changing settings.

Under **Home network**, **This server answers on** lists the server's addresses. **Router mappings** shows the router protocols, mapping state and external ports when mappings exist. **This device** shows whether your current connection counts as home or away according to **Home networks**.

## Check the secure connection

Under **Secure connection**, read the status and check **Use a secure connection**. The **Certificate** details include its name, issuer, expiry, renewal time and secure address when available. A failed certificate request shows a problem and **Try again**.

If you supply your own certificate, check the separate status under **Your own certificate**. Confirm that the domain and file paths are correct and readable by the server.

## Check the route clients use

A Portico Account connection uses current signed route information and verifies the server's identity. Successful account sign-in doesn't prove that the server is reachable. A direct connection also needs a reachable address and a successful identity check.

Fix local reachability first, then the public route and firewall, then any certificate problem. Repeatedly refreshing a route won't open a blocked firewall. For network checks, see [Check router and firewall settings](/docs/remote/manual-networking/).
