# Screen frames in the Kit page (native)

`frames.gallery.tsx` draws every frame and its states from the app's own components, with reference text
(`packages/i18n/.../dev/frames-fixtures.ts`, shared with the web) and the reference artwork served by the capture
run at `http://100.120.50.24:8787/assets/`. Development builds only (`App.tsx` loads the Kit behind `__DEV__`).

Open one section full-window, with no catalogue around it:

- Any platform: the deep link `portico://kit?gallery=<id>&window=1`.
- Apple capture builds (the gallery app, `tv.getportico.gallery`): launch argument `-PorticoKitGallery <id>`;
  `-PorticoKitGallery list` publishes the ids for this device.

The page sections (`frames-browse-*`, `frames-title*`, `frames-state`) are drawn inside the shell's own tab bar on a
phone and its rail on a tablet; the TV rail needs a signed-in session and is not drawn. The video player has three
forms: shoot `frames-player*` on a phone upright and again on its side, and on a tablet and TV.

Each section fills the window and adapts to it, so the same id is captured on a phone, a tablet, a foldable
(folded and open) and a TV.

| id | Shows |
|---|---|
| `frames-backdrop` | The window's backdrop alone |
| `frames-browse-home` | Browse: Home, the moderate hero and two rows |
| `frames-browse-list` | Browse: a pushed list with Back, a title and a header action |
| `frames-browse-loading` | Browse: loading, in the loaded page's boxes |
| `frames-title` | Title page with artwork, three round buttons, a synopsis and tabs |
| `frames-title-missing` | Title page with no artwork, a long title, no round buttons, no synopsis |
| `frames-title-loading` | Title page loading |
| `frames-settings-list` | Settings list (phone: the sheet; tablet and TV: list beside a page) |
| `frames-settings-page` | A settings page with an in-section error, kept data marked old, loading and empty |
| `frames-sheet` | A bottom sheet (side panel on wide forms and TV) |
| `frames-sign-in` | Sign-in with an error above the field (phone and tablet) |
| `frames-player`, `-paused`, `-buffering`, `-scrubbing`, `-live`, `-missing` | The video player with the kit's player parts, in each state; nothing moves between them |
| `frames-audio` | Audio player: music with artwork and a short title |
| `frames-audio-missing` | Audio player: no artwork, a long title, unsupported controls dimmed, nothing next |
| `frames-audio-book` | Audio player: an audiobook |
| `frames-audio-loading` | Audio player: loading |
| `frames-state` | The whole-page state |
| `frames-elements` | Type roles, row kinds, in-section states, artwork boxes with and without a picture, cards |
| `frames-regions-browse`, `-title`, `-settings`, `-sheet`, `-sign-in`, `-player`, `-audio`, `-state` | Each frame empty: what the frame owns drawn by the frame, what the screen supplies as outlined boxes (blue), the one flexible region in amber |

Compare `frames-audio`, `-missing`, `-book` and `-loading`, and `frames-title` with `-missing` and `-loading`: the
scrubber, the transport, the bottom row and the title page's action row sit on the same lines in each.

Buttons, fields, switches, chips and cards in every state are in the existing sections (`controls-*`, `cards-*`,
`states-*`, `overlays-*`, `tv-account-*`).

## The little things: where each shared part is drawn

Each is one shared kit part. The capture lane shoots these ids too.

| Part | Kit component | Section id |
|---|---|---|
| Context and More menus (title page, card, row, Home card) | `kit/overlays` `Menu` | `overlays-menu-member`, `overlays-menu-owner`, `overlays-menu-reduced`, `browse-menu-member`, `browse-menu-owner` |
| Sort menu | `Menu` | `browse-sort` |
| Popover | `Popover` | `overlays-popover`, `overlays-popover-modal` |
| Dialog and confirmation | `Dialog` | `overlays-dialog` |
| Side panel | `SidePanel` | `overlays-panel` |
| Bottom sheet | `BottomSheet` | `overlays-sheet`, `overlays-sheet-full`, `frames-sheet` |
| Toast and banner | `Toast`, `Banner` | `overlays-toast`, `overlays-toast-action`, `overlays-toast-artwork`, `overlays-banner` |
| Select | `Select`, `RichSelect` | `controls-select`, `controls-rich-select`, `controls-select-tv-choice` |
| Buttons in every state | `Button`, `ResumeButton` | `controls-buttons`, `controls-buttons-busy`, `controls-resume` |
| Chips, pills and badges | `Chip` | `controls-chips` |
| Tabs and segmented | `Tabs`, `Segmented` | `controls-tabs`, `controls-segmented` |
| Switches and fields | `Switch`, `TextField` | `controls-switches`, `controls-fields` |
| Profile menu | `kit/shell` `AccountMenu` | `shell-account` |
| Notifications | `NotificationItem` | `shell-notifications` |
| Cards, with badges and progress | `kit/cards` | the `cards` sections |
| Player: transport, scrubber and preview, read-outs, title, pills, Skip intro | `kit/player` | `frames-player`, `-paused`, `-buffering`, `-scrubbing`, `-live`, `-missing` (turn a phone on its side for the landscape form) |

Not in the kit yet, so not drawn: tooltips (none on native), and the player's own panels (Subtitles and Audio,
Quality, Speed, the options panel, Episodes, Up next). They are drawn by `screens/watch` (`WatchPanels.tsx`,
`EpisodePanel.tsx`, `UpNextCard.tsx`). Moving them into `kit/player` is listed in `FRAMES.md` for Stage 2.
