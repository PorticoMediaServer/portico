---
title: Complete first-run setup
description: Choose a Portico Account or a server account, and secure the first owner.
category: install
order: 5
audience: Server owners
updated: 2026-10-18
---

Open `http://127.0.0.1:32500/` on the new server. From another device on the same network, use the server's local IP address with port `32500`. A server that hasn't been set up yet shows **Welcome to Portico**.

## Name the server

Enter a **Server name** that people will recognize on their devices. Under **How will people sign in?**, choose one of the two options below.

## Use a Portico Account

Choose **Use a Portico Account** for one sign-in across Portico apps, invitations and access away from home.

1. Under **Create a recovery owner**, enter a **Username**, **Password** and **Confirm password**. This server account gets you back in if your Portico Account is unavailable.
2. Save the credentials somewhere safe and check **I saved this password somewhere safe**.
3. Select **Create recovery owner**.
4. In the Portico Account window, sign in if needed and approve the server. If you need to reopen it, select **Open Portico Account**.
5. Return to the setup page. It continues after approval, and the server requests its secure connection.

**Do this later** skips account approval. You can finish it under **Settings → Server → Remote access**. Until then, devices use plain HTTP.

Your media and the recovery owner's password stay on your server. The Portico Account handles account sign-in and server discovery.

## Keep accounts on this server

Choose **Keep accounts on this server** to manage sign-ins locally.

1. Under **Create the owner account**, enter a **Username**, **Password** and **Confirm password**.
2. Select **Create owner**.

This option doesn't create a Portico Account. You can configure remote access separately. See [Enable remote access](/docs/remote/enable-remote-access/).

## Server account password rules

The password must contain at least eight characters and use no more than 72 UTF-8 bytes. Some characters use more than one byte. Uppercase letters, numbers and symbols aren't required; choose a long, unique password.

These rules apply to the server owner and recovery owner accounts. Your Portico Account has its own account-security settings.

## After the owner is created

Portico closes first-run setup so nobody else can claim the server. Keep owner access for the person responsible for the server.

## Check the basics

Before you add media, check the server name, data folder location, local connection and system clock. See [Portico data folder](/docs/install/server-data-folder/) for its location. Incorrect system time can break authentication and signed connection checks.

## Add viewers later

Don't use the owner account as a shared household login. Finish your first library and a local playback test, then [Add a person](/docs/people/add-a-person/) for each viewer.
