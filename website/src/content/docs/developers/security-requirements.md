---
title: Client security requirements
description: Rules for credential storage, route verification, protected URLs and redacted diagnostics in Portico clients.
category: developers
order: 4
audience: Developers
updated: 2026-10-18
---

## Credential storage

Keep long-lived native credentials in the platform's secure storage. The web client uses an encrypted browser store for saved server credentials. Don't write tokens, cookies, recovery codes, signed route documents or private server addresses to general application logs.

Keep Portico Account credentials separate from server credentials. An installation ID isn't a secret or a device private key. Refresh tokens are secrets bound to that installation; serialize refreshes and save replacement access and refresh tokens atomically.

## Route verification

For Portico Account discovery, verify signed route and policy evidence using the trusted hosted signing material. Enforce the document's type, account audience, expected server identity, generations and expiry. Before sending server credentials, verify a fresh server-signed challenge bound to the selected origin and the expected identity.

A manually entered origin establishes an initial identity pin. Check the address carefully; a first connection can't independently establish that it is the server you intended. Treat a changed identity as a verification failure, not a reason to resend credentials.

## Transport

Owners may choose HTTP. It uses the same authorization checks as HTTPS but doesn't encrypt traffic. Use HTTPS on untrusted networks. Apple apps permit plain HTTP only for local-network addresses. A signed route or identity proof doesn't provide transport encryption.

## Protected resource URLs

Use the server's canonical resource URL helpers for media and protected binary resources. Don't scatter URL construction through feature code.

An API key or personal access token can travel in the `Authorization: Bearer` header, or as `apiKey` on `GET` and `HEAD`. A key-bearing URL has the power of that key; keep it out of shared screenshots, browser history exports and logs. Don't put a key in a public web page. Resource-specific media grants have their own limits and aren't interchangeable with a full API key.

The server permits bearer-authenticated API requests from other browser origins. CORS doesn't replace scope, role or library checks. API keys can't perform operations marked `x-scope: session`.

## Diagnostics

Keep request identifiers and structured problem details when available. Before export, redact credentials, account data, personal media names, paths and private network information. Review support archives before sharing; a redacted settings export doesn't make every attached log safe to publish.
