---
title: Add a Live TV source
description: Connect a compatible channel source, check its lineup and set its tuner limit.
category: live-tv
order: 2
audience: Server owners
updated: 2026-10-18
---

## Add a URL or tuner

1. Open **Settings → Server → Live TV & Channels → Sources** as the owner.
2. Select **Add source → Playlist URL or tuner**.
3. Choose **M3U playlist**, **Xtream Codes** or **HDHomeRun**.
4. Enter the name and playlist, provider or tuner address. For Xtream Codes, enter the provider username and password.
5. Add a **Guide (XMLTV) URL** if needed. Xtream Codes uses its own XMLTV endpoint when this is empty. HDHomeRun generally needs a separate guide.
6. Choose **Who can watch** and the guide-refresh interval.
7. Select **Check source**. Confirm local-network access only for the tuner and source addresses you intended to use.
8. Review the channel and program counts and any skipped entries, then select **Add source**.

For an HDHomeRun you haven't located yet, use **Add source → Find tuners on the network** and select the device.

## Upload playlist files

Select **Add source → Upload playlist files**. Choose an M3U or M3U8 playlist and, optionally, an XMLTV `.xml` guide. Set **Tuners** to the number of simultaneous streams the source allows, select **Check files**, then **Add source**. Uploaded playlist and guide files are stored on the server. Changes to the originals on your computer aren't imported automatically.

## Review the channels

Open the source's actions menu and select **Channels and numbers**. Hide unwanted channels, change channel numbers and correct guide listing IDs. Channel names come from the source; this dialog doesn't rename them.

Use **Source settings** for group and channel selection, filters, numbering, logo import, a shared guide source, and stream buffering or retry overrides. Changes to filters and numbering appear after the next refresh.

## Check playback and capacity

Play several channels before relying on guide mapping or recordings. If playback fails, check the source, tuner and server connection first. See [Add and map guide data](/docs/live-tv/guide-data/) once channels play.

HDHomeRun can supply its detected tuner count. Uploaded sources use the **Tuners** value; 0 means no configured limit. An unknown or unlimited value doesn't prove the provider allows unlimited streams. Use a capacity that matches the real source and don't increase it to hide recording conflicts.
