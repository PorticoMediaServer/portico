---
title: Live TV or recording problems
description: Separate source, guide, tuner, storage, schedule, and playback failures.
category: troubleshooting
order: 7
audience: Server owners
updated: 2026-07-12
---

Test live playback directly from the source configuration. If the source cannot provide a channel, guide and recording changes will not repair it.

If live playback works but guide data is wrong, review the last guide refresh and channel mapping. If the guide is correct but a recording did not start, review tuner availability, conflicts, schedule state, and recording-folder permissions.

If a recording exists but will not play, treat it as a media playback problem and inspect the file and playback decision.

Keep the source's real concurrent-stream limit in Portico. Raising it can make schedules appear valid while causing failures at recording time.
