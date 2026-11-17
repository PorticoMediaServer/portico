# Screen frames (web)

The brief for building a screen. The rules are in the design package (`design/ui-v1`: `system-rules.html`,
`system-frames.html`, `notes/system-rules.md`); their numbers are data in `packages/design/src/frames.ts`; the
frames here apply them through the `--frame-*` tokens in `kit/tokens.css`. The native apps have the same frames
with the same names in `apps/native/src-next/kit/frames`.

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
| A menu, picker or short task | `SheetFrame` (a bottom sheet at phone width, a dialog wider) | the rows |
| Welcome, sign in, local server, codes, profiles | `SignInFrame` (the kit `AuthStage`) | the form |
| The video player | `PlayerChromeFrame` | none |
| Now playing (music, audiobooks) | `AudioPlayerFrame` | phone width: the gap under the artwork; from 1024: Up next |
| The whole page can't load | the frame's own `state`, or `PageStateFrame` | the sentence |

## Props

- `BrowseFrame`: `header: {title, back?, trailing?}` or `hero` (Home; `null` while there is nothing to feature);
  `state?: {kind: 'loading'} | PageStateProps`; `children` are kit `Shelf`s or a grid. The frame spaces them.
- `HomeHero`: the kit `Hero`'s data and render props only (`candidates`, `renderArtwork`, `renderActions`, ...).
  Its sizes are rule 14's.
- `TitleFrame`: `art` (the kit `AmbientArt treatment="title"`), `back`, `topAction?`, `title`, `metadata?`,
  `primary`, `actions?` (round buttons; one the title doesn't support stays with `disabled`), `synopsis?`, `tabs?`,
  `children` (the tab panel, built from `Section`s), `state?`.
- `SettingsListFrame`: `bar`, `list`, `page?`.
- `SettingsPageFrame`: `title`, `back?`, `action?`, `status?` (Saving, Saved), `intro?`, `children`
  (`SectionCard`s), `state?`.
- `SheetFrame`: `open`, `onOpenChange`, `title`, `footer?`, `children`.
- `SignInFrame`: `back?`, `title`, `lede?`, `error?`, `children`. Terms and Privacy are the stage's.
- `PlayerChromeFrame`: `picture`, `chromeVisible`, `top`, `transport?`, `bottom`, `prompts?`.
- `AudioPlayerFrame`: `close`, `more`, `context?`, `artwork?`, `icon`, `title`, `byline?`, `detail?`, `scrubber`,
  `transport` (five slots), `nextLine?`, `options` (three or four slots), `upNext?`, `loading?`.
- `PageStateFrame`: `icon`, `title`, `body?`, `primaryAction?`, `secondaryAction?`, `retry?`, `details?`.

Parts for the flexible regions: `Section` (a heading and its block), `SectionCard` (a group label and one raised
card of `SettingsRow`s; `stale` marks kept data as out of date), `StateRow` (loading, empty or error inside one
section, as one row of that section's card), `ArtBox` (an artwork slot that always keeps its box).

## What a screen may do

- Choose the frame and pass it data, text and callbacks.
- Fill the flexible region with kit components: `Shelf`, `WindowedGrid`, the cards, `Section`, `SectionCard`,
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
- Type a colour, a font size or weight, or a spacing number in a screen's CSS. Colours are `--color-*`, text is
  the frame's role classes and `--type-*`, spacing is the frame's and `--space-*`.

## What the raw-value guard should forbid in `screens/` (Stage 2)

`scripts/lint-design.mjs` already refuses raw colours. Add, for `src/screens/**`:

- `.portico-backdrop`, `ColourBackdrop`, and `background` or `background-color` on a screen's root.
- `font-size`, `font-weight`, `line-height`, `letter-spacing` with a literal value.
- `padding`, `margin`, `gap`, `inset`, `top`, `left`, `right`, `bottom` with a literal length (allow 0 and `var()`).
- `var(--page-gutter)`, `var(--shell-gutter)` and `var(--frame-*)`: the frames place content, screens do not.
- `-webkit-line-clamp`, `text-overflow` and fixed heights on a page or section title.

## Where the pages and the code differ (for the lead)

- The rule 12 placeholder (icon on a raised fill) is `ArtPlaceholder`, used by `ArtBox` and the audio player. The
  kit cards still draw the older generated cover for a titled item with no picture. Both keep the box full. Which
  one cards should use is a design ruling, not made here.
- The moderate hero is the kit `Hero` with `moderate` (through `HomeHero`) and `TitleFrame`. The first mock-ups'
  hero sizes stay in `layout.module.css` for the screens that have not moved; delete them when the last one has.
- Inside the shell the frames take the shell's gutter (`--shell-gutter`, which already follows rule 4); a
  standalone page takes `--frame-gutter`. `--page-gutter` is the older name; remove it when no screen reads it.
- Settings at phone width is a full page with the compact bar (as the web is today), not a sheet as on the phone app.
- The kit `SettingsRow` is about 58 high at 1440 where rule 7 says 76, and a row of `Shelf` cards carries 24 of its
  own padding on top of the 28 or 44 between sections. Both are the shared components' own; fix them there.
- The players' scrubbers live in the screens today; the frames give the scrubber a fixed box.
- Not built, because nothing calls for them yet: a full-height sheet on the web, and outlined empty frames in the
  gallery (the cases show each frame filled instead).
- Measurements the pages do not give are filled in `packages/design/src/frames.ts` from the nearest drawn form;
  correct them there, nowhere else.
