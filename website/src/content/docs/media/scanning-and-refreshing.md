---
title: Scan and refresh a library
description: Know when Portico should look for files and when it should update details for known items.
category: media
order: 6
audience: Server owners
updated: 2026-07-12
---

A scan checks library folders for added, changed, moved, or removed files. Run one after changing the contents or organization of a library.

A metadata refresh revisits details such as titles, summaries, people, ratings, and artwork for items Portico already knows about. It does not repair an unreadable folder or a missing file.

## Use the narrowest action

Refresh one item when only its details are wrong. Scan one library when files changed in that library. A full-server operation is useful after a broad storage change, but it creates more work and makes problems harder to isolate.

Portico also supports scheduled and folder-aware work where the platform permits it. Review scheduled tasks before running large jobs during busy playback hours.
