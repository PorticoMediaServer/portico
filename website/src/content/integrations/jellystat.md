---
title: Jellystat
description: Collect activity through REST polling.
kind: tool
order: 16
---

## Prepare Portico

Enable **Integrations compatibility** in **Settings → Server → Integrations**. Create a dedicated scoped API key. Server-wide statistics require owner or administrator authority and `admin` scope; a limited credential cannot read global sessions or counts.

## Configure Jellystat

1. Choose the server type labeled **Jellyfin** in Jellystat’s setup.
2. Enter Portico’s URL and the dedicated API key.
3. Disable WebSocket collection in Jellystat’s environment and restart the tool:

```sh
JF_USE_WEBSOCKETS=false
```

Use REST polling. Portico does not provide a Playback Reporting plugin, plugin SQL endpoint or WebSocket protocol for this tool.

## Check collection

Start a playback session and confirm that polling observes it. Statistics begin when Jellystat is configured and may miss activity between polls. This connection does not import a complete historical playback ledger. Stored media facts are available for reporting; they are not playback URLs.
