---
title: Resolve recording conflicts
description: Why overlapping schedules compete for the same source, and how to choose which recording runs.
category: live-tv
order: 5
audience: Server owners
updated: 2026-10-18
---

Recordings can compete for the same tuners or provider stream limit. Live viewers can also occupy that capacity. Storage limits and an unavailable source can prevent a recording even when tuners are free.

## Compare overlapping recordings

1. Open **Channels → Recordings → Upcoming** and the recording's **Resolve conflict…** action.
2. Review the overlapping programs and available tuner count.
3. Choose **Record this instead** to raise this recording above the conflicting recordings, or **Skip this one** to cancel it.

Higher-priority recordings take precedence. Equal priorities are ordered by start time, then by the server's stable recording identifier. The server can share a capture for recordings on the same channel, so every scheduled entry doesn't necessarily consume another tuner.

If a program airs again, schedule that showing instead. Reduce start or end padding only when the broadcaster's timing makes that safe. Padding counts towards overlap.

## When someone is watching live

The owner chooses the policy under **Settings → Server → Live TV & Channels → Recording**, in **When a recording needs a tuner someone is watching**.

- **Keep watching (the recording does not start)** is the default. The recording waits for capacity and can start late if a tuner becomes free; Portico notifies you.
- **Stop watching (the recording takes the tuner)** gives the live viewer a countdown before stopping playback to free the tuner.

## Set a real capacity

Only raise a tuner limit if the source supports another simultaneous stream. An unknown capacity is an estimate, not a guarantee that overlapping recordings will work. Portico doesn't reject a schedule solely because of an unknown-capacity estimate.

Check recording state and **Channels → Recordings → History** for a late start, incomplete recording or failure. Free space, provider availability and a correct channel mapping still matter after a scheduling conflict is resolved.
