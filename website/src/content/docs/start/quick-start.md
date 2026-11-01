---
title: Quick start
description: Install Portico Server, create the owner account, add a library and test playback.
category: start
order: 2
audience: Server owners
updated: 2026-10-18
---

This is the shortest path to a working Portico Server. Start on your home network and check local playback before you configure access from outside it.

## 1. Install Portico Server

Download the package for the computer that stores your media or can reach it. Then follow the guide for your system: [Install on Windows](/docs/install/windows/), [Install on macOS](/docs/install/macos/), [Install on Linux](/docs/install/linux/) or [Install with Docker](/docs/install/docker/).

Open `http://127.0.0.1:32500/` on the server. From another device on the same network, replace `127.0.0.1` with the server's local IP address.

## 2. Choose how to sign in

Enter a **Server name**, then choose how people will sign in:

1. **Use a Portico Account** gives you one sign-in across Portico apps and server invitations. Create a local recovery owner with a **Username**, **Password** and **Confirm password**. Check **I saved this password somewhere safe**, then select **Create recovery owner**. Approve the server in the Portico Account window. Portico then sets up its secure connection.
2. **Keep accounts on this server** keeps sign-in on your server. Enter a **Username**, **Password** and **Confirm password**, then select **Create owner**.

The local password must have at least eight characters and fit within 72 UTF-8 bytes. Choose a unique password and keep recovery credentials safe. See [Complete first-run setup](/docs/install/first-run/) for the full steps.

## 3. Add one library

1. Open **Settings → Server → Libraries**.
2. Select **Add library**.
3. Enter a **Name** and choose **Movies** or **TV shows** under **Type**.
4. Set **Folder on the server** to a small, well-organized media folder.
5. Choose the metadata source and language, where offered, then select **Add library**.

You can add more folders and review scanning options afterwards. See [Create a library](/docs/media/create-a-library/).

## 4. Wait for the first scan

Portico reads the folder, adds the media it recognizes and gathers available artwork and details. When the scan finishes, open the library and check a few items.

## 5. Test playback locally

Play one item from a browser or another device on the same network. Check that:

- playback starts,
- progress is saved,
- you can resume the item.

If all three work, the basic setup is done.

## Next steps

[Add a person](/docs/people/add-a-person/) for each viewer. If you need access from outside your home, follow [Enable remote access](/docs/remote/enable-remote-access/).
