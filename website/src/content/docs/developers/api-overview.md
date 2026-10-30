---
title: API architecture overview
description: Understand how Portico separates Server and Cloud contracts before integrating with either one.
category: developers
order: 1
audience: Developers
updated: 2026-07-12
---

Portico maintains separate OpenAPI contracts for Portico Server and Portico Cloud. This overview explains the boundary an integration must preserve.

Use the **server contract** for media, libraries, search, playback, downloads, Live TV, DVR, user permissions, settings, diagnostics, and other server-owned work.

Use the **cloud contract** for Portico Accounts, hosted sessions and security, claimed servers, invitations, membership, route discovery, and signed policy coordination.

An integration must not turn a cloud membership into assumed media access. After selecting and verifying a server route, it creates a server-scoped session and lets Portico Server authorize each request.

The generated Portico client libraries own URL construction, credentials, retries, and typed request and response models. External integrations should not rely on private endpoint paths copied from browser traffic.
