---
title: Update Portico Server
description: Prepare, install, verify, and roll back a Portico Server update.
category: administration
order: 4
audience: Server owners
updated: 2026-07-12
---

Read the release notes and check the supported operating-system, database, and client requirements before updating.

1. Confirm the server is healthy and important background work is finished.
2. Create and verify a current backup.
3. Record the installed version and service configuration.
4. Install the update using the package method for the platform.
5. Wait for database and startup work to finish.
6. Test owner sign-in, a normal viewer, library browsing, local playback, and remote access.

Keep the prior package and backup until the new version has run through normal household use. A rollback must use a version that understands the current database schema; do not restore an old executable over a changed database without checking compatibility.
