---
title: Install on macOS
description: Install Portico Server on an Apple silicon Mac and give it reliable access to your media folders.
category: install
order: 2
audience: Server owners
updated: 2026-10-18
---

Download the macOS package for Apple silicon (ARM64) Macs from the [Download Portico](/download/) page. Intel Macs aren't supported by this package. If you downloaded a DMG, open it and run the `.pkg` inside. If you downloaded the `.pkg` directly, run it. The installer places the app at `/Applications/Portico Media Server.app` and installs the server service.

## First launch

Open the installed app to open the server console in your browser, or visit `http://127.0.0.1:32500/`. If macOS asks you to confirm the download, follow its prompt. Follow [Complete first-run setup](/docs/install/first-run/).

## Automatic startup

The installer registers a system launchd service named `tv.getportico.server.service`. Its definition is `/Library/LaunchDaemons/tv.getportico.server.service.plist`. It runs as the hidden `_portico` account and starts at boot, before anyone signs in.

Opening the app opens the console. You don't need to add the app to Login Items to start the server.

To inspect the service, run:

```sh
sudo launchctl print system/tv.getportico.server.service
```

## Data and folder access

The default data folder is `/Library/Application Support/Portico Media Server`. Back it up separately from your media. See [Portico data folder](/docs/install/server-data-folder/).

Give the `_portico` service account read access to every media folder. If a folder is on an external disk or network share, make sure it mounts consistently and remains readable by that account after a restart. Access from your desktop account alone doesn't establish service access.

## Keep the Mac available

A Mac that's asleep can't serve media. Check its energy settings and how its external disks behave before other people rely on it.

## Check after a restart

Restart the Mac. Confirm the launchd service is running, its media folders are available and a local client can connect without you opening the app.
