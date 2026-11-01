---
title: Before you add Live TV
description: Prepare a channel source, guide data, storage and account access before you set up Live TV.
category: live-tv
order: 1
audience: Server owners
updated: 2026-10-18
---

Prepare a reachable channel source, guide data, recording storage and viewer access before scheduling recordings.

## Choose a source and guide

Portico accepts M3U playlists, Xtream Codes sources and HDHomeRun tuners. You can enter a source URL, discover an HDHomeRun on your network, or upload an M3U playlist. HDHomeRun channels marked as DRM-protected are skipped.

Guide listings use XMLTV. Supply a guide URL or upload an XMLTV file with a playlist. Xtream Codes can provide its own XMLTV guide. Live channels can play without listings, but guide-based recording needs matching program data.

Use only sources you're authorized to access where you live. See [Add a Live TV source](/docs/live-tv/add-a-source/) and [Add and map guide data](/docs/live-tv/guide-data/).

## Prepare recording storage

Open **Settings → Server → Live TV & Channels → Recording** to configure recording storage. The server service account needs write access. Leave enough free space for the expected quality and duration, and review storage limits and retention before relying on recordings.

## Check capacity

Live playback and recording share source capacity. They also use network bandwidth, disk throughput and, when conversion is needed, processing capacity. HDHomeRun capacity can be discovered; other sources need a limit that matches what the provider allows. Don't raise a limit beyond what the source can deliver.

## Decide who can watch and record

Review source visibility and each account's Live TV limits. Under **Recording → Who can record**, grant recording access to the intended accounts and profiles. Recording isn't granted to the owner automatically; an explicit profile decision takes priority over an inherited account permission.

Viewers manage recordings they own. Sharing access to a channel doesn't grant access to someone else's recordings or permission to delete them.
