---
title: Activity and statistics
description: Collect who watched what by polling.
kind: tool
order: 13
---

Statistics and monitoring tools can read Portico’s sessions and catalogue to chart what’s being watched.

## Prepare Portico

Turn on **Integrations compatibility** in **Settings → Server → Integrations**. Create an API key just for this tool. Server-wide statistics need owner or administrator authority and the `admin` scope; a narrower key can’t read everyone’s sessions or counts.

## Set up the tool

1. In the tool’s setup, add a media server that takes a server address and an API key.
2. Enter Portico’s address and the key.
3. Turn off live (WebSocket) collection in the tool’s settings and use polling. Restart the tool if it asks.

Portico has no playback-reporting plugin, plugin database queries or WebSocket feed for these tools.

## Check collection

Start playing something and confirm the tool sees it. Statistics start when the tool is set up and can miss activity between polls. Past playback isn’t imported. Media details are available for reports; they aren’t playback links.
