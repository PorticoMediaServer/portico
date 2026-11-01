---
title: Radarr
description: Notify Portico when a movie import changes a file.
kind: tool
order: 11
---

## Prepare Portico

Enable **Integrations compatibility** in **Settings → Server → Integrations**. Create a dedicated Radarr API key with `read` and `library` scopes and access to the relevant libraries.

## Configure Radarr

1. In Radarr’s **Settings → Connect**, add its **Emby / Jellyfin** connection.
2. Enter Portico’s host, port (normally `32500`) and API key. Match the HTTPS setting to your server address. Use the server root; leave any optional URL base empty.
3. Match Radarr’s reported paths to the paths visible to Portico, including container mounts and path mappings.
4. Enable the relevant import and upgrade notifications, run **Test**, then save.

Optional administrator notifications require owner authority and `admin` scope. Leave that option off when the key lacks them.

## Check a real import

Import one movie and check Portico’s scan jobs. The update must name a specific changed path. A connection test alone cannot verify path mappings. Portico refuses targets it cannot scan narrowly. Use the [native notify recipe](/developers/api/integrations/quick-start/#notify-a-file-or-folder-change) when the connection cannot report the right path or retry a busy source.
