---
title: Create a library
description: Add a media folder to Portico and choose the library type that matches it.
category: media
order: 5
audience: Server owners
updated: 2026-10-18
---

A library is a named set of folders with one media type. The type controls how Portico scans, matches and shows the files. Keep movies and TV episodes in separate libraries.

## Library types

The **Add a library** dialog offers these choices under **Type**:

| Type | Media |
| --- | --- |
| **Movies** | Films, one per file or folder |
| **TV shows** | Series organized by season |
| **Anime** | Series with anime matching and absolute episode numbering |
| **Music** | Artists, albums and songs |
| **Audiobooks** | Books with chapters |

DVR recordings are managed separately in **Channels → Recordings** and aren't a library type.

## Add the library

1. Open **Settings → Server → Libraries**.
2. Select **Add library**.
3. Enter a **Name**.
4. Choose the media **Type**.
5. Enter **Folder on the server**, or select **Choose folder** to browse. Use a path the server can read, not a path on the device running your browser. In Docker, use the path inside the container, such as `/media/Movies`.
6. Choose a source under **Metadata** and a **Metadata language**, where offered.
7. Select **Add library**. Portico creates the library and starts its first scan.

The initial dialog takes one folder. To add more, open the library's **General** tab and select **Add folder** under **Folders**.

## Check the settings

- Check that Portico can read every folder and that each holds the selected media type.
- Review library access under **Settings → Server → People**.
- Review the library's **Scanning** and **Metadata** tabs for scan and matching options.

## After adding the library

Let the first scan finish before you make several changes at once. Then open a few items and check their titles, artwork, episode grouping and playback.
