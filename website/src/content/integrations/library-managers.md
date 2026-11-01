---
title: Library managers
description: Tell Portico which file changed after a download or import.
kind: tool
order: 10
---

Tools that download, rename or organise films, shows and music can tell Portico exactly which file or folder changed, so it updates that title instead of scanning a whole library.

## Prepare Portico

Turn on **Integrations compatibility** in **Settings → Server → Integrations**. Create an API key just for this tool, with `read` and `library` scopes and access to the libraries it manages.

## Add the connection

1. In the tool’s connection or notification settings, add a media server connection that takes a server address and an API key.
2. Enter Portico’s host, port (normally `32500`) and the key. Match the HTTPS setting to your server address. Use the server root and leave any URL base empty; an older URL prefix is accepted too.
3. Make the tool’s paths match the ones Portico sees, including container mounts. Set up path mappings where they differ.
4. Turn on the import and upgrade notifications you want, run the tool’s test, then save.

Some tools also offer administrator notifications. They need owner authority and the `admin` scope; leave them off when the key doesn’t have both.

## When the connection can’t name a path

Some connections send only a library label, or ask for a refresh without a path. Portico refuses both, because neither says what changed. A passing connection test doesn’t prove that imports will work.

For those tools, use a custom script hook instead. Put the [Python notify script](/developers/api/integrations/quick-start/#notify-a-file-or-folder-change) on the machine that runs the hook, and give that process `PORTICO_URL` and an owner key with the `library` scope. Keep the key out of the script and its logs. After each import, call it with the exact file or folder, written as Portico sees it:

```sh
python3 /opt/portico-hooks/notify.py '/media/Music/Artist/Album' changed
```

Use `added` for something new and `removed` for a deletion. Never send a library label, a source root or every folder.

## Check a real import

Import one item and look at Portico’s scan jobs. Each update must name a specific changed path. `202` means targeted work was queued. If the source is busy, wait for `Retry-After` and retry the same path. Portico never swaps a refused target for a full-library scan, so have the hook keep failed notifications and retry them.
