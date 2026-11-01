---
title: Playback failed
description: What to check when an item won't start or stops with an error.
category: troubleshooting
order: 5
audience: Everyone
updated: 2026-10-18
---

## Find out whether it's the media or the device

Play the same item on another Portico client. Then play a different item on the client that failed. Record the exact error and time, and whether it affects one account or every account.

## Check the file and delivery

Confirm that the file still exists and that the Portico service can read it. As an owner or administrator, check **Settings → Server → Dashboard → Watching now** while retrying, if a session appears. Note its delivery method and bitrate when available.

Check the matching events under **Settings → Server → Troubleshooting → Logs**. [Connection troubleshooting basics](/docs/troubleshooting/connection-basics/) lists file and service log locations. Conversion failures appear in the server logs.

If conversion is involved, check **Settings → Server → Streaming** for conversion settings, available media tools, hardware acceleration and a writable conversion folder with enough free space.

## Check audio and subtitle tracks

Note which audio and subtitle tracks are selected. Try another audio track or temporarily turn subtitles off. An unsupported or damaged track, or a subtitle that needs converting or burning into the picture, can change the delivery method.

## Don't keep rebuilding the library

Don't refresh metadata or rebuild the library to fix a playback error unless the stored media path is wrong. Library details and media delivery are separate parts of the server.
