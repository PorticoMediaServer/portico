---
title: Portico data folder
description: Understand what Portico stores outside your media folders and why it needs a separate backup.
category: install
order: 6
audience: Server owners
updated: 2026-07-12
---

The Portico data folder contains the server database and the working files needed to turn media folders into a usable library. It is separate from the media itself.

Depending on the enabled features, it may contain account and permission records, library metadata, artwork, watch progress, server settings, remote-access state, logs, generated playback files, and maintenance data.

## Storage requirements

Place the folder on reliable local storage. The service account needs write access. Avoid temporary directories, removable drives that are not always present, and network paths that can disappear while the server is running.

## Back it up

A media backup protects the files you watch. A Portico data backup protects the work around those files. Keep both. Follow [Back up Portico Server](/docs/administration/backups/) before an update, migration, or storage change.
