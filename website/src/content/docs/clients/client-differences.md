---
title: Why controls differ between apps
description: Which Portico behaviour is the same in every app, and which controls depend on the device.
category: clients
order: 5
audience: Everyone
updated: 2026-10-18
---

Portico apps use the same account permissions, libraries, My List and watch progress. Their layouts and playback controls fit the device you're using.

## What depends on the device

- Phones and tablets use touch controls, background audio and mobile data preferences.
- TV apps use the directional buttons on your remote and show which control is selected.
- Portico Web uses the browser's media player and security rules.
- Native apps use the device's secure storage and playback integration.

Use your device or TV's volume controls in native apps. An on-screen system volume slider, where shown, changes the device volume. The web player has its own volume control.

Apple apps allow plain HTTP only to local-network addresses; remote public addresses need HTTPS. Other platforms allow HTTP from anywhere. Portico apps can trust the owner's own certificate after checking the server's identity. Browsers apply their own trust rules.

## When a control is missing

A control can be absent because your account lacks permission, the server hasn't enabled the feature or your device doesn't support it. File formats, audio, subtitles and HDR support can also change how a title plays.

Check the page for your app and the server's playback decision before assuming a control has moved or stopped working.
