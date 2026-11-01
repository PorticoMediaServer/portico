---
title: Update Portico Server
description: Prepare for, install, check and roll back a Portico Server update.
category: administration
order: 4
audience: Server owners
updated: 2026-10-18
---

Read the release notes before you update. Check the supported operating-system, database and client requirements.

Open **Settings → Server → General** to check the installed version and update information. **Check for new versions** controls update checks. Portico doesn't install updates automatically.

## Update the server

1. Check that the server is healthy and that important background work has finished.
2. Create a current backup, wait for it to finish and copy its complete folder to separate storage. See [Back up Portico Server](/docs/administration/backups/).
3. Write down the installed version and service configuration.
4. Install the update using the package method for your platform.
5. Wait for database and startup work to finish.
6. Test owner sign-in, a normal viewer, library browsing, local playback, and remote access.

## Keep a way back

Keep the previous package and the backup until the new version has been through normal household use.

A rollback must use a version that understands the current database schema. Don't restore an old executable over a changed database without checking that they're compatible.
