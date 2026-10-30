# 10. MCP: connecting an AI assistant

Portico has a Model Context Protocol server built in. An assistant that speaks
MCP can list libraries, search, start scans, see what is playing, fix a match,
read settings and so on, as the member whose key it was given.

| | |
|---|---|
| Address | `https://<your server>/v1/mcp` (`POST /v1/mcp (portico.openapi.json, operationId mcp)`) |
| Transport | Streamable HTTP. Each request is one JSON-RPC 2.0 message; each answer is one JSON document. |
| Authentication | `Authorization: Bearer <API key or personal access token>` |
| Protocol versions | `2025-06-18`, `2025-03-26`, `2024-11-05` |
| Methods | `initialize`, `ping`, `tools/list`, `tools/call`, and `notifications/*` (accepted, `202`) |

The server opens no stream of its own, so `GET /v1/mcp (portico.openapi.json,
operationId mcp_stream)` answers `405`, as the transport allows. There is no
session id to keep: every request stands alone.

## Setting it up

1. Make an API key (owner) or a personal access token (any member) with the
   scopes the assistant should have (`07-keys-tokens-and-scopes.md`). `read` is
   enough to look things up; add `playback`, `library` or `admin` only for what
   you want it to be able to change.
2. Give the assistant the address and the key. A desktop client that supports remote Streamable HTTP needs a URL and a
   bearer header. Client configuration syntax differs; this is a generic shape,
   not a promise that every desktop application accepts it:

```json
{
  "mcpServers": {
    "portico": {
      "type": "http",
      "url": "https://media.example.com/v1/mcp",
      "headers": {"Authorization": "Bearer pk.3q2-7wExampleExampleExampleExampleExample0"}
    }
  }
}
```

A signed-in app session is not accepted here: use a key or token, so that what
the assistant may do is something you chose.

After adding the remote server in your desktop assistant, let it initialize
and list tools before asking it to act. Keep the credential in that application's
private secret settings. An assistant on another machine needs a route that
reaches this origin; `127.0.0.1` always means that assistant's own machine.
A client supporting only local stdio needs a remote-HTTP bridge of its own;
Portico does not install a desktop bridge.

You can check the protocol with `curl` (replace `$KEY` with the scoped credential):

```bash
curl -fsS "$BASE/v1/mcp" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"Desktop assistant","version":"1.0"}}}'
curl -fsS "$BASE/v1/mcp" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

## What the assistant can do

`tools/list` returns only the tools the key's scopes **and** its member's role
can use; a tool outside them does not exist for that key. A tool is a
description of one or two calls to the API in this guide, made by the server to
itself with the caller's own credential. So a tool can never do what the API
would refuse: roles, content restrictions, scopes, validation and limits all
apply exactly as they do to a direct request.

| Tool | Scope | Who | What it does |
|---|---|---|---|
| `list_libraries`, `search`, `list_titles`, `find_title_by_provider_id`, `get_title`, `list_seasons`, `list_episodes` | `read` | anyone | Look at what is in the libraries. |
| `library_scan_status`, `list_notifications`, `tv_guide`, `list_recordings` | `read` | anyone | Scan progress, notifications, the guide, recordings. |
| `server_status` | none | anyone | Is the server up, its name and version. |
| `mark_watched`, `control_playback`, `schedule_recording`, `cancel_recording` | `playback` (and `read` where it looks something up first) | anyone | The member's own watch state, playback and recordings. |
| `now_playing`, `stop_playback` | `playback` | administrator | What everyone is playing; stop a session. |
| `start_library_scan`, `report_changed_paths` | `library` | owner | Scan a library, or only the folders that changed. |
| `list_scan_jobs`, `list_unmatched_files`, `list_needs_attention`, `library_needs_attention`, `count_needs_attention`, `get_match`, `search_match_candidates`, `fix_match` | `library` | owner | Scan history, files that could not be matched, fixing a match. |
| `list_members`, `play_history`, `read_logs` | `admin` | administrator | People, what was played, the server log. |
| `server_health`, `storage_usage`, `get_settings`, `change_settings`, `list_backups`, `run_backup` | `admin` | owner | Health and free space, settings, backups. |
| `list_webhooks`, `create_webhook`, `update_webhook`, `delete_webhook`, `test_webhook`, `list_webhook_deliveries` | `admin` | owner | Webhooks. |

A tool that is not set up on a server (Live TV with no tuner source, for
example) is not listed there.

## Calling a tool

```json
{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": {"name": "list_titles", "arguments": {"library": "movies", "kind": "movie", "sort": "added", "order": "desc", "limit": 10}}}
```

The result has the API's answer as text and, when it is a JSON object, as
`structuredContent`. When the API refuses the call, `isError` is `true` and the
text is the API's own message. Arguments are held to each tool's `inputSchema`:
an unknown argument, a wrong type, or an id that is not a plain identifier is a
JSON-RPC `-32602` error.

## Limits

An MCP request counts as one request against the key's rate and its four
requests in flight (`07-keys-tokens-and-scopes.md`); the API calls a tool makes
inside it are not counted again. A body is at most 1 MiB, a batch at most 16
messages, and a tool's answer at most 256 KiB (ask for a smaller page if a tool
says its answer is too large).
