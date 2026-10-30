# Portico API developer guide

Use the server's own origin, such as `http://127.0.0.1:32500` or
`https://media.example.com`. Hosted is not needed for local API integrations.
The complete [generated registry contract](../portico.openapi.json) describes
production routes, including retained HTTP handlers and raw streams. It is the
sole API specification; the old YAML files are removed.
Examples below are illustrative requests, with IDs and secrets to replace.

1. [Conventions](01-overview.md): time, paging, errors, revisions and limits.
2. [Authentication](02-authentication.md): sessions, direct sign-in, refresh and TV codes.
3. [Browsing](03-browsing.md): libraries, content, search and title details.
4. [Playback](04-playback-v1.md): capabilities, sessions, queues and subtitles.
5. [Personal state and jobs](05-personal-state-and-jobs.md).
6. [Events](06-events.md): changes while a screen is open.
7. [Keys, tokens and scopes](07-keys-tokens-and-scopes.md).
8. [Tools](08-for-tools.md): title query and Sonarr/Radarr changed-path reports.
9. [Webhooks](09-webhooks.md): events, signatures and delivery.
10. [MCP](10-mcp.md): connect a desktop assistant and discover its tools.

For owner setup and media organisation, see [installation](../../../INSTALLING.md)
and [libraries](../../../docs/libraries.md). Topic references in the API folder
cover metadata editing, downloads, administration and social playback.

Owner playback diagnostics include `GET /v1/admin/playback/route-failures`
(the latest 100 active remembered failures, with expiry and no credentials).
`DELETE /v1/admin/playback/route-failures/{sourceId}` clears all remembered
routes for that file so devices can retry it after a player or driver change.
Both routes require an owner and the admin key scope.

For anime files numbered across seasons, show identification accepts the
provider's official order or a show-owned TMDB episode group. An explicit
`order` such as `tmdb-absolute:official:-12` maps local episode 15 to position 3;
`tmdb-absolute:tmdb-group:<groupId>:-12` applies the same offset to the selected
group. The offset is added to the local number. Local numbers remain unchanged,
and original seasonal coordinates are retained as provenance. Client-core's
`tmdbAbsoluteOrder` constructs this bounded order value. Seasonal files in the
same show use the selected base order without the absolute offset.

Regenerate the checked-in registry contract when source-registered routes change:
run `GOFLAGS=-p=2 go run ./cmd/apigen` from `server/`. This updates the shared
TypeScript contracts and admission table as well. Validate guide examples with
`node server/api/guide/check-examples.mjs`.
