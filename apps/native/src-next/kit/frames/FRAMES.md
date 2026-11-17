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
  `state?: {kind: 'loading'} | PageStateProps`; `children` are kit `Row`s or a grid. The frame spaces them.
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
- `PlayerChromeFrame`: `picture`, `chromeVisible`, `top`, `transport?`, `bottom`, `prompts?`.
- `AudioPlayerFrame`: `close`, `more`, `context?`, `artwork?`, `icon`, `title`, `byline?`, `detail?`, `scrubber`,
  `transport` (five slots), `nextLine?`, `options` (three or four slots), `upNext?`, `loading?`.
- `PageStateFrame`: `icon`, `title`, `body?`, `primaryAction?`, `secondaryAction?`, `retry?`, `details?`.

Parts for the flexible regions: `Section` (a heading and its block), `SectionCard` (a group label and one raised
card of `SettingsRow`s; `stale` marks kept data as out of date), `StateRow` (loading, empty or error inside one
section, as one row of that section's card), `ArtBox` (an artwork slot that always keeps its box).

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
- Type a colour, a font size or weight, or a spacing number. Colours come from `theme.colors`, text from the
  frame's type roles and `theme.type`, spacing from the frame and `theme.space`.

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
- `theme.gutter` is 20 on a medium tablet, where rule 4 says 28. The frames use 28. Fold the two together when
  the screens have moved.
- Home's wordmark bar and the tab bar, sidebar and rail stay with the shell (`shell/NativeShell.tsx`); the frame
  keeps the wordmark bar's room. On Home the shell passes its own bottom inset; fold that into the shell's page
  wrapper so no screen receives one.
- The players' scrubbers live in the screens today (`screens/watch`, `screens/destinations/music`); the frames
  give the scrubber a fixed box. Lifting one shared scrubber into the kit is Stage 2 work.
- Not built, because nothing calls for them yet: a Saved or Changed mark beside a settings page's title on native,
  and outlined empty frames in the gallery (the sections show each frame filled instead).
- Tablet and TV measurements the pages do not give (the audio player's columns on a tablet, a card's inset on wide
  forms) are filled in `frames.ts` from the nearest drawn form; correct them there, nowhere else.
