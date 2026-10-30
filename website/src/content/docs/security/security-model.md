---
title: Portico security model
description: Understand the separate server and cloud authorities and where access is enforced.
category: security
order: 1
audience: Everyone
updated: 2026-07-12
---

Your Portico Server keeps control of your media and decides who can use it. Portico Cloud handles Portico Accounts and helps an approved app find the right server. It cannot play, share, or administer media that the server has not authorized.

## Technical details

Portico Server is the authority for media, libraries, playback, local users, permissions, settings, recordings, downloads, and administration. Every server request is authorized there, including one made through a Portico Account.

Portico Cloud is authoritative for Portico Accounts, hosted security settings, server ownership and membership, invitations, and signed route and policy documents. It cannot grant more media access than the server currently allows.

Server certificate private keys remain on the server. Cloud route and policy signing keys remain in the hosted control plane. Clients verify the documents and server identity used to establish a direct route.
