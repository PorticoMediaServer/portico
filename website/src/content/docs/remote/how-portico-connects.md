---
title: How Portico connects
description: How an app finds your server without sending your private media through Portico Services.
category: remote
order: 1
audience: Everyone
updated: 2026-10-18
---

You can connect with a Portico Account or sign in directly to a server account.

## What each side knows

- **Portico Services** manage Portico Accounts, server membership and signed connection information.
- **Your Portico Server** manages its media, local permissions, playback and server settings.

## Connect with a Portico Account

When an authorized app opens a server, it obtains short-lived signed connection information, checks the server's identity and opens a session for that server. At home it can use a verified local route; away from home it needs a reachable remote route.

## Connect directly

A server account can sign in at that server's address without a Portico Account. Portico Web bundled with the server already knows its address. It offers server account username-and-password sign-in when the server keeps sign-in local; a linked server's start page offers Portico Account sign-in and owner recovery instead. In a native app, enter the address and use the server account. See [Connect a Portico app](/docs/clients/connect-a-client/).

Direct sign-in also works away from home when the address is reachable and the owner's outside sign-in policy allows it. A server account isn't automatically a Portico Account or an entry in its server list.

## Where your media travels

Media and server-specific API requests go directly between the app and your Portico Server. Portico Services don't relay, tunnel, cache or inspect your private media.

## The server has the final say

Your server makes the final access decision, including for a request that started with a Portico Account. Membership through a Portico Account can't grant a library or action that the server doesn't allow.
