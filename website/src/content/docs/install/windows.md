---
title: Install on Windows
description: Install Portico Server on a Windows computer and check that the service is running.
category: install
order: 1
audience: Server owners
updated: 2026-10-18
---

Download the Windows installer from the [Download Portico](/download/) page. Run it from an administrator account.

## Install the service

1. Open the installer and choose the application installation location.
2. Let the installer create the Windows service and add its inbound TCP `32500` firewall rule.
3. Open Portico from the Start menu, or visit `http://127.0.0.1:32500/`.
4. Follow [Complete first-run setup](/docs/install/first-run/).

The installer uses `%ProgramData%\Portico Media Server` for server data, separate from the application folder. It doesn't offer a data-folder chooser. See [Portico data folder](/docs/install/server-data-folder/).

Portico runs as a Windows service and starts with the computer. Its service name is `PorticoMediaServer`; Windows Services displays **Portico Media Server**. The default account is LocalService. You don't need to keep a terminal or desktop session open.

## Media permissions and network storage

Grant `NT SERVICE\PorticoMediaServer` read access to local media folders. Give it write access only where you enable deletion or recordings.

Windows services don't automatically see mapped drives from your desktop session. For an authenticated network share, use a stable UNC path and configure an account with access in the service's **Log On** tab. Confirm that account can reach the share after a restart.

## Manage the service

Run these commands in an elevated PowerShell window:

```powershell
Get-Service PorticoMediaServer
Restart-Service PorticoMediaServer
Stop-Service PorticoMediaServer
Start-Service PorticoMediaServer
```

The installer creates a TCP `32500` firewall rule for all Windows network profiles. Review its scope for the networks you intend to allow. If you change the listening port, update the rule.

## Confirm the installation

Restart the computer once. Check that the service, media folders and local playback work without launching the app. If the server doesn't return, check the service and server logs before reinstalling.

## Portable ZIP

The portable ZIP uses `state` beside `start-portico.cmd` unless you set an absolute `PORTICO_STATE_DIR`. It runs under your account and installs no Windows service or firewall rule.
