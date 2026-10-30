---
title: Data and retention
description: Understand which system stores account, server, route, audit, and media information.
category: security
order: 4
audience: Everyone
updated: 2026-07-12
---

Portico Server stores the durable information needed to run a personal media server: local media records, account mappings and permissions, settings, playback state, recordings, and operational data.

Portico Cloud stores Portico Account and hosted security information, server ownership and membership, invitations, and the bounded operational information required to coordinate signed routes and protect the service.

Connection observations and audit events are retained according to explicit hosted and server policies rather than as unlimited history. Expired setup, login, and transient route state is removed when it is no longer required.

Private media files and server certificate private keys do not move to Portico Cloud.
