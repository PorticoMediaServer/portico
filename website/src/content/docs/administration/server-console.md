---
title: Use the Server Console
description: Check server health, active streams, work, alerts, and connection state.
category: administration
order: 1
audience: Administrators
updated: 2026-07-12
---

Open **Settings → Diagnostics** to see the Server Console.

## Check server health

The **Server health** section reports four checks: Database, Web application, Workload, and SQLite health. It also lists packaged dependencies and their detected versions. Start here when the server opens but a feature does not work.

## Find a recent event

The **Server console** lists up to 250 recent events. Use **Filter events** to search the message and fields, or choose a level such as **Warn** or **Error**. Select **Copy visible** to copy the filtered events before opening a support request. Sensitive values are redacted by the server.

Record the event time, level, message, and any request or job identifier shown in its fields. Select **Refresh** after reproducing a problem to load the newest events.

## Check the installed release

The **Release** section shows the Portico version, API version, installation method, operating system, architecture, update channel, and database migration state. Include these details when reporting an issue that began after an update.

For current playback decisions and server work, open **Settings → Status**. Use **Settings → Connectivity** for remote-access health and the current public route.
