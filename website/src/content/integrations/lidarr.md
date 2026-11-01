---
title: Lidarr
description: Send real changed paths after music imports.
kind: tool
order: 12
---

## Connection type

For its connection test, choose Lidarr’s **Emby / Jellyfin** connection under **Settings → Connect**. Enable **Integrations compatibility** in Portico and use a dedicated `read`/`library` API key. Its legacy URL prefix is accepted.

## Use a path notification for imports

The inspected Lidarr connection can send a collection label such as `music`, or request a refresh without a path. Portico refuses both: neither identifies a narrow change. A successful connection test does not mean import notifications will work.

Use a **Custom Script** connection for import and upgrade events. Put the [Python notify script](/developers/api/integrations/quick-start/#notify-a-file-or-folder-change) on the machine running the hook, and supply `PORTICO_URL` and a dedicated owner key with `library` scope to that process. Keep the key outside the script and its logs.

After each import, call it with the exact imported file or album directory, translated to Portico’s filesystem path:

```sh
python3 /opt/portico-hooks/notify.py '/media/Music/Artist/Album' changed
```

Replace the example with the real path from your import hook. Use `added` for a new target or `removed` for a deletion. Do not send a collection label, a source root or every music directory. Container paths must match Portico’s mounts.

## Check the result

Run the hook against one real import and inspect its output and Portico’s jobs. `202` means targeted work was queued. If the source is busy, honor `Retry-After` and retry the same path. Have the hook runner retain failed notifications for retry; do not silently fall back to an untargeted refresh.
