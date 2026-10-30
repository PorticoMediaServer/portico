---
title: Authentication architecture
description: Follow the trust boundary from a Portico Account to one authorized server session.
category: developers
order: 3
audience: Developers
updated: 2026-07-12
---

An app using hosted sign-in begins with a Portico Account session. That session can list the servers the account is allowed to know about and request short-lived signed route information.

The app verifies the route document and server identity before creating a server-scoped session. The server then maps the hosted identity into its current local authorization state.

Do not send the cloud session cookie or bearer material to an arbitrary server address. Production integrations should use the generated Portico client libraries for the exchange and route-verification sequence.

Handle account-expired, membership-removed, server-offline, route-invalid, and server-session-expired states separately. Repeating a request with the wrong credential is not recovery.
