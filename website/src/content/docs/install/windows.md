---
title: Install on Windows
description: Install Portico Server on a Windows computer and confirm that the service is running.
category: install
order: 1
audience: Server owners
updated: 2026-07-12
---

Download the Windows installer from the [Downloads page](/download/) and run it from an administrator account.

## Install the service

1. Open the installer.
2. Review the installation location and Portico data-folder location.
3. Allow the installer to create the Portico Server service.
4. Approve the local firewall rule when Windows asks.
5. Open Portico and complete first-run setup.

Running as a Windows service lets Portico start with the computer without keeping a terminal or desktop session open.

## Media on network storage

Windows services do not automatically inherit every mapped drive shown in File Explorer. Use a stable network path and make sure the service account can reach it after a restart.

## Confirm the installation

Restart the computer once. Portico should become available again without manually launching it. If it does not, check the Portico Server service and review the server logs before reinstalling.
