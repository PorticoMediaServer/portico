---
title: Buffering during playback
description: Compare the file, playback decision, server load, storage, and network path.
category: troubleshooting
order: 4
audience: Everyone
updated: 2026-07-12
---

Try another item on the same device. If only one file buffers, compare its format, bitrate, tracks, subtitles, and storage location with an item that works.

Open **Settings → Status** and select the active stream. Record the playback method, source and output bitrate, selected quality, video and audio codecs, subtitle mode, and transcode speed. A transcode can be limited by CPU, hardware acceleration, temporary storage, or a subtitle that must be burned into the picture.

For direct play, compare the media bitrate with the available network path. Test the server over wired Ethernet and remove Wi-Fi, VPN, or remote internet variability where possible.

Lower the selected quality as a diagnostic step. If a lower stream works, inspect **Settings → Playback** and the remote bitrate limit under **Settings → Connectivity** before leaving the quality reduced permanently.

If every transcode buffers, copy the active-stream details and the matching Server Console events. If only one device fails, review [How clients differ](/docs/clients/client-differences/). If playback fails instead of buffering, use [Playback failed](/docs/troubleshooting/playback-failed/).
