---
title: Client security requirements
description: Preserve credential storage, direct-route verification, server authorization, and redacted diagnostics.
category: developers
order: 4
audience: Developers
updated: 2026-07-12
---

Store long-lived credentials only in the secure storage appropriate to the platform. Do not write tokens, cookies, recovery codes, route documents, or private server addresses to general application logs.

Verify signed route and policy documents with the published key set and enforce their audience, server identity, scope, and expiry. Treat a reachable URL as untrusted until verification succeeds.

Use the server's canonical resource URL helpers for media and other protected binary resources. Do not build sensitive URLs by string concatenation in feature code.

Preserve request identifiers and structured problem details in diagnostics, but redact credentials, account data, personal media names, paths, and private network information before export.
