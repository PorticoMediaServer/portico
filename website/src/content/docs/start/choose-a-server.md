---
title: Choose a server
description: Pick hardware and storage that can keep Portico available when your household needs it.
category: start
order: 3
audience: Server owners
updated: 2026-07-12
---

Portico Server should run on a computer that can stay on and maintain steady access to the media folders. A desktop is fine for testing. A dedicated computer, home server, or properly configured virtual machine is usually better for shared use.

## Start with the media path

The server needs reliable read access to every folder used by a library. Locally attached storage is straightforward. Network storage also works when the server account can reconnect to it after a reboot without someone signing in first.

## Allow for playback work

Direct play requires little processing. Transcoding can require considerably more CPU or supported hardware acceleration, especially for high-resolution video. The right server therefore depends on the files you keep and the devices that will play them.

## Prefer a wired connection

Connect the server to the router or network switch with Ethernet whenever possible. Wireless connections can work, but the server is the one device that every stream depends on.

## Plan for Portico data

Keep Portico's data folder on reliable storage with room for its database, artwork, logs, and generated playback files. Back up this folder separately from the media itself.
