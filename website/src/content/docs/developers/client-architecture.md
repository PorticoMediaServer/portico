---
title: Client architecture
description: What Portico clients share, and what each platform handles natively.
category: developers
order: 2
audience: Developers
updated: 2026-10-18
---

Portico's web and React Native clients share TypeScript packages for API access and product behaviour. Each platform supplies its own app shell and media integration.

## Shared packages

The server repository owns the canonical packages:

- `packages/contracts` defines route helpers and API types.
- `packages/client-core` contains connection verification, identity and session services, preferences, library and search models, and playback services.
- `packages/i18n` contains the English message catalogue and localized strings.

The native repository copies these shared packages through `scripts/shared-packages.mjs`. Change shared behaviour at its canonical source rather than patch a copied package independently.

Generated API models and route helpers are part of this structure. Authentication, retry handling and product services also include hand-written code; an OpenAPI-generated transport alone doesn't implement the full app.

## Shared behaviour

Web and native clients use shared services for authentication, server discovery, route verification, request and error handling, library vocabulary, playback intent, search scope and view models. The shared settings structure supplies page names and available controls; capabilities, permissions and platform support determine what appears.

Portico Web uses React with browser navigation and browser media constraints. The native apps use React Native alongside platform code for playback, secure storage and operating-system integration. A television also needs predictable remote focus; a phone needs touch navigation and mobile lifecycle handling.

## Identity and playback

Keep Portico Account sessions separate from server sessions. Verify a server connection before sending its credentials. Server account sign-in and scoped integration credentials also work without a Portico Account.

Playback starts, queues and session controls use the server's `/v1` API. Playback services negotiate delivery with the server; a platform player handles the chosen media. Don't assume that sharing an API makes every device support the same codecs, subtitles or offline behaviour.

## Keep transport objects at the adapter

Screens should use stable view models. Keep wire models, protected resource URLs and endpoint construction in transport and service adapters. Preserve opaque IDs and revisions exactly as the API returns them.
