---
title: Manage scheduled tasks
description: Choose when scans, analysis, maintenance, guide work, and backups can run.
category: administration
order: 5
audience: Administrators
updated: 2026-07-12
---

Scheduled tasks keep the server current without requiring routine manual work. They can also compete with playback for CPU, storage, and network capacity.

Use the task settings to choose which automated work is enabled and when it may start. Schedule heavy scans, analysis, guide imports, image work, and backups for times that match how the server is used.

Disabling scheduled scanning or analysis should stop new scheduled work from being queued. It does not necessarily cancel an operation that is already running; review the active task before assuming the server is idle.

Repeated failures usually indicate a permissions, storage, network, provider, or database problem. Fix the cause before shortening the interval.
