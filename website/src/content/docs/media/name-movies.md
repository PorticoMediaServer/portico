---
title: Name movie files
description: Movie folder and file names that Portico can match consistently.
category: media
order: 2
audience: Server owners
updated: 2026-10-18
---

Put each movie in its own folder. Include the release year when different films share a title.

## Example layout

```text
Movies/
  Arrival (2016)/
    Arrival (2016).mkv
```

## Extras, versions and subtitles

Keep bonus material, alternate versions and subtitles inside the movie's folder. Use recognized extras folders such as `extras`, `trailers` or `behind the scenes`, or a suffix such as `Arrival (2016)-trailer.mkv`.

Name alternate editions explicitly, for example `Arrival (2016) {edition-Extended Cut}.mkv`. Keep the movie title and year consistent across versions. Match a subtitle's base name to the video, for example `Arrival (2016).en.srt`. See [Choose audio and subtitles](/docs/watch/audio-and-subtitles/).

For local artwork, use `poster.jpg` and `fanart.jpg`, or the video's base name followed by `-poster.jpg` or `-fanart.jpg`. PNG versions are also recognized. Keep unrelated movies in separate folders so folder artwork and extras belong to the right film.

## Before rescanning

If you correct a name after Portico has already matched the item, scan the library again. If the file is organized correctly but the title Portico picked is still wrong, use the match tools on the item's detail page. See [Fix an incorrect match](/docs/media/fix-a-match/).
