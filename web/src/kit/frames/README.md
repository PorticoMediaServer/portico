# Screen frames in the Kit page (web)

`src/dev/families/frames.tsx` draws every frame and its states from the app's own components, with reference text
(`packages/i18n/.../dev/frames-fixtures.ts`, shared with native) and the reference artwork served by the capture
run at `http://100.120.50.24:8787/assets/`. The Kit page exists only on the Vite dev server (`import.meta.env.DEV`).

Open one case with no chrome around it:

    /dev/kit?family=frames&case=<id>&capture=true&width=<w>&height=<h>

Every case answers to the window's width, so set the window and the `width` and `height` parameters together:
1440 by 900 for the desktop, 390 by 844 for phone width (and 820 or 1180 wide for the tablet forms).

| case | Shows |
|---|---|
| `backdrop` | The window's backdrop alone |
| `browse-home` | Browse: Home, the moderate hero and two rows |
| `browse-list` | Browse: a pushed list with Back, a title and a header action |
| `browse-loading` | Browse: loading, in the loaded page's boxes |
| `title` | Title page with artwork, three round buttons, a synopsis and tabs |
| `title-missing` | Title page with no artwork, a long title, no round buttons, no synopsis |
| `title-loading` | Title page loading |
| `settings-list` | Settings list (from 600 beside a page; at phone width under the compact bar) |
| `settings-page` | A settings page with an in-section error, kept data marked old, loading and empty |
| `sheet` | A sheet (a dialog from 600 wide) |
| `sign-in` | Sign-in with an error above the field |
| `player` | The video player's chrome |
| `audio` | Audio player: music with artwork and a short title |
| `audio-missing` | Audio player: no artwork, a long title, unsupported controls dimmed, nothing next |
| `audio-book` | Audio player: an audiobook |
| `audio-loading` | Audio player: loading |
| `state` | The whole-page state |
| `elements` | Type roles, row kinds, in-section states, artwork boxes with and without a picture, cards (1800 tall) |

The native Kit page has the same sections as `frames-<case>`. Buttons, fields, switches, chips and cards in every
state are in the existing families (`controls`, `cards`, `states`, `overlays`).
