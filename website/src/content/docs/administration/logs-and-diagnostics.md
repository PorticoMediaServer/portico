---
title: Read logs and diagnostics
description: Gather what you need to understand a failure without exposing private data.
category: administration
order: 3
audience: Administrators
updated: 2026-10-18
---

Start with the time, account, device and action involved. Note whether the same action works on another device or for another person.

## Find the relevant events

Open **Settings → Server → Troubleshooting**. Under **Logs**, filter by level and category. Turn on **Live** while you reproduce the problem, or refresh to fetch recent messages.

Owners can also use the **Server records** section on that page, which separates server, client and audit events. Record the time, message, level and any request or job identifier available. For active playback decisions and background tasks, open **Settings → Server → Dashboard**. See [Use the Server Console](/docs/administration/server-console/).

For a difficult problem, the owner can select **Record more detail for 30 minutes** and reproduce the failure during that window. Include the playback decision and any relevant transcoder log for playback problems.

## Prepare a support export

The server owner can use **Support export** on the same **Troubleshooting** page:

1. Choose a time window of 1 hour, 6 hours, 1 day or 3 days.
2. Select **Server records**, **Client uploads**, **Audit** or **Health snapshot** as needed for the problem.
3. Select **Prepare** beside **Prepare export**.
4. Review what was included, excluded and redacted, then select **Download JSON**.

This prepares a diagnostic file. It doesn't copy the visible log lines to the clipboard or send the file to support automatically.

## Protect private information

Review the downloaded file before sharing it, even when it reports redaction. Remove account identifiers, email addresses, tokens, cookies, server routes, local paths, public addresses and media names that should stay private. Don't paste an environment file or database into a support request.

Keep the export narrow: the failed action and a short period around it. Retention controls are under **Settings → Server → Storage & backups → What Portico keeps**.
