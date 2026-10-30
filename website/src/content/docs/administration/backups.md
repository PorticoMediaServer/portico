---
title: Back up Portico Server
description: Protect the server database, settings, accounts, progress, and other Portico data.
category: administration
order: 2
audience: Server owners
updated: 2026-07-12
---

Back up the Portico data folder separately from the media folders. The two backups protect different things: a Portico backup protects the server database and configuration, while a media backup protects the original files.

## Create a backup

1. Open **Settings → Maintenance**.
2. Find **Backups** and select **Back up now**.
3. Wait for the new entry to appear in the backup list.
4. Confirm that the entry shows its creation time, file size, schema version, and **verified manifest**.

The backup contains the SQLite database and the Portico records stored in it, including server configuration, accounts, permissions, libraries, metadata choices, and watch progress. It does not copy your media library.

## Before a major change

Create a backup before updating Portico, moving the data folder, changing database storage, migrating to another computer, or performing a repair.

## Restore a backup

In **Settings → Maintenance → Backups**, select **Restore** beside an entry marked restore-ready. Confirm **Stage restore**. Portico verifies and stages the backup, then displays the restart instruction required to complete the restore.

Test important backups on an isolated Portico installation. Confirm that the owner can sign in and that libraries, people, permissions, and progress appear. Do not point a restore test at writable production media.

Keep at least one backup away from the server and its primary storage.
