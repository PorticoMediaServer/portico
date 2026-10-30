---
title: Connection troubleshooting basics
description: Narrow a connection problem to the app, account, local network, remote route, or server.
category: troubleshooting
order: 1
audience: Everyone
updated: 2026-07-12
---

Start with the smallest path. Confirm Portico Server is running, then open its bundled web app from another device on the same network.

If local access works but remote access does not, open **Settings → Connectivity**. Copy the route-summary message, claim state, public endpoint, certificate state, and last reachability result. Continue with [Read connection status](/docs/remote/read-connection-status/) and [Enable remote access](/docs/remote/enable-remote-access/).

If one account fails while another works, open **Settings → People & Access** and review that person's status, role, and assigned libraries. If one device fails while another works under the same account, update the app, confirm the device clock, and sign in again on that device.

Before restarting anything, record the exact error and time. Then open **Settings → Diagnostics**, filter the Server Console around that time, and copy the visible events. Include the Portico version and API version from the **Release** section.

- Local works, remote fails: continue with [Remote access](/docs/remote/enable-remote-access/).
- One device fails: review [How clients differ](/docs/clients/client-differences/).
- Sign-in fails everywhere: use [Sign-in problems](/docs/troubleshooting/sign-in-problems/).
