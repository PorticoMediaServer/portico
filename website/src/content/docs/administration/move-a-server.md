---
title: Move Portico to another computer
description: Move your server data and media paths to a new computer without creating a second, unrelated server.
category: administration
order: 6
audience: Server owners
updated: 2026-10-18
---

Treat the move as a backup and restore. Keep the old server stopped while you check the restored copy. Otherwise two computers can publish the same server identity or write conflicting data.

## Move the server

1. Create a complete backup, including the companion files and manifest. Stop the old server after the backup finishes.
2. Install a compatible Portico version on the new computer and copy the completed backup folder to a location the new server can read.
3. Sign in as the new installation's owner. Open **Settings → Server → Storage & backups → Backups → Restore from a file or folder…** and select the copied folder. Follow [Back up Portico Server](/docs/administration/backups/) to restore it. Portico restarts automatically.
4. Sign in with the restored account and check the restore result.
5. Make the media available at the expected paths. If paths have to change, update them under **Settings → Server → Libraries**, confirm the server can access them, then scan.

Copy operating-system service configuration and owner-supplied certificates separately where needed. A standalone database file doesn't carry all the identity files in a complete backup.

## Check the new system

On the new computer, check:

- service-account permissions
- storage mounts
- firewall rules
- local address
- remote-access state
- certificate paths

## Test before you go live

Test local playback with a normal viewer, then check remote access using the new computer's address and firewall configuration. Keep the old system and the backup intact until the new server has been checked.
