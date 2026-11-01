---
title: Home Assistant
description: Monitor playback and control permitted sessions.
kind: tool
order: 15
---

## Prepare an app password

Enable **Integrations compatibility** in **Settings → Server → Integrations**. Create a named app password for the account and profile Home Assistant should use, with `read` and `playback` scopes. Server-wide monitoring or administration requires native administrator authority and appropriate scope, such as `admin`.

## Configure Home Assistant

1. Add the integration labeled **Jellyfin** in Home Assistant.
2. Enter Portico’s server URL, the bound username and the dedicated app password.
3. Finish setup and check which sessions the account can see.

Use the URL reachable from Home Assistant, not its own loopback address. Use a dedicated app password rather than an actual account password or PIN.

## Supported controls

Pause, resume, seek and stop translate to native session controls with their revision checks. The bound account/profile must have permission for the session. Unsupported commands return an error.

Volume, mute, starting playback on another player and media-source streaming are outside this connection’s supported scope. Test one permitted session’s pause and resume before attaching an automation.
