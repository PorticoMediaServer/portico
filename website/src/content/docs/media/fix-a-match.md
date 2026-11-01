---
title: Fix an incorrect match
description: Correct the title or details Portico picked for a movie, show, album or other item.
category: media
order: 7
audience: Server owners
updated: 2026-10-18
---

Check the file and folder names first. A manual correction fixes what's displayed. Clear names reduce matching errors after a rebuild or migration; keep a backup to preserve manual corrections. See [Name movie files](/docs/media/name-movies/), [Name TV episodes](/docs/media/name-tv-shows/) and [Organize music](/docs/media/name-music/).

## Choose the right match

1. Open the item's detail page and its **More** menu.
2. Choose **Edit metadata…**, then **Matching**.
3. For movies and shows, enter a title or provider ID in **Search the provider**, then choose **Search**. Include the release year when it helps distinguish titles. For music, search uses the scanned metadata.
4. Compare the results and select **Use this match**. Review the episode order when it's offered.

Available matching tools depend on the media type and metadata source. Some items, including audiobooks or artists without a remote matcher, need local metadata or manual edits instead of provider search.

## If no result is right

Check that:

- the library type matches the file,
- the server can reach its configured metadata providers,
- the title exists in the selected source.

For uncommon or personal media, use local metadata and artwork where that's supported, rather than forcing an unrelated match.

## Check the result

After you change a match, review the title, year, artwork, episode or track structure, and the available media file. Do this before you apply the same correction to other items.
