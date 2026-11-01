---
title: Home automation
description: See what’s playing and pause, resume, seek or stop it.
kind: tool
order: 14
---

Home automation hubs can show what’s playing on Portico and control sessions the account is allowed to control, for example to pause when the doorbell rings.

## Prepare an app password

Turn on **Integrations compatibility** in **Settings → Server → Integrations**. Create a named app password for the account and profile the hub should use, with `read` and `playback` scopes. Seeing or managing everyone’s sessions needs administrator authority and a wider scope, such as `admin`.

## Set up the hub

1. Add the hub’s media server integration that signs in with a username and password.
2. Enter Portico’s address, the username and the app password.
3. Finish setup and check which sessions the account can see.

Use an address the hub can reach, not its own loopback address. Never use a real account password or PIN.

## What it can do

Pause, resume, seek and stop work on sessions the account and profile may control. Anything else returns an error. Volume, mute, starting playback on another device and streaming aren’t part of this connection. Try pausing and resuming one session before building an automation on it.
