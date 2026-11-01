---
title: Choose audio and subtitles
description: Pick audio and subtitle tracks, and set your preferred playback languages.
category: watch
order: 4
audience: Viewers
updated: 2026-10-18
---

## Pick a track while you watch

Open the player's audio or subtitle controls to see the tracks available for what you're playing. Choose a track while playback is open.

## Where subtitles come from

Subtitles can be embedded in the media file or stored beside it. Sidecar files need the same base name as the video, optionally followed by a language or track flag. For example:

```text
Arrival (2016).mkv
Arrival (2016).en.srt
Arrival (2016).fr.forced.srt
```

Portico recognizes sidecars including SRT, WebVTT, ASS and SSA. Playback depends on the subtitle format and your device. Server owners can also configure online subtitle search.

## Personal defaults

1. Open **Settings → Account → Audio & subtitles**.
2. Under **Audio**, set **Preferred audio languages** in order of preference.
3. Under **Subtitles**, set **Preferred subtitle languages** and **Show subtitles**. The subtitle choices are **Off**, **Forced only** and **Always**.

Portico uses the first preferred language available for the title. With no preferred audio languages, it uses the title's original track. These settings belong to your profile. A preference can't add a track the media doesn't have.

## Subtitles and transcoding

Some subtitles have to be drawn into the picture, which can require video transcoding. If quality changes or transcoding starts after you turn subtitles on, try another available subtitle format. Ask the server owner to review the playback decision if needed. [Direct play and transcoding](/docs/watch/direct-play-and-transcoding/) explains that decision.
