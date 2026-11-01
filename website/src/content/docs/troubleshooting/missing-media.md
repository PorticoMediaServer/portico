---
title: Media is missing from a library
description: Check access, folders, naming, scans and filters when an item doesn't appear.
category: troubleshooting
order: 3
audience: Server owners
updated: 2026-10-18
---

## Check with the owner

Look for the item with the owner account. If it appears there but not for a viewer, check the viewer's profile restrictions and library access under **Settings → Server → People**. An administrator can also have restricted library access, so that role alone doesn't prove the file is missing.

## Check the folder and permissions

Open **Settings → Server → Libraries**, select the library and check **Folders** on its **General** tab. The file must be inside a folder assigned to that library. The Portico service account needs permission to read the entire path. Your desktop account seeing a network drive doesn't establish that the service can read it. In Docker, use the path mounted inside the container.

To add a missing library, select **Add library** under **Settings → Server → Libraries**. This opens the **Add a library** dialog.

## Check filters, then scan

1. Clear the library's filters and search terms.
2. Under **Settings → Server → Libraries**, open the library and select **Scan now**.
3. Open **Scanning** and review **Last scan**. Use **What the last scan skipped → See the list** for files and folders left out, with the reason.
4. Review **Needs attention** where that tab is available for unresolved matches. Check server logs under **Settings → Server → Troubleshooting** for path and scan failures.

## If it appears under the wrong title

Fix the filename, then follow [Fix an incorrect match](/docs/media/fix-a-match/). Don't keep adding the folder again.
