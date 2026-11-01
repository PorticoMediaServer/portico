---
title: Portico data folder
description: What Portico stores outside your media folders, and why it needs its own backup.
category: install
order: 6
audience: Server owners
updated: 2026-10-18
---

The Portico data folder holds the server database and the working files Portico uses to turn your media folders into a library. It's separate from the media itself.

## Default locations

| Installation | Default data folder |
| --- | --- |
| Windows installer | `%ProgramData%\Portico Media Server` |
| macOS package | `/Library/Application Support/Portico Media Server` |
| Linux DEB or RPM | `/var/lib/portico-media-server` |
| Docker image | `/var/lib/portico` inside the container |
| Windows or Linux portable archive | `state` beside the launcher |

The installed packages run as services. These are service data folders, separate from your desktop account's application preferences. Docker needs a persistent volume mounted at its data path.

Portable launchers accept an absolute `PORTICO_STATE_DIR` to use another folder. Changing a service's data path also requires the appropriate filesystem permissions and, on Linux, a writable path in its systemd sandbox. See the installation instructions for your platform.

## What's in it

Depending on which features you use, the folder can contain:

- account and permission records
- library metadata
- artwork
- watch progress
- server settings
- remote-access state
- logs
- generated playback files
- maintenance data

The live database is `server.sqlite`. Built-in backups name their database copy `portico.db`; see [Back up Portico Server](/docs/administration/backups/). Keep the whole data folder for a server backup, including its identity and connection state. Generated files can use a separate cache location through `PORTICO_CACHE_DIR`, and database backups can use `PORTICO_BACKUP_DIR`. Both paths must be absolute and writable by the server. A saved conversion-folder setting can override the temporary conversion path.

## Storage requirements

Put the folder on reliable local storage, and give the service account write access to it. Avoid:

- temporary directories,
- removable drives that aren't always connected,
- network paths that can disappear while the server is running.

## Back it up

A media backup protects the files you watch. A Portico data backup protects everything Portico has built around those files. Keep both. Follow [Back up Portico Server](/docs/administration/backups/) before an update, a migration or a storage change.
