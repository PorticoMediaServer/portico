---
title: Request tools
description: Let people ask for films and shows from a separate request app.
kind: tool
order: 12
---

Request tools show what’s already on your server and let people ask for more. They connect with a dedicated app password, never anyone’s real password.

## Prepare an app password

Turn on **Integrations compatibility** in **Settings → Server → Integrations**. Create a named app password tied to a real owner or administrator account and profile. Choose only the scopes the features you use need.

Sign in with the username Portico shows and that app password. Account passwords, profile passwords, PINs and API keys don’t work here. The owner can Reveal and Copy the app password later. Revoking it also revokes every token made from it.

## Set up the tool

1. In the tool’s setup, add a media server that signs in with a username and password.
2. Enter Portico’s address, the username and the app password.
3. Choose the libraries it may see, test the connection and finish setup.

The tool can create and read a key within the app password’s grant. It can’t read another grant’s keys or gain wider scopes. Where the tool offers it, you can give it a key you made yourself instead.

## People using the tool

Give people the tool’s own local accounts. Never ask them for their Portico passwords. Quick Connect isn’t offered.

Check that libraries show as available and that a request works from a local account before inviting anyone. This covers requests and your catalogue; it isn’t a player and doesn’t stream.
