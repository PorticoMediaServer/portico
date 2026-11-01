---
title: Live TV or recording problems
description: Work out whether a Live TV problem comes from the source, guide, tuner, storage, schedule or playback.
category: troubleshooting
order: 7
audience: Server owners
updated: 2026-10-18
---

## Test the source first

Open **Settings → Server → Live TV & Channels → Sources**. Check whether the source is enabled and whether its last refresh failed. For a new remote source, **Check source** previews the channel and guide data before you save it. That preview doesn't prove that every channel will play.

Try the affected channel in **Channels**, and test it in the source provider's own player where available. If the source can't deliver the stream, changing guide data or recording rules won't fix it.

## Live TV plays but the guide is wrong

On **Sources**, check the guide update time. Open the source's menu and choose **Refresh guide now**. Use **Channels and numbers** to check channel mapping and **Source settings** to check which guide source is assigned.

## The guide is right but a recording didn't start

Check the recording under **Channels → Recordings**, then review:

- Source availability and tuner capacity at the scheduled time
- Recording conflicts and schedule state
- Recording permissions for the owning profile
- Write access and free space on the recording folder
- Storage limits and reserved free space under **Settings → Server → Live TV & Channels → Recording**

Read server events around the scheduled start under **Settings → Server → Troubleshooting**. [Connection troubleshooting basics](/docs/troubleshooting/connection-basics/) lists log locations.

## A recording exists but won't play

Check that the recording file exists and is readable. Follow [Playback failed](/docs/troubleshooting/playback-failed/) to check tracks, delivery and conversion.

## Keep tuner capacity accurate

When adding an uploaded playlist, set **Tuners** to the number of streams the source can actually deliver at once. A value of **0** means no configured limit. Remote-source previews and the source list show detected capacity where available. Check that count against the source's real limit. Raising a configured limit can't add tuners or provider capacity.
