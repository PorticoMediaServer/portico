---
title: Complete first-run setup
description: Choose a Portico Account or a local server account and secure the first owner.
category: install
order: 5
audience: Server owners
updated: 2026-07-12
---

Open Portico from a device on the same network as the new server. A server that has not been configured presents the owner setup flow.

## Choose an owner identity

Use an identity controlled by the person responsible for the server. The first-run screen provides two paths.

### Portico Account

Choose **Portico Account** when you want remote access, invitations, and one identity across Portico apps. Select **Continue to Portico** and approve the short-lived claim page. Your media and server credentials remain on your server; the Portico Account handles sign-in and server discovery.

### This Server only

Choose **This Server only** when you want the owner identity to exist only on this server. Enter the requested username, display name, email, and a unique password with at least eight characters, uppercase and lowercase letters, and a number or special character. Confirm that the credentials are managed by this server, then select **Create This Server owner**.

This path does not create a Portico Account. You can configure remote access separately later.

The completed identity becomes the server owner. Portico then closes first-run setup so another visitor cannot claim that role.

## Check the basics

Before adding media, confirm the server name, data-folder location, local connection, and time settings. Incorrect system time can cause authentication and signed connection checks to fail.

## Add viewers later

Do not use the owner account as a shared household login. Finish the first library and local playback test, then add a separate account for each person.
