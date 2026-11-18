# Screen frames (native)

The brief for building a screen. The rules are in the design package (`design/ui-v1`: `system-rules.html`,
`system-frames.html`, `notes/system-rules.md`); their numbers are data in `packages/design/src/frames.ts`; the
frames here apply them. The web has the same frames with the same names in `web/src/kit/frames`.

A screen picks one frame and fills its slots. It never draws a backdrop, a header, page padding, a gutter, the
space between sections, or a page-level loading or error state: the frame does. Each frame has fixed regions,
which never move, and one flexible region, the only place that may change when content is missing or added.

## Which frame

| Screen | Frame | Flexible region |
|---|---|---|
| Home | `BrowseFrame` with `hero={<HomeHero/>}` | the rows |
| A library, Live TV, Saved, Search results, See all, any list or grid | `BrowseFrame` with `header` | the rows or grid |
| A film, show, season, person, album, artist, book, collection | `TitleFrame` | the synopsis and the tab panel |
| Settings (the list) | `SettingsListFrame` | the groups |
| One settings or owner page, a form | `SettingsPageFrame` | the sections |
| A menu, picker or short task | `SheetFrame kind="bottom"` | the rows |
| An editor that takes the whole phone | `SheetFrame kind="full"` | its page |
| Welcome, sign in, local server, codes, profiles | `SignInFrame` (TV: the existing `tv/auth/TVAuthLayout`) | the form |
| The video player | `PlayerChromeFrame` | none |
| Now playing (music, audiobooks) | `AudioPlayerFrame` | phone: the gap under the artwork; wide and TV: Up next |
| The whole page can't load | the frame's own `state`, or `PageStateFrame` | the sentence |

## Props

- `BrowseFrame`: `header: {title, back?, trailing?}` or `hero` (Home; `null` while there is nothing to feature);
  `state?: {kind: 'loading'} | PageStateProps`; `children` are kit `Row`s or a grid. A `Row` draws its own heading;
  `Section` is only for a block that is not a row of cards, and a `Row` never goes inside one. The frame spaces them.
- `HomeHero`: the kit `Hero`'s data and render props only (`candidates`, `renderArtwork`, `renderActions`, ...).
  Its sizes are rule 14's.
- `TitleFrame`: `art` (the kit `AmbientArt treatment="title"`), `back`, `topAction?`, `title`, `metadata?`,
  `primary`, `actions?` (round buttons; one the title doesn't support stays with `disabled`), `synopsis?`, `tabs?`,
  `children` (the tab panel, built from `Section`s), `state?`.
- `SettingsListFrame`: `bar`, `open`, `onOpenChange`, `list`, `page?`.
- `SettingsPageFrame`: `title`, `back?`, `action?`, `intro?`, `children` (`SectionCard`s), `state?`.
- `SheetFrame`: `open`, `onOpenChange`, `children`, and `kind: 'bottom'` with `title`, `footer?`, or
  `kind: 'full'` with `bar?`.
- `SignInFrame`: `back?`, `title`, `lede?`, `error?`, `children`, `terms?`.
- `PlayerChromeFrame`: `picture`, `chromeVisible`, `top`, `scrubber`, `transport`, `leading?`, `trailing?`, `prompts?`.
- `AudioPlayerFrame`: `close`, `more`, `context?`, `artwork?`, `icon`, `title`, `byline?`, `detail?`, `scrubber`,
  `transport` (five slots), `nextLine?`, `options` (three or four slots), `upNext?`, `loading?`.
- `PageStateFrame`: `icon`, `title`, `body?`, `primaryAction?`, `secondaryAction?`, `retry?`, `details?`.

Parts for the flexible regions: `Section` (a heading and a block that is not a row of cards), `SectionCard` (a group label and one raised
card of `SettingsRow`s; `stale` marks kept data as out of date), `StateRow` (loading, empty or error inside one
section, as one row of that section's card), `ArtBox` (an artwork slot that always keeps its box).

## Where stock parts are not enough: the players

A player needs more than buttons, rows and cards. Its own parts are a small named set in `kit/player`, the same
names in both apps, and the player frames place them:

| Part | What it is |
|---|---|
| `PlayerControl` | One round transport or tool button (the kit Button's `transport` size): `primary` for Play or Pause, `busy` while buffering, `disabled` in place when unsupported |
| `PlayerScrubBar` | The scrubber as drawn: track, buffered, played, knob, marked stretches. The screen's timeline owns gestures and keys |
| `PlayerScrubPreview` | The thumbnail and its time over the scrubber while scrubbing |
| `PlayerTimes` | The read-outs under the scrubber: position, when it ends, time left, or Live |
| `PlayerTitle` | The title block at the top |
| `PlayerPill` | An options entry point that shows its value (quality, speed) |
| `PlayerPrompt` | Skip intro and Up next, in the frame's prompt corner |

`PlayerChromeFrame` takes them as `top`, `scrubber`, `transport`, `leading`, `trailing` and `prompts`: the transport is
in the middle of the picture on a phone and in the middle of the bottom bar on a tablet and TV.
`AudioPlayerFrame` draws its five transport slots with `PlayerControl` and takes the scrubber and read-outs as
`scrubber`. Their sizes, scrims and type are `frameSpec.player`, `componentSpec.transport` and the `player*` type roles.

The built video player (`screens/watch/WatchChrome.tsx`, `WatchTimeline.tsx`, `TrickplayStrip.tsx`, `UpNextCard.tsx`)
still draws its own versions; they are bound to the playback runtime. Stage 2 moves that screen onto the frame and
these parts and deletes its private drawing. Apply the same test to any other screen that needs more than stock
parts (the reader, the guide grid, the equaliser): name the parts, put them in the kit, draw them in the Kit page.

## What a screen may do

- Choose the frame and pass it data, text and callbacks.
- Fill the flexible region with kit components: `Row`, `WindowedGrid`, the cards, `Section`, `SectionCard`,
  `SettingsRow`, `StateRow`, `ArtBox`, `Button`, `TextField`, `Switch`, `Tabs`.
- Leave out a row or section that has nothing (whole, never a gap), and pass `disabled` for a control the item
  doesn't support.

## What a screen may not do

- Draw a backdrop, glow, gradient band or page background. The window draws the one backdrop.
- Draw a header, a Back or a page title outside the frame's `header`, `back` and `title`.
- Set page padding, a gutter, a top or bottom inset, or the space between sections.
- Draw a whole-page error or a large title inside a section (use `StateRow`), or more than one page state.
- Move, hide or resize a fixed region, or collapse a reserved slot (artwork, title lines, the metadata line, the
  Next line).
- Cut a page or section title, or cut text by a box's height.
- Type a colour, a font size or weight, or a spacing number. Colours come from `theme.colors`, text from
  `theme.type` (rule 6's roles: `pageTitle`, `barTitle`, `rowHeading`, `groupLabel`, `listLabel`, `rowLabel`,
  `rowHelp`, `intro`, `cardTitle`, `cardSubtitle`, `tab`), spacing from the frame and `theme.space`.

## What the raw-value guard should forbid in `screens/` (Stage 2)

- `StandardBackdrop`, `ColourBackdrop`, `SettingsBackdrop`, `PaletteWash` and `backgroundColor` on a screen's root.
- Hex and `rgb()` colours.
- Numeric `fontSize`, `fontWeight`, `lineHeight`, `letterSpacing`; `shellType(` and `browseType(` with numbers.
- Numeric `padding*`, `margin*`, `gap`, `top`, `left`, `right`, `bottom` (allow 0 and values read from the theme).
- `useSafeAreaInsets` (the frames own the safe areas).
- `numberOfLines` on a page or section title.
- Importing `kit/frames/metrics`: only frames and kit parts read it.

## Where the pages and the code differ (for the lead)

- The rule 12 placeholder (icon on a raised fill) is `ArtPlaceholder`, used by `ArtBox` and the audio player. The
  kit cards still draw the older generated cover for a titled item with no picture (`missingArt`). Both keep the
  box full. Which one cards should use is a design ruling, not made here.
- The moderate hero is applied through `HomeHero` and `TitleFrame`. `componentSpec.hero` still carries the first
  mock-ups' sizes for the screens that have not moved; delete them when the last one has.
- `theme.gutter` is now rule 4's on every form (a tablet under 1024 wide went from 20 to 28). Screens that type
  20 by hand on a tablet are off by 8 until they move.
- Older type names that rule 6's roles replace, to delete as screens move: `title` and `display` used as a page
  title, `shellType(` and `browseType(` with numbers, the settings rows' typed sizes.
- Home's wordmark bar and the tab bar, sidebar and rail stay with the shell (`shell/NativeShell.tsx`); the frame
  keeps the wordmark bar's room. On Home the shell passes its own bottom inset; fold that into the shell's page
  wrapper so no screen receives one.
- Not built, because nothing calls for it yet: a Saved or Changed mark beside a settings page's title on native.
- Tablet and TV measurements the pages do not give (the audio player's columns on a tablet, a card's inset on wide
  forms) are filled in `frames.ts` from the nearest drawn form; correct them there, nowhere else.
