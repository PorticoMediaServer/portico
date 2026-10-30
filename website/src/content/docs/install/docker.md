---
title: Install with Docker
description: Plan a persistent Portico container with explicit media and data mounts.
category: install
order: 4
audience: Server owners
updated: 2026-07-12
---

A containerized Portico Server needs persistent storage, stable media mounts, and a clear network path to its clients.

## Required mounts

- Mount the Portico data directory as a persistent writable volume.
- Mount media folders read-only unless Portico needs an explicitly enabled file-management action.
- Keep temporary transcode storage separate when the host has a faster or disposable location for it.

The container user must be able to read the media mounts and write to the Portico data and temporary paths. File permissions on the host still apply inside the container.

## Networking

Publish only the Portico listener required by your deployment. Local discovery may need host-specific network configuration because broadcast traffic does not cross every container network automatically.

## Updates

Back up Portico data before changing images. Pull the new image, recreate the container with the same persistent mounts, and confirm the server reports healthy before removing the prior image.
