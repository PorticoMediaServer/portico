---
title: Direct play and transcoding
description: Understand why Portico sometimes sends the original file and sometimes prepares another stream.
category: watch
order: 5
audience: Everyone
updated: 2026-07-12
---

**Direct play** sends the original media in a form the device can use without changing it. It places the least load on the server and normally preserves the source quality.

**Remuxing** changes the container around compatible audio and video without re-encoding those tracks. It is lighter than transcoding but still creates a new delivery stream.

**Transcoding** converts one or more tracks while you watch. It is used when the device cannot handle the source format, a selected subtitle must be burned into the picture, or the requested quality is lower than the original file.

The server makes the final playback decision using the media, the client's reported capabilities, the selected tracks and quality, current policy, and the connection path.

If transcoding stutters, review [Buffering during playback](/docs/troubleshooting/buffering/) and the server's active stream diagnostics.
