---
title: Add and map guide data
description: Connect TV program listings and match them to the channels in your lineup.
category: live-tv
order: 3
audience: Server owners
updated: 2026-10-18
---

Guide listings tell Portico what's on each channel and when. Portico reads XMLTV program data, including gzip-compressed data fetched from a URL.

## Supply XMLTV listings

When adding a remote source under **Settings → Server → Live TV & Channels → Sources**, enter its **Guide (XMLTV) URL**. Xtream Codes uses the provider's XMLTV endpoint if you leave the guide URL empty. An HDHomeRun tuner generally needs an XMLTV URL supplied separately.

For an uploaded M3U playlist, choose an optional XMLTV `.xml` guide in **Upload playlist files**. An uploaded file stays as uploaded; it doesn't fetch new listings automatically.

A source can use another source's guide through **Source settings → Guide from**. The source configuration also supports **Guide only (XMLTV)** for a source used only for listings.

## Check channel mapping

Portico uses channel IDs to match listings. Open the source's **Channels and numbers** action to review its **Guide listing ID** for each channel. Correct unmatched or ambiguous IDs and save. A wrong mapping can record the wrong program even when live playback works.

## Keep listings current

Remote sources offer refresh intervals of 1, 6, 12 or 24 hours when added. Use **Refresh guide now** in the source's menu to request an immediate refresh. Keep the source's credentials valid and check the last guide update and any refresh error.

Under **Sources → Live TV defaults**, **Guide days** controls how far ahead Portico keeps listings, from 1 to 21 days. It can't create programs the provider hasn't supplied.

A failed refresh keeps the previously published lineup. Check the last successful refresh before removing and recreating a source.
