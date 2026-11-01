---
title: Manage scheduled tasks
description: Set backup windows, review scheduled jobs and control background work.
category: administration
order: 5
audience: Server owners
updated: 2026-10-18
---

Open **Settings → Server → Schedule** as the owner. **Scheduled jobs** shows scheduled work, the windows it runs in and the last and next run when available.

## Schedule backups

The built-in **Maintenance windows** schedule runs **Backups**. Choose the window's name, whether it's on, its cadence, start time and duration, and whether backups are included. Times use the server's time zone. Select **Save** after editing.

The default window is **Overnight**, starting at 03:00 for four hours each day. Scheduled backups use the retention count under **Settings → Server → Storage & backups → What Portico keeps**.

Disabling the window or its backup task stops future scheduled backups. It doesn't cancel a backup already running. Check **Backups** under **Storage & backups** for progress.

## Control other background work

Maintenance windows don't schedule every kind of server work:

- Library scans follow each library's settings under **Settings → Server → Libraries**, in its **Scanning** tab.
- Metadata matching, artwork and analysis follow library settings and the server's background-work controls.
- Remote Live TV sources have their own guide-refresh interval under **Settings → Server → Live TV & Channels → Sources**.
- Recording jobs follow program times and recording rules.

On **Schedule**, choose the background speed or select **Pause for an hour**. Use **Resume** to end the pause early. These controls manage background work; check **Settings → Server → Dashboard → Background activity** for what's actually running.

## Additional schedules

If an integration or API client has created additional scheduled jobs, they also appear under **Scheduled jobs**. Their **Edit** dialog controls whether they're enabled, the daily start window, its length, time zone and catch-up behaviour. The job kinds available depend on the server, including library scans, lyric fetching, retention and playback cleanup, notification maintenance and missing-title cleanup.

## Repeated failures

Check storage, permissions, network access, provider responses and database health before shortening a failing job's interval. Heavy scans, analysis, guide imports, image work and backups can compete with playback for resources.
