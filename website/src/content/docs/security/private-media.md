---
title: Where private media travels
description: Why Portico Services don't relay or store your media.
category: security
order: 2
audience: Everyone
updated: 2026-10-18
---

Portico Services never carry your video or audio. An authorized Portico app streams private media directly from the Portico Server that holds it, over a verified local or remote connection.

## What Portico Services do

Portico Services help an account find the servers it can use and supply signed connection information. They don't relay, tunnel, cache, inspect, transcode or store the media stream.

## What this means for server owners

Because media doesn't pass through Portico Services, the server owner keeps control of storage, upload capacity, playback work and library permissions. It also means remote playback only works when the server and its internet connection are available.

## Artwork and metadata

Private-library metadata and artwork served by Portico Server require authorization. Before serving a protected file, the server checks that the requesting account and profile have access, or that a valid permission was issued for that resource.

Save-for-later items appear under **Saved → My List**. My List, history and other personal media state belong to the selected profile on the server. They aren't sent to Portico Services.

## External services you choose

Metadata and artwork providers can receive lookup requests from your server. Owner-configured webhooks can send titles and playback events to a chosen destination. These are separate from the account and connection coordination that Portico Services provide; check which services the owner has enabled.
