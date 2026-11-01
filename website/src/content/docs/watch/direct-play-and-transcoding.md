---
title: Direct play and transcoding
description: Why Portico sometimes sends the original file and sometimes prepares another stream.
category: watch
order: 5
audience: Everyone
updated: 2026-10-18
---

Portico can deliver media three ways. Which one you get depends on the file, your device and your settings.

## Direct play

**Direct play** sends the original media in a form your device can use as it is. It puts the least load on the server and normally keeps the source quality.

## Remuxing

**Remuxing** changes the container around compatible audio and video without re-encoding those tracks. It's lighter than transcoding, but it still creates a new stream for delivery.

## Transcoding

**Transcoding** converts one or more tracks while you watch. Portico uses it when:

- the device can't handle the source format,
- a selected subtitle has to be burned into the picture,
- the requested quality is lower than the original file.

## How the server decides

The server makes the final playback decision. It takes into account:

- the media,
- the capabilities the app reports,
- the selected tracks and quality,
- current policy,
- the connection path.

## If transcoding stutters

Read [Buffering during playback](/docs/troubleshooting/buffering/) and check the server's active stream diagnostics.
