---
title: Media is missing from a library
description: Check permissions, folders, naming, scan state, and filters when an item does not appear.
category: troubleshooting
order: 3
audience: Server owners
updated: 2026-07-12
---

First test with an owner or administrator account. If the item appears there but not for a viewer, review the viewer's library access.

Confirm that the file is inside a folder assigned to the expected library and that the Portico service account can read the complete path. A desktop account seeing a network drive does not prove the service can see it.

Check the current library filters and search terms, then run a scan for that library. Review the scan result for unreadable paths, unsupported files, or matching failures.

When the item appears under the wrong title, fix the filename and use [Fix an incorrect match](/docs/media/fix-a-match/) instead of repeatedly adding the folder.
