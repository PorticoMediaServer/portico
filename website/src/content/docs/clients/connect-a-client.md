---
title: Connect a Portico app
description: Sign in, choose a server you have access to, and check that the app can reach it.
category: clients
order: 1
audience: Viewers
updated: 2026-10-18
---

Use the kind of account the server owner gave you: a Portico Account invitation or a server account. A server account doesn't require a Portico Account.

## Connect with a Portico Account

1. Accept the server invitation with the Portico Account you intend to use.
2. Sign in to the app with that account.
3. Open the server you want. The app can open your only server or the server you used last automatically.

At home the app can prefer a verified local route. Away from home it uses current signed connection information. The server still checks your membership, profile and permissions before opening its libraries.

## Use a server account in a browser

On a server that keeps sign-in local, open the address the owner provides on its local network, such as `http://192.168.1.20:32500`. Enter the username and password created on that server. Portico Web bundled with the server already knows which server to connect to.

If the server is linked to a Portico Account, its bundled start page offers Portico Account sign-in and **Owner recovery sign-in**. The recovery option is for the owner. For a normal server account on that server, use direct sign-in in a native app. In Portico Web at [web.getportico.tv](https://web.getportico.tv), select **Sign in directly to a server**. Enter a reachable HTTPS server address your browser trusts.

For direct local HTTP sign-in, open Portico Web bundled with a server that keeps sign-in local. Hosted Portico Web uses HTTPS and is subject to browser certificate, mixed-content and local-network rules.

## Use a server account in an app

1. Choose **Sign in directly to a server** to open **Sign in to a server**.
2. Enter the owner's server address, including the port if needed.
3. On a phone or tablet, enter the server account's username and password, then select **Sign in**.
4. Complete two-step verification, a required password change or device approval if requested. Choose your profile when prompted.

On a television, entering the address normally shows a Quick Connect code. Approve it from a device already signed in to that server: open **Settings → Account → This device → Link a TV** on a phone, or the server's `/device` page in a browser. You can also choose **Use a password instead**.

The server account can connect away from home too, if the owner provides a reachable address and permits outside sign-in. It won't appear in a Portico Account's server list just because you know its address.

## HTTP and certificates

Apple apps allow plain HTTP only to local-network addresses. Use HTTPS for remote public addresses. Other platforms allow HTTP from anywhere; HTTPS protects traffic on untrusted networks.

Portico apps support the owner's own certificate, including a self-signed certificate, by verifying and remembering the server's identity and certificate. A changed or unverified identity can ask you to confirm trust. Check with the owner before accepting an unexpected change. Browsers use their own certificate trust rules.

## If something's wrong

- **No servers listed:** check that you accepted the invitation with the same Portico Account, or use direct sign-in for a server account.
- **Server shows as offline:** work through [Server not found](/docs/troubleshooting/server-not-found/).
- **Device waiting for approval:** ask the owner to review it under **Settings → Server → People → Devices**.
