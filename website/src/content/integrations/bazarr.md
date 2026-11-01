---
title: Bazarr
description: Refresh subtitle sidecars without refreshing metadata.
kind: tool
order: 13
---

## Prepare Portico

Enable **Integrations compatibility** in **Settings → Server → Integrations**. Create a dedicated key with `read` and `library` scopes. Subtitle refresh requires owner authority.

## Configure Bazarr

1. In Bazarr’s server connection settings, select its **Jellyfin** connection.
2. Enter the Portico server URL and the dedicated API key, then test and save.
3. Match subtitle and media paths to Portico’s filesystem, including container mounts.

Item/provider lookup and numbered-season episode lookup use Portico’s catalog identities. For a series without a maintained folder identity, use immediate item validation rather than guessing a show directory.

## Validate one subtitle

Add or replace a subtitle sidecar and request validation for that item. This rereads eligible exact sidecars using stored media evidence; it does not enrich metadata, replace artwork or probe media. Missing or changed evidence produces a refusal.

For your own post-processing hook, use the [native subtitle refresh example](/developers/api/integrations/quick-start/#refresh-subtitles). Use the native item ID, not a tool’s translated ID. Check the response before marking the refresh complete.
