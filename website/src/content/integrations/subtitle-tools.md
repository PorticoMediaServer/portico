---
title: Subtitle tools
description: Reread subtitle files without refreshing metadata.
kind: tool
order: 11
---

Tools that find and download subtitles can ask Portico to pick up a new or replaced subtitle file for one title.

## Prepare Portico

Turn on **Integrations compatibility** in **Settings → Server → Integrations**. Create an API key just for this tool, with `read` and `library` scopes. Refreshing subtitles needs owner authority.

## Add the connection

1. In the tool’s media server settings, add a connection that takes a server address and an API key.
2. Enter Portico’s address and the key, then test and save.
3. Make subtitle and media paths match the ones Portico sees, including container mounts.

Portico finds titles and numbered-season episodes by its own catalog identities. For a show without a maintained folder identity, validate the item directly rather than guessing a show folder.

## Check one subtitle

Add or replace a subtitle file and ask for that item to be validated. Portico rereads the matching subtitle files using what it already knows about the media. It doesn’t refresh metadata, replace artwork or probe the file. Missing or changed evidence is refused.

For your own post-processing hook, use the [native subtitle refresh example](/developers/api/integrations/quick-start/#refresh-subtitles) with Portico’s own item ID, and check the response before marking the refresh done.
