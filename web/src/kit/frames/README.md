# Screen frames in the Kit page (web)

`src/dev/families/frames.tsx` draws every frame and its states from the app's own components, with reference text
(`packages/i18n/.../dev/frames-fixtures.ts`, shared with native) and the reference artwork served by the capture
run at `http://100.120.50.24:8787/assets/`. The Kit page exists only on the Vite dev server (`import.meta.env.DEV`).

Open one case with no chrome around it:

    /dev/kit?family=frames&case=<id>&capture=true&width=<w>&height=<h>

Every case answers to the window's width, so set the window and the `width` and `height` parameters together:
1440 by 900 for the desktop, 390 by 844 for phone width (and 820 or 1180 wide for the tablet forms).

The page cases (`frames-library` `browse-*`, `title*`, `settings-list`, `state`) are drawn inside the shell's own top bar
from 1024 wide and its wordmark bar and tab bar at phone width; the rail between 600 and 1023 is not drawn. The video
player has three forms: shoot `player*` at 1440 by 900, 390 by 844 and 844 by 390.

| case | Shows |
|---|---|
| `backdrop` | The window's backdrop alone |
| `frames-library` family: `browse-home` | Home: the hero with its pager thumbnails, Continue Watching, New on your server (New badges, subtitle, See all), On now, Continue Listening, People on this server are watching |
| `frames-library`: `browse-list`, `-discover`, `-music`, `-filters`, `-collections`, `-see-all`, `-empty`, `-menu`, `-loading`, `regions-browse` | The library: the grid with its rail; Discover's rows; Music's tabs; Filters open; Collections; See all; no titles match; the More menu (owner); loading in the loaded page's boxes; the regions outlined |
| `title` | Title page with artwork, three round buttons, a synopsis and tabs |
| `title-missing` | Title page with no artwork, a long title, no round buttons, no synopsis |
| `title-loading` | Title page loading |
| `settings-list` | Settings list (from 600 beside a page; at phone width under the compact bar) |
| `settings-page` | A settings page with an in-section error, kept data marked old, loading and empty |
| `sheet` | A sheet (a dialog from 600 wide) |
| `sign-in` | Sign-in with an error above the field |
| (family `frames-player`) `player`, `-paused`, `-buffering`, `-scrubbing`, `-missing`, `-skip`, `-undo` | The video player with the kit's player parts, in each state; nothing moves between them. Shoot at 1440 by 900, 390 by 844 and 844 by 390 |
| (family `frames-player`) `player-options`, `-subtitles`, `-audio`, `-quality`, `-speed`, `-episodes`, `-up-next`, `-up-next-credits`, `-up-next-countdown`, `-end-card`, `-live-controls`, `-live-guide` | The player's panels, cards and live chrome |
| (family `frames-player`) `audio`, `-lyrics`, `-lyrics-choose`, `-save`, `-queue`, `-missing`, `-book`, `-loading` | The audio player with its queue panel, Lyrics, Save as playlist, an audiobook, no artwork, loading |
| `state` | The whole-page state |
| `elements` | Type roles, row kinds, in-section states, artwork boxes with and without a picture, cards (1800 tall) |
| `regions-title`, `-settings`, `-sheet`, `-sign-in`, `-state` | Each frame empty: what the frame owns drawn by the frame, what the screen supplies as outlined boxes (blue), the one flexible region in amber |

The native Kit page has the same sections as `frames-<case>`. Buttons, fields, switches, chips and cards in every
state are in the existing families (`controls`, `cards`, `states`, `overlays`).

## The little things: where each shared part is drawn

Each is one shared kit part. The capture lane shoots these ids too (`family` `case`).

| Part | Kit component | Case |
|---|---|---|
| Context and More menus (title page, card, row, Home card) | `kit/overlays` `Menu` | `overlays` `menu`, `menu-long`, `menu-states`, `phone-menu` |
| Sort and library menus | `Menu` | `browse` `sort-menu`, `library-switcher` |
| Popover | `Popover` | `overlays` `popover`, `modal-popover` |
| Dialog and confirmation | `Dialog` | `overlays` `dialog`, `phone-dialog`, `recording-dialog` |
| Side panel | `SidePanel` | `overlays` `side-panel`, `side-panel-owner` |
| Bottom sheet | `BottomSheet` | `overlays` `sheet-half`, `sheet-full`; `frames` `sheet` |
| Toast and banner | `Toast`, `Banner` | `overlays` `toast`, `toast-action`, `phone-toast`, `banner`, `phone-banner` |
| Tooltip | `Tooltip` | `overlays` `tooltip`, `tooltip-detail` |
| Select and dropdown | `Select`, `RichSelect` | `controls` `select`, `select-open`, `rich-select`, `rich-select-open`, `rich-select-phone` |
| Buttons in every state | `Button`, `ResumeButton` | `controls` `primary`, `glass`, `round`, `icon`, `text`, `resume`, `disabled`, `loading`, `interactions` |
| Chips, pills and badges | `Chip` | `controls` `chip`, `chip-selected`, `chip-count`, `chip-applied`, `chip-pill`, `chip-facet` |
| Tabs and segmented | `Tabs`, `Segmented` | `controls` `tabs`, `tabs-phone`, `tabs-long`, `segmented` |
| Switches and fields | `Switch`, `TextField` | `controls` `switch-on`, `switch-off`, `field`, `field-focus`, `field-error`, `search` |
| Profile menu | `kit/shell` `AccountMenu` | `shell` `account-menu`, `account-menu-phone`, `profile-current` |
| Notifications | `NotificationItem` | `shell` `notification-read`, `notification-archived` |
| Cards, with badges and progress | `kit/cards` | `cards` `reference` |
| Focus and hover | the controls' own | `controls` `interactions` |
| Player: transport, scrubber and preview, read-outs, title, pills, Skip intro | `kit/player`, `kit/media` `SeekLine` | `frames` `player`, `player-paused`, `player-buffering`, `player-scrubbing`, `player-live`, `player-missing`, `player-landscape` |
| Player as built today | `screens/watch` | `watch` `desktop-player-f1` |

| Player panels: Playback options, Subtitles, Audio, Quality, Speed | `kit/player` `PlayerOptions` | `frames` `player-options`, `player-subtitles`, `player-audio`, `player-quality`, `player-speed` (at 1440 the one panel shows every list; the sub-lists are the phone forms) |
| Player: Episodes drawer | `kit/player` `PlayerEpisodes` | `frames` `player-episodes` |
| Player: Up next card | `kit/player` `PlayerUpNext` | `frames` `player-up-next` |
| Player: Live TV chrome (Record, Guide, channel down and up, Go to live), and with the short guide open | `kit/player` `PlayerLive` | `frames` `player-live-controls`, `player-live-guide` |

Shoot the panel and live cases like the player: 1440 by 900, 390 by 844 and 844 by 390.

Not in the kit yet, so not drawn here: Chapters, the queue, Playback info and the end card (`screens/watch`). They are
listed in `FRAMES.md` for Stage 2.

Not sections to shoot: `browse` `sort-menu` and `library-switcher` and `cards` `reference` are measurement cases that need the gallery inputs manifest (`web/scripts/config.mjs`); without it they say "gallery inputs unavailable" or stay empty. The `overlays` `toast` case is 1440 wide; shoot `phone-toast` at 390. Shoot with scrollbars hidden.

The `browse` and `layout` families read their measurement inputs (server responses and mock-up pages) from a manifest outside the repository, named by `PORTICO_GALLERY_INPUT_MANIFEST` when the dev server starts (`web/scripts/config.mjs`, guarded by `test/gallery-inputs.test.ts`). They are not in the repository on purpose.
