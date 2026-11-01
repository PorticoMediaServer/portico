---
title: Use Portico on the web
description: Use Portico Web at web.getportico.tv or bundled with your own Portico Server.
category: clients
order: 2
audience: Everyone
updated: 2026-10-18
---

Portico Web is the browser app bundled with your Portico Server and hosted at [web.getportico.tv](https://web.getportico.tv).

## Bundled Portico Web

Open your server's address in a browser, including its port. The default is 32500, for example `http://192.168.1.20:32500` on your home network. Portico Web bundled with your server connects to that server.

Use it for first-run setup and owner access. On a server that keeps sign-in local, enter your server username and password. On a server linked to a Portico Account, the start page offers Portico Account sign-in and **Owner recovery sign-in**; recovery is for the owner. For a normal server account on a linked server, use a native app or direct sign-in in hosted Portico Web with a reachable HTTPS address your browser trusts. See [Connect a Portico app](/docs/clients/connect-a-client/).

## Hosted Portico Web

Sign in at [web.getportico.tv](https://web.getportico.tv) with your Portico Account to open servers shared with you. Portico verifies the server's route and identity before opening it.

For a server account, choose **Sign in directly to a server** and enter a reachable HTTPS server address your browser trusts, then your server username and password. For local HTTP access, use Portico Web bundled with a server that keeps sign-in local.

Hosted Portico Web doesn't relay your media. Your browser requests authorized media directly from the selected Portico Server.

## Browser permissions and playback

Use a current browser. Allow fullscreen, media playback or local-network access when requested for an action you chose. Browser codec support affects whether media can play directly or needs conversion.

The Portico Web player has its own volume control in addition to your operating system's volume. Browser certificate and mixed-content rules still apply: native apps' support for an owner's certificate doesn't change what a browser trusts. For local HTTP access, open Portico Web at the server's address; its sign-in options depend on whether the server keeps sign-in local or uses a Portico Account.
