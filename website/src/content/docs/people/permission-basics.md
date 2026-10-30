---
title: Permission basics
description: Separate viewing, downloading, administration, and destructive actions.
category: people
order: 3
audience: Server owners
updated: 2026-07-12
---

Permissions should match the action a person actually needs. Being allowed to play a library does not require permission to edit metadata, delete files, manage other people, or change the server.

Pay particular attention to actions that affect the filesystem or other users. Deletion, file management, library administration, account administration, remote-access configuration, diagnostics export, and backup operations should remain restricted.

Portico Server is the final authorization point for every library and administrative request. Hiding a button is useful presentation, but it is not the security boundary.

Review permissions after changing a person's responsibilities and when enabling a new feature such as downloads or Live TV.
