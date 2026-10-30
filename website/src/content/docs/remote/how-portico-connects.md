---
title: How Portico connects
description: See how an authorized app finds a server without sending private media through Portico Cloud.
category: remote
order: 1
audience: Everyone
updated: 2026-07-12
---

Portico uses two authorities. Portico Cloud knows about Portico Accounts, server membership, and signed connection information. Your Portico Server knows about its media, local permissions, playback, and server settings.

When an authorized app selects a server, it receives a short-lived signed route document. The app checks the route and server identity before establishing the server-scoped session.

After that connection succeeds, media and server-specific API requests travel directly between the app and the Portico Server. Portico Cloud does not relay, tunnel, cache, or inspect private media.

The server remains the final authorization point even when a request began with a Portico Account. A cloud membership cannot grant access to a library or action that the server does not currently permit.
