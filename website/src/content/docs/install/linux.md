---
title: Install on Linux
description: Install Portico Server as a managed service on a supported Linux system.
category: install
order: 3
audience: Server owners
updated: 2026-07-12
---

Download the Linux package for the architecture of your server. Portico publishes DEB and RPM packages plus a distribution-neutral `.tar.gz` bundle for both x64 and ARM64. Use a normal administrative account and keep Portico's executable, configuration, data, and media paths separate.

Use the DEB package on Debian or Ubuntu, the RPM package on Fedora or a related distribution, or unpack the `.tar.gz` bundle when neither package format fits your system. The archive contains Portico Media Server, its web application, ffmpeg, ffprobe, licences, and third-party notices.

## Service account

Run Portico under a dedicated service account. Grant that account read access to library folders and write access only to the Portico data folder and other paths Portico must manage.

## Start Portico at boot

Install the supplied service definition for your distribution, enable it, and start the service. Do not use a terminal session as the normal production process.

## Firewall

Allow the Portico service from the networks that should reach it. Keep database files, administrative operating-system services, and unrelated ports private.

## Verify after restart

Restart the host and confirm that the Portico service is active, its media mounts are present, and a local client can connect.
