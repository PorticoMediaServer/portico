---
title: Move Portico to another computer
description: Transfer server data and media paths without creating a second unrelated server.
category: administration
order: 6
audience: Server owners
updated: 2026-07-12
---

Plan the move as a backup and restore operation. Keep the old server stopped while the restored copy is being verified so two computers do not publish the same server identity or write competing state.

Install a compatible Portico version on the destination, restore the Portico data using the supported restore process, and make the media available at the expected paths. When paths must change, update them deliberately and scan after confirming access.

Check service-account permissions, storage mounts, firewall rules, local address, remote-access state, and certificate paths on the new system.

Test local playback and a normal viewer before publishing the new remote route. Keep the old system and backup intact until the new server has been verified.
