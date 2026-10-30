---
title: Playback failed
description: Work through an item that will not start or stops with an error.
category: troubleshooting
order: 5
audience: Everyone
updated: 2026-07-12
---

Try the same item on another Portico client and another item on the affected client. This separates a media problem from a device or account problem.

Confirm that the file still exists and the Portico service can read it. Review the playback decision and any transcoder log created for the attempt.

Check the selected audio and subtitle tracks. A corrupt track, unsupported format, or subtitle that requires conversion can change the playback path.

Do not repeatedly refresh metadata or rebuild the library for a playback error unless the stored media path itself is wrong. Library details and media delivery are separate parts of the server.
