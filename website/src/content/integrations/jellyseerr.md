---
title: Jellyseerr
description: Connect the request tool with a dedicated app password.
kind: tool
order: 14
---

## Prepare an app password

Enable **Integrations compatibility** in **Settings → Server → Integrations**. Create a named integration app password bound to a genuine owner or administrator account and profile. Choose only the scopes needed by the features you configure.

Use the username shown in Portico with that app password. Actual account passwords, profile passwords, PINs and API keys are not compatibility passwords. The owner can Reveal and Copy the app password later. Revoking it also revokes its derived integration tokens.

## Configure the administrator wizard

1. In Jellyseerr’s initial administrator wizard, choose its **Jellyfin** connection.
2. Enter Portico’s URL, the bound username and the dedicated app password.
3. Choose the permitted libraries, test the connection and finish setup.

The wizard can create and retrieve a key within the app password’s grant. It cannot retrieve another grant’s keys or gain broader scopes. Pre-issued-key setup is an alternative where your version offers it.

## Set up end users

Use **Jellyseerr’s local accounts** for end users. Do not ask them to enter their Portico account passwords. Quick Connect is not provided.

Check library availability and a request using a local account before inviting others. Tool compatibility covers request and catalog integration; it does not provide a player or streaming API.
