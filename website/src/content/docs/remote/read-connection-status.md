---
title: Read the connection status
description: Use Portico's local, public, certificate, and verification results to find the failed layer.
category: remote
order: 3
audience: Server owners
updated: 2026-07-12
---

Remote access depends on several checks. A single green or red label cannot explain all of them, so Portico keeps the layers separate.

**Local connection** confirms that clients on the same network can reach the server.

**Published route** confirms that the server has current signed connection information associated with the correct server identity.

**Public reachability** confirms that the route can be reached from outside the local network.

**Certificate** confirms that the public name and encryption state are valid for a browser or client connection.

Work from the first failed layer. Changing certificate settings will not repair an offline server, and repeatedly publishing a route will not open a blocked firewall.
