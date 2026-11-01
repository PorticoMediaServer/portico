---
title: Back up Portico Server
description: Back up and restore the server database, settings, accounts, progress and other Portico data.
category: administration
order: 2
audience: Server owners
updated: 2026-10-18
---

Back up Portico's server data separately from your media. The built-in backup protects the database and selected server files; your original media and recordings need their own backups.

## What a backup contains

Each backup folder contains `portico.db`, a checksum manifest and selected files that can't be rebuilt from your media. Backups store a copy of the live `server.sqlite` database from the [Portico data folder](/docs/install/server-data-folder/) as `portico.db`. The selected files include server identity keys, TLS identity, the server state file `supervisor/authority-incarnation.v1`, managed-storage configuration, channel logos and subtitles. Database records include settings, accounts, permissions, libraries, metadata choices and watch progress.

Backups exclude media files, recordings, generated artwork, conversion caches and logs. Certificates you supply outside the selected server files and operating-system service configuration need separate backups.

## Create and locate a backup

1. Sign in as the owner and open **Settings → Server → Storage & backups**.
2. Under **Backups**, select **Back up now**.
3. Wait for the backup to finish and appear in the list. Each entry shows its time, type, size, schema version, server version and path.
4. Copy the entire completed backup folder to separate storage, including its manifest and companion files.

The default location is `backups/` inside the server data folder. Set `PORTICO_BACKUP_DIR` to an absolute path before starting Portico to use another destination. The service account needs write access there. Pre-restore recovery copies stay beside the live database.

Scheduled backups run in the windows set under **Settings → Server → Schedule**. **Backups to keep** under **Settings → Server → Storage & backups → What Portico keeps** controls scheduled retention; the default is seven. Manual backups remain until deleted.

## When to back up

Create a backup before an update, move, storage change, migration or repair. Keep at least one copy away from the server and its primary disk.

## Restore a backup

1. Open **Settings → Server → Storage & backups → Backups** as the owner.
2. Select **Restore this backup** beside a listed backup. For a copied backup, choose **Restore from a file or folder…** and select its folder or a supported standalone Portico `.db` file on the server. This browses the server's files, not your browser's computer.
3. Read the confirmation and select **Restore backup**.
4. Wait while Portico validates, copies and stages the backup. It then restarts automatically, interrupting playback.
5. Sign in again and check that the result says **Restored**. Check libraries, people, permissions, progress and server identity before normal use.

Portico checks integrity and schema compatibility. A backup with a newer schema needs a newer server. It keeps the preceding data as a **Before restore** backup. If recovery can't finish safely, Portico refuses to start with partly restored data. Fix the reported disk or permission problem and start it again; keep the recovery files.

A standalone database file doesn't contain the companion identity and other files in a full backup folder. Prefer a complete backup for moving a server. See [Move Portico to another computer](/docs/administration/move-a-server/).

## Review security after restoring

People are signed out. Passwords, two-step verification and approved devices return to their backup-time state. API keys and personal access tokens survive, so a credential revoked after the backup can work again. Review them after restoring.

Test important backups on an isolated installation. Don't point the test at production media it can write to. For a complete offline recovery copy, stop Portico cleanly and copy the whole data folder plus external media, recordings, certificate files and service configuration you need.
