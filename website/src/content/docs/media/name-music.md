---
title: Organize music
description: Arrange artists, albums, tracks and embedded tags for a reliable music library.
category: media
order: 4
audience: Server owners
updated: 2026-10-18
---

Portico matches music using both the folder structure and the tags embedded in each audio file. Correct tags matter more than how the filenames look.

## Folder layout

Use a stable artist and album structure:

```text
Music/
  Artist/
    Album (2024)/
      01 - First Track.flac
```

## Check your tags

Before you scan a large collection, check these tags:

- artist
- album artist
- album
- disc number
- track number
- title
- release year

Consistent album-artist tags stop compilations and collaborations from splitting unexpectedly.

## Artwork and rescanning

If you keep your own artwork, put `cover.jpg`, `folder.jpg` or `cover.png` in the album folder. Embedded cover art is also read. After you change embedded tags, scan the library again. Renaming a file on its own may not change what Portico displays.
