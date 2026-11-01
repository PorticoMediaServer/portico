---
title: API architecture overview
description: How Portico splits the Server API and the Portico Services API, and which one to use for what.
category: developers
order: 1
audience: Developers
updated: 2026-10-18
---

Portico Server and Portico Services have separate APIs. Use the Server API for integrations that work with a person's media server. Portico Account sign-in and connection coordination use the Portico Services API.

Start with [Integrations](/developers/api/) for task guides, tool setup and the native Integrations reference. The full generated reference is under [Building a Portico app](/developers/api/clients/).

The OpenAPI tags are documentation buckets, not permissions. Integrations contracts preserve methods, paths, field meanings, IDs, paging, errors and scopes within `/v1`; breaking changes require a new major version and a documented transition. Ignore unknown optional fields. Portico is pre-release: this stability promise starts at the first stable release. Clients may negotiate presentation versions and add capabilities more frequently.

## Server address and contract

The API base path is `/v1` on the same origin and port as Portico Web bundled with the server. The default port is TCP **32500**. For example, a local server's library list is `http://127.0.0.1:32500/v1/libraries`. Give a reverse-proxied server its own hostname; hosting below a path such as `/portico` isn't supported.

`GET /v1/system` is public and reports the server's ID, name, version and setup state. Protected operations require a server session, API key or personal access token, as described in [Authentication architecture](/docs/developers/authentication/).

The server repository's `server/api/portico.openapi.json` is generated from the production route registry. Its `server/api/guide/` directory documents authentication, media, playback, keys, webhooks and MCP. Read each operation's models, `x-scope`, limits and preconditions instead of inferring routes from browser traffic.

Use this contract for libraries, titles, search, playback, downloads, Live TV, DVR, permissions, settings and diagnostics.

## Portico Services API

Portico Services manage Portico Accounts, hosted sessions and security, claimed servers, invitations, membership, route discovery and signed policy coordination. The Portico Services API contract is separate from the Server API. A server integration using a locally issued key doesn't need a Portico Account or an app registered with Portico Services.

Don't treat membership through a Portico Account as media permission. An account-based app verifies a server route and obtains that server's own session. Portico Server then authorizes each request.

## Requests, paging and retries

JSON requests use `Content-Type: application/json`. Errors include an `error` object with a machine-readable `code` and a readable `message`; additional retry and field details depend on the operation.

Cursor pages return `nextCursor`. Send it back unchanged with the same query and identity; an absent or empty cursor ends the list. A short page alone doesn't mean the list is finished.

Writes can require a body `expectedRevision` or an `If-Match` header. Playback starts require `Idempotency-Key`. Follow the operation's contract and reread current state after a revision conflict. Honour `Retry-After` on rate or capacity limits.

## Webhooks

Owners manage webhooks under **Settings → Server → Integrations → Webhooks**. API management uses `/v1/admin/webhooks`; an owner's key with `admin` scope can manage them too. The local server sends event payloads directly to the configured HTTP or HTTPS destination. Events can include titles and playback activity, so choose the destination carefully.

Verify the `Portico-Signature` header using HMAC-SHA256 over `<timestamp>.<raw request body>` with the webhook secret. Reject stale timestamps and deduplicate `Portico-Event-Id`. The server retries failed deliveries; inspect the webhook's **Delivery log**.

## Assistants through MCP

Portico Server includes a Model Context Protocol endpoint at `POST /v1/mcp`. It uses Streamable HTTP with JSON-RPC requests and JSON responses. Give the assistant an API key or personal access token in the bearer header; a signed-in app session isn't accepted here.

Tools are filtered by the credential's scopes and member role. They use the same server permissions as direct API calls. **Settings → Server → Integrations → Assistants** contains **Assistants (MCP)** and shows the endpoint address. An assistant on another computer needs an address it can reach; its `127.0.0.1` isn't your media server.

## Shared client code

Portico's shared client packages contain generated route helpers, transport code and typed models alongside hand-written services for authentication, retries and playback. Use those layers when working on a Portico client. External integrations can use the published Server API contract directly.
