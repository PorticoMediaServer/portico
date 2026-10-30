---
title: Check router and firewall settings
description: Review the local network when automatic remote access cannot reach the server.
category: remote
order: 4
audience: Server owners
updated: 2026-07-12
---

Start by giving the server a stable address on the home network. A router reservation is usually safer than entering a static address that may conflict with the router's own address pool.

Allow the Portico listener through the server operating system's firewall for the intended networks. Keep the rule limited to Portico rather than disabling the firewall.

If automatic router configuration is not available, create the narrow port mapping shown by Portico's Remote access settings. Forward it to the current local address of the Portico Server, then return to Portico and run the public reachability check again.

Carrier-grade NAT, multiple routers, VPN software, and security products can change the public path. If the router's external address is private or differs from the address visible on the internet, contact the internet provider or use a supported alternative connection arrangement.
