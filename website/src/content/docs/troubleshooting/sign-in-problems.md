---
title: Sign-in problems
description: Check the account, server membership, password, device time and session.
category: troubleshooting
order: 6
audience: Everyone
updated: 2026-10-18
---

## Use the right account

A Portico Account and a server account have different credentials. Use Portico Account sign-in for the account that accepted the invitation. Use that server's address, username and password for a server account.

If a Portico Account offers Google or Apple sign-in, use a method linked to the same account. Don't create another account to solve a missing membership.

## Recover the right password

For a Portico Account, use **Forgot password?** on the account sign-in form. If you sign in only through Google or Apple, recover access with that provider.

For a server account, ask the owner to reset that person's password under **Settings → Server → People**. Don't share or reset the owner's credential to sign in as a viewer.

If **Two-step verification** is on, enter a current authenticator code or an unused recovery code. Keep recovery codes before replacing the authenticator device. Portico Account security is under **Open your account → Security** from **Settings → Account → Profile & account**; server account security is under **Settings → Account → Profile & account → Sign-in**.

## Check the device date and time

Authenticator codes, sessions and signed connection documents depend on the device clock. Enable automatic date and time on the client and server, then try again.

## Check approval and profile selection

A successful password check can still leave a device waiting for owner approval, a required password change or a profile/PIN selection. Follow the displayed step. Ask the owner to review pending devices under **Settings → Server → People**.

## If you sign in but the server is missing

Check server membership and the connection before changing the password. Follow [Server not found](/docs/troubleshooting/server-not-found/).
