---
title: Portico security model
description: What Portico Server controls, what Portico Services control, and where access is checked.
category: security
order: 1
audience: Everyone
updated: 2026-10-18
---

Your Portico Server keeps your media and decides who can use it. Portico Services handle Portico Accounts and help an approved app find the right server. A signed route tells an app where to connect; it doesn't grant media access.

## What Portico Server controls

Portico Server manages media, libraries, playback, server accounts, permissions, settings, recordings, downloads and administration. Protected requests are checked against current account, profile and permission state. Public discovery and setup endpoints have their own access rules.

## What Portico Services control

Portico Services manage Portico Accounts, hosted security settings, server ownership and membership, invitations, and signed route and policy documents. The server maps an admitted Portico Account to its local authorization state and checks the requested action. Membership through a Portico Account alone isn't a playback permission.

## Keys and verification

The server's identity signing key and certificate private key stay on the server. Certificate requests send a certificate signing request, not the certificate private key. Route and policy signing keys belong to Portico Services. Portico Server verifies hosted signing certificates against Portico's trusted public signing root. The shared web and native connection code fetches the route-verification key from the configured Portico Account service over HTTPS and checks route signatures with that key.

For account-based discovery, the app checks the connection document's signature, intended account, server identity, version numbers (generations) and expiry. Before sending credentials to an address, it asks the server to sign a new challenge for that exact address and verifies the signature against the expected server identity. A manually entered server address establishes an initial identity that the app remembers; this isn't prior independent proof that you entered the intended server.

## HTTP and HTTPS

Owners can use plain HTTP or HTTPS. Both use the same authentication and permission checks, but plain HTTP doesn't encrypt credentials or media in transit. Use HTTPS on networks you don't trust. HTTP and HTTPS share the server's listening port, which defaults to TCP **32500**. HTTPS can use a Portico-managed certificate or the owner's certificate; a reverse proxy can also terminate HTTPS.

Portico Services publish public routes over HTTPS. Apple apps accept plain HTTP only for local-network addresses, including supported private and overlay addresses. They require HTTPS for other addresses. Signed identity checks don't turn plain HTTP into an encrypted connection.

## Sessions

The server issues short-lived bearer access tokens and rotating refresh tokens bound to an installation ID. Server access tokens last up to 15 minutes. The installation ID identifies an app installation; it isn't a device private key. Signing out ends the session family. Disabling an account, changing its password or blocking a device ends its sessions; permission changes apply to subsequent requests.

Portico Web keeps saved server credentials in an encrypted browser store. Server API authentication uses bearer headers, not cookies. Portico Account sessions and server sessions are separate; the app doesn't forward a Portico Account session token to an arbitrary server.

## API keys and personal access tokens

Owners create API keys; members can create their own personal access tokens when the owner allows it. Each credential acts as its creator under current permissions and its chosen scopes: `read`, `playback`, `library` or `admin`. It can perform most actions that person can perform with those scopes.

Send the credential in `Authorization: Bearer <secret>`. For `GET` and `HEAD`, `apiKey=<secret>` is also accepted in the query string. These credentials work from any browser origin. Cross-origin access doesn't bypass roles, library restrictions or scopes. URLs containing keys are secrets too.

Sign-in, password and two-step verification changes, credential management, ownership and server claiming require sessions. Other session-only operations, including profile selection, invitation acceptance and device pairing, are identified by `x-scope: session` in the API contract. Keys can't create other keys.

Keys and tokens have their own lifetime. Password changes and sign-out don't revoke them. Revoke them separately under **Settings → Server → Integrations**, or **Personal access tokens** under **Settings → Account → Profile & account**. After restoring a backup, review the credentials restored with the database.
