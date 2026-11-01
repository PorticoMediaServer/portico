---
title: Report a product issue
description: How to send a reproducible client or server bug report without sharing secrets or private media.
category: developers
order: 5
audience: Developers
updated: 2026-10-18
---

## Describe the failure

Identify:

- The Portico component and version
- The operating system or device
- Whether you used Portico Web bundled with the server, Portico Account sign-in or a native app
- The exact action that failed
- What you expected, and what happened instead

## Narrow it down

Reduce the problem to the shortest sequence that still fails. Say whether it happens for every account, library, media item, server, network or client.

## Attach evidence

Include a request identifier, a short relevant log excerpt and the playback decision when they apply. In Portico Web, server logs are under **Settings → Server → Troubleshooting**. The server version appears under **Settings → Server → General**.

Before you send them, remove tokens, cookies, emails, public or private addresses, filesystem paths, and media names that should stay private.

## Security problems

Report security vulnerabilities through [Report a security issue](/docs/security/report-a-security-issue/), not as a public product issue.
