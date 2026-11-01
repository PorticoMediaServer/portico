---
title: Data and retention
description: Which information Portico Server and Portico Services each store, and how long it's kept.
category: security
order: 4
audience: Everyone
updated: 2026-10-18
---

## What Portico Server stores

Portico Server stores what it needs to run your media server:

- Local media records
- Server accounts, profile pictures, account mappings and permissions
- Settings
- Playback state
- Recordings
- Operational data

## What Portico Services store

Portico Services store:

- Portico Account and hosted security information
- Server ownership and membership
- Invitations
- Server identity public keys, connection addresses and the limited operational information needed to coordinate signed routes and protect the service

These connection addresses can include local-network addresses. They identify where a server can be reached, not its media sources or filesystem paths.

## How long it's kept

Retention depends on the kind of record. Active accounts, memberships and credentials remain while they're needed; temporary setup, sign-in and recovery records expire and are cleaned up separately.

Retention periods for Portico Services are in [Privacy → Retention](/privacy/#retention). Server retention is managed separately by the server owner.

On your server, owners control log-file retention under **Settings → Server → Storage & backups → What Portico keeps**. Deleting an app doesn't delete its account, server activity or API credentials. Revoke access and review stored data separately.

Database backups preserve the records present when they were made. Restoring a backup can also restore credentials that were revoked later. Keep backups private and review access after a restore.

## What never moves to Portico Services

Private media files, playback history, My List and server certificate private keys don't move to Portico Services. Owner-configured webhooks can send event details, including media titles and playback activity, to a destination chosen by the owner. Those messages go directly from the server to that destination.
