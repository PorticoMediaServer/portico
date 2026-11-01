---
title: Name TV episodes
description: Group shows, seasons and episodes so Portico can build a complete series.
category: media
order: 3
audience: Server owners
updated: 2026-10-18
---

Use one folder for each show, with a folder for each season inside it. Put the season and episode number in every episode's filename.

## Example layout

```text
TV Shows/
  Example Show (2024)/
    Season 01/
      Example Show - S01E01 - Pilot.mkv
      Example Show - S01E02 - Second Episode.mkv
```

## Specials, multi-episode files and alternate cuts

Use `Season 00` or `Specials` for specials, with filenames such as `Example Show - S00E01.mkv`, when the metadata source uses a specials season.

For a file containing several episodes, use `S01E01E02` for episodes 1 and 2, or `S01E01-E03` for episodes 1 through 3. Portico recognizes up to eight episode links per file. Check that the chosen metadata order agrees with your numbering.

Keep alternate cuts clearly labelled and check their grouping after a scan. Consistent naming alone doesn't guarantee that separate files will be grouped as one version.

## After renaming

When you change episode names, scan the library again. Then check that each episode appears under the right show and season.
