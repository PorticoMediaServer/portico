---
title: Sonarr
description: Notify Portico when a TV import changes a file.
kind: tool
order: 10
---

## Prepare Portico

Enable **Integrations compatibility** in **Settings → Server → Integrations**. Create a dedicated Sonarr API key with `read` and `library` scopes and access to the relevant libraries.

## Configure Sonarr

1. In Sonarr’s **Settings → Connect**, add its **Emby / Jellyfin** connection.
2. Enter Portico’s host, port (normally `32500`) and API key. Match the HTTPS setting to your server address. Use the server root; leave any optional URL base empty.
3. Make paths match those Portico sees, including container mounts. Configure path mappings where needed.
4. Enable the relevant import and upgrade notifications, run **Test**, then save.

Optional administrator notifications require owner authority and `admin` scope. Leave that option off when the key lacks them.

## Check a real import

Import one item and check Portico’s scan jobs. Updates must report a specific changed path. A successful connection test does not prove that a real import’s path is valid. A refused target is never replaced with a full-library scan. Use the [native notify recipe](/developers/api/integrations/quick-start/#notify-a-file-or-folder-change) if your tool cannot send a correct path or retry a busy source.
