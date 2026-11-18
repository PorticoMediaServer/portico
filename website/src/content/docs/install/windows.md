---
title: Install on Windows
description: Install Portico Server on a Windows computer and check that it is running.
category: install
order: 1
audience: Server owners
updated: 2026-11-18
---

Download the Windows installer from the [Download Portico](/download/) page. Run it signed in as the person Portico should run as. It asks for administrator rights to install the program and its firewall rule.

## Install

1. Open the installer and choose the application installation location.
2. Let the installer add its inbound TCP `32500` firewall rule. Portico starts when the installer finishes.
3. Open Portico from the Start menu, or visit `http://127.0.0.1:32500/`.
4. Follow [Complete first-run setup](/docs/install/first-run/).

Portico runs as you, the way Plex does: it starts when you sign in to Windows and runs while you're signed in, with no window of its own. It keeps its data in `%LOCALAPPDATA%\Portico`, separate from the application folder. See [Portico data folder](/docs/install/server-data-folder/).

## Media folders and network storage

Portico reads what your Windows account can read: your own folders, mapped drives and network shares you've signed in to. Nothing needs extra permissions.

## Stop and start Portico

- To stop it, end `portico-server.exe` in Task Manager. Signing out also stops it.
- To start it again, choose **Start Portico Media Server** in the Start menu, or sign in again.
- To stop it starting at sign-in, turn off **Portico Media Server** in **Settings > Apps > Startup**.

The installer creates a TCP `32500` firewall rule for all Windows network profiles. Review its scope for the networks you intend to allow. If you change the listening port, update the rule.

## Confirm the installation

Restart the computer and sign in. Check that Portico, your media folders and local playback work. If the server doesn't answer, look in `%LOCALAPPDATA%\Portico\logs` before reinstalling.

## Portable ZIP

The portable ZIP runs from `start-portico.cmd` under your account, in that window, with the same data folder unless you set an absolute `PORTICO_DATA_DIR`. It doesn't start at sign-in or add a firewall rule.
