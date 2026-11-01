---
title: Choose a server
description: Pick hardware and storage that keep Portico available when people want to watch.
category: start
order: 3
audience: Server owners
updated: 2026-10-18
---

Portico Server should run on a computer that can stay on and keep steady access to your media folders. A desktop is fine for testing. For shared use, a dedicated computer, a home server or a properly configured virtual machine is usually a better choice.

## Start with the media path

The server needs reliable read access to every folder a library uses. Storage attached directly to the server is the simplest option. Network storage works too, as long as the Portico service account can reconnect to it after a reboot without anyone signing in first.

## Allow for playback work

Direct play takes very little processing. Transcoding can need much more processing power, especially for high-resolution video. Supported hardware acceleration reduces CPU work. So the right server depends on the files you keep and the devices that will play them. See [Direct play and transcoding](/docs/watch/direct-play-and-transcoding/) for how Portico chooses between them.

## Use a wired connection

Connect the server to your router or network switch with Ethernet whenever you can. Wi-Fi can work, but the server is the one device every stream depends on.

## Plan for Portico data

Put Portico's data folder on reliable storage with room for its database, artwork, logs and generated playback files. Back up this folder separately from your media. [Portico data folder](/docs/install/server-data-folder/) explains what it holds.
