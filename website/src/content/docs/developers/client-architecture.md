---
title: Client architecture
description: Share product behaviour while keeping platform navigation, secure storage, and playback native to each client.
category: developers
order: 2
audience: Developers
updated: 2026-07-12
---

Portico clients consume the same OpenAPI and Product Contract but do not need to share one universal user-interface runtime.

Shared client behaviour includes authentication flows, server discovery, route verification, request and error handling, canonical library vocabulary, playback intent, search scope, and compatible view models.

Each platform owns its application shell, navigation, accessibility integration, secure credential storage, lifecycle, and media player. A television requires deterministic remote focus; a phone requires touch and mobile lifecycle handling; a browser works within web media and security constraints.

Keep transport objects at the adapter boundary. Product screens should consume stable view models rather than copying wire fields and endpoint paths throughout the interface.
