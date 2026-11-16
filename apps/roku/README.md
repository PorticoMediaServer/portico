# Portico for Roku

The Roku app: plain BrightScript and SceneGraph, drawn for a 1920x1080 screen. It follows the TV
mock-ups and the same behaviour as the other Portico apps; `packages/client-core` and the API
contract are the reference, since none of that code can run on a Roku.

- `manifest`, `source/`, `components/`, `images/`, `fonts/` are the app. Zip those and sideload the
  zip through a Roku's developer page to run it.
- `source/Theme.brs` holds every colour, type size and shared measurement; `source/Present.brs`
  holds how a title is worded and pictured. `components/kit/` holds the shared pieces every screen
  is built from (backdrop, rail, shelf, card, hero, buttons, notice).
- `components/tasks/ApiTask` makes every request to the server.
- `images/` is generated: `python3 apps/roku/tools/make-assets.py` draws the backdrop, scrims, focus
  ring, masks, generated covers and icons from the shared design tokens. Roku cannot blur, draw a
  gradient or round a picture's corners, so those are pictures.
- `tools/` also holds the compile check that runs on every change: `apps/roku/tools/check.sh`.

There is no Roku simulator. What the app looks like and how focus moves is checked on a television.
