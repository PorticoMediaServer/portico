# Screen frames (web)

The brief for building a screen. The rules are in the design package (`design/ui-v1`: `system-rules.html`,
`system-frames.html`, `notes/system-rules.md`); their numbers are data in `packages/design/src/frames.ts`; the
frames here apply them through the `--frame-*` tokens in `kit/tokens.css`. The native apps have the same frames
with the same names in `apps/native/src-next/kit/frames`.

A screen picks one frame and fills its slots. It never draws a backdrop, a header, page padding, a gutter, the
space between sections, or a page-level loading or error state: the frame does. Each frame has fixed regions,
which never move, and one flexible region, the only place that may change when content is missing or added.

## The rules (round 8, 18 Nov)

Every rule is data in `packages/design` and is inherited; a frame or a screen never types one of these numbers or
colours. Names and values: `packages/design/src/scale.ts`, `index.ts` (`formScale`) and `frames.ts`.

- **R2 form scale.** Type, card, button and row sizes come from the FORM, not the width: one table, `formScale`
  (form x role), projected to the tokens at phone width (`:root`), tablet width (600), desktop width (1024, the `web`
  column) and TV. A page reads `--type-<role>-*`, `--card-*-width`, `--card-caption-*`, `--button-hero|small|xs-*`,
  `--list-row-*`; the kit `Button` and the cards already do. At 600 to 1023 the web draws the tablet's column.
- **R3 emphasis.** Four levels: `--emphasis-primary | -secondary | -tertiary | -accent`. Every role has
  `--type-<role>-color` (its level), and the role classes here apply it. A value at its default is
  `--emphasis-value`, a chosen one `--emphasis-value-chosen` (with their `-weight`); a control that is on is
  `--emphasis-active`; an open tab `--emphasis-on` and its neighbours `--emphasis-off`; progress and the New badge are
  `--emphasis-progress-*` and `--emphasis-badge-*`.
- **R4 icon classes.** `primary` (the transport), `secondary` (shuffle, repeat, tool glyphs), `tertiary` (inline
  glyphs): `--icon-<class>` (size), `--icon-<class>-box`, `--icon-<class>-color`. The kit `Button` takes `iconClass`
  and `active`; dimming is the level's colour, never an opacity.
- **R5 rhythm.** The frame is the one owner of the space between sections. A frame's rows container composes the kit's
  `framedRows` (`kit/layout/layout.module.css`); inside it a `Shelf` adds no spacing of its own and the frame's
  `--frame-row-section` and `--frame-row-heading-gap` are the whole rhythm. Other blocks use `--frame-section-gap` and
  `--frame-heading-gap`. A card's caption is a reserved two-line slot (`--card-caption-height`), so a row's height
  includes its captions.
- **Focus.** The ring is `:focus-visible` only: focus that a pointer hands to an opened menu or dialog draws none.
- **R10 layers.** See "The backdrop" below.

## Which frame

| Screen | Frame | Flexible region |
|---|---|---|
| Home | `BrowseFrame` with `hero={<HomeHero/>}` | the rows |
| A library, See all, any list or grid | `BrowseFrame` with `header` | the rows or grid |
| Search: before typing, results, no matches | `SearchFrame` | the results |
| Live TV: the guide, the channel list, Recordings (Upcoming, Recorded, Series rules) | `LiveFrame` (its `details` is `SplitFrame`'s pane) | the guide, the list or the recordings |
| Saved: My List and its tabs | `BrowseFrame` with `header` (named use `saved`) | the rows or grid |
| Saved: Downloads | `SplitFrame` with Rules as its pane from tablet width, around `BrowseFrame` (named use `downloads`) | the rows |
| Music and Audiobooks libraries, Discover, Songs | `BrowseFrame` with `header` (named uses `music`, `audiobooks`) | the rows or grid |
| A page with a second pane: a programme, a rule, Downloads' Rules | `SplitFrame` | the page |
| The reader (books, comics, PDFs) | `ReaderFrame` (the kit's `ReaderChrome`; the native apps draw it too, in the reading guest) | the page |
| A film, show, season, person, album, artist, book, collection | `TitleFrame` | the synopsis and the tab panel |
| Settings (the list) | `SettingsListFrame` | the groups |
| One settings or owner page, a form | `SettingsPageFrame` | the sections |
| A menu, picker, confirmation or short task | `SheetFrame` with its R9 `task` | the rows |
| Welcome, sign in, local server, codes, profiles | `SignInFrame` (the kit `AuthStage`) | the form |
| The video player | `PlayerChromeFrame` | none |
| Now playing (music, audiobooks) | `AudioPlayerFrame` | phone width: the gap under the artwork; from 1024: Up next |
| The whole page can't load | the frame's own `state`, or `PageStateFrame` | the sentence |

## Props

- `BrowseFrame`: `header: {title, back?, trailing?}` or `hero` (Home; `null` while there is nothing to feature);
  `state?: {kind: 'loading'} | PageStateProps`; `children` are kit `Shelf`s or a grid. A `Shelf` draws its own heading;
  `Section` is only for a block that is not a row of cards, and a `Shelf` never goes inside one. The frame spaces them.
- `HomeHero`: the kit `Hero`'s data and render props only (`candidates`, `renderArtwork`, `renderActions`, ...).
  Its sizes are rule 14's.
- `TitleFrame`: `art` (the kit `AmbientArt treatment="title"`), `back`, `topAction?`, `title`, `metadata?` (facts and the
  rating badge), `primary` (with `progress` and `remaining` it is Resume, with the played part along its lower edge),
  `actions?` (in order: `labelled` ones such as Start over and My List, then round ones such as watched and More; one
  the title doesn't support stays with `disabled`), `tagline?`, `synopsis?`, `credits?` (Directed by, Starring), `tabs?`,
  `children` (the tab panel), `state?`.
- `SettingsListFrame`: `bar`, `list`, `page?`.
- `SettingsPageFrame`: `title`, `back?`, `action?`, `status?` (Saving, Saved), `intro?`, `children`
  (`SectionCard`s), `state?`.
- `SheetFrame`: `open`, `onOpenChange`, `task` (R9, below), `title`, `subtitle?`, `action?`, `foot?`, `anchorRef?`, `children`.
- `SignInFrame`: `back?`, `title`, `lede?`, `error?`, `children`. Terms and Privacy are the stage's.
- `PlayerChromeFrame`: `picture`, `chromeVisible`, `close`, `title?`, `tools?`, `scrubber`, `times`, `transport`, `leading?`,
  `trailing?`, `prompts?`, `body?` (the watch page under an upright phone's picture).
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
| `SeekLine` (`kit/media`) | The scrubber: track, buffered, played, thumb, chapter ticks, marked stretches, the time at the pointer |
| `PlayerScrubPreview` | The thumbnail and its time over the scrubber while scrubbing |
| `PlayerVolume` | Mute and the volume line |
| `PlayerTimes` | The read-outs under the scrubber: position, when it ends, time left, or Live |
| `PlayerTitle` | The title block at the top |
| `PlayerPill` | An options entry point that shows its value (quality, speed) |
| `PlayerPrompt` | Skip intro and Up next, in the frame's prompt corner |

`PlayerChromeFrame` has three drawn forms, and the mock-ups put different things in its places for each. A player
screen fills the places as below; the frame does the arranging.

| Place | Tablet, desktop, TV (`desktop-player`, `tablet-play`, `tv-player`) | Phone on its side (`phone-player` 1, 2) | Phone upright (`phone-player` 4) |
|---|---|---|---|
| `close`, `tools` | on glass discs | plain white glyphs | plain white glyphs; subtitles is a glyph here |
| `title` | beside Close | beside Close | not drawn; the watch page below has it |
| `transport` | middle of the bottom bar | middle of the picture | middle of the picture, smaller |
| `scrubber`, `times` | scrubber, then position left and "Ends" and time left right | position, scrubber, time left on one line | scrubber on the strip's lower edge; "12:04 / 49:00" at the left of the row above |
| `leading` | Episodes | Episodes | not drawn; Episodes is a tab of the watch page |
| `trailing` | volume, subtitles, quality, speed, picture in picture, full screen | labelled pills: Subtitles, Audio, quality | quality pill, full screen |
| `prompts` | white prompt with a round dismiss | white prompt | white prompt |
| `body` | not drawn | not drawn | the watch page |

`AudioPlayerFrame` draws its five transport slots with `PlayerControl` and takes the scrubber and read-outs as
`scrubber`. Their sizes, scrims and type are `frameSpec.player`, `componentSpec.transport` and the `player*` type roles.

The parts are lifted from the built web player (`screens/watch/WatchScreen.tsx`, `watch.module.css`), which still
draws its own copies of the title, controls, read-outs, pills and prompts. Stage 2 moves that screen onto the frame and
these parts and deletes its private drawing. Apply the same test to any other screen that needs more than stock
parts (the reader, the guide grid, the equaliser): name the parts, put them in the kit, draw them in the Kit page.

The player's own panels and the Live TV chrome are kit parts too. They were drawn by the watch and live screens and
were moved, not copied: the screens draw these same parts.

| Part | What it is | Form |
| --- | --- | --- |
| `PlayerOptions` | Playback options with Subtitles, Audio, Quality, Speed, subtitle style and the sleep timer, from one `PlayerOptionsModel` | Desktop: one panel of columns over the picture. Phone upright: a bottom sheet, a list that opens each sub-list. Phone on its side: the same list in a side sheet |
| `PlayerEpisodes` | The Episodes drawer: season chip and a row of episode cards | Across the bottom of the picture; on an upright phone it rises over the watch page |
| `PlayerUpNext` | The Up next card over the credits (`layout`: desktop, tablet, phone) | The player's bottom right corner |
| `PlayerLive` | The Live TV chrome from one `LiveChromePort`: channel and programme lockup, Record, the programme-long bar with the live edge and Go to live, channel down and up, Guide and its short guide | One layout that narrows; it is not built on `PlayerChromeFrame` |

Changed in the fix round after the first captures:

- `PlayerChromeFrame` takes `card` (Up next: alone in the picture's corner, controls hidden), `panel` with
  `panelCovers` (`bar` for the Episodes drawer, `all` on a phone on its side) and `scrubbing` (the transport makes way
  for the preview at phone width). At phone width a prompt sits in the strip's bottom row, where it covers nothing.
- `SeasonBar` and `EpisodeRows` (in `kit/frames`): the bar over a show's episodes, and the kit `EpisodeTile`s laid out
  as rows. The show page's own copy of the row rules (`screens/detail/show.module.css` `.episodeRow`) goes in Stage 2.
- Skip arrows weigh the same as Previous and Next. `AudioPlayerFrame` is re-laid: the title follows the artwork, and
  from 1024 the column sits centred beside the Up next panel under a bar across the window.

Changed in the second fix round (r5):

- `PlayerLive`: the transport's side controls are plain glyphs, the skip seconds are drawn inside the arrows, Record carries the red mark, and at phone width the title block has its own row so the programme's name is not cut.
- The tab rule runs the page's full width (`tabsInset` pads the tabs, not the strip). `PlayerOptions` keeps its place: its CSS anchors it 40 from the right and 156 from the bottom, which is where desktop-player 3 draws it at the mock-up's height; the button that opened it shows as active.
- `AudioPlayerFrame` matches native: `source`, `favourite`, `chapter`, `next`, tiles, `inline`, skip seconds. The queue in the gallery is the kit `SongRow`.
- The gallery's top bar uses the shell's own `DesktopNavigationUnderline` and `TopBarActivity`: the shell already drew both; the gallery's stand-in bar had left them out.
- The Settings index in the gallery is the shell's own `SettingsNav`, passed as the frame's `list`.

Live screens whose look changes with this foundation:

- web Live TV: plain side controls with the seconds inside the arrows, a red Record glyph, and at phone width the programme's name on its own row (up to two lines).

For Stage 2, so two drawings are not inherited:

- `SeasonBar` here is a stand-in for the gallery's watch page, not the native part's equal: it has no owner `settings` menu, takes its Hide watched label as a prop, and draws the season trigger as a small Button. The live show page draws it as `Chip variant="toolbar"` with `SeasonMenu` (`screens/detail/Show.tsx`), which is also what the mock-up's `ep-bar` draws. Stage 2 moves the show page's bar into the kit with the owner variant and deletes this one.
- `EpisodeRows` lays `EpisodeTile` out by the order of its children (`:nth-child`), so a change inside the tile breaks it silently. Stage 2 gives the web tile a `layout="row"` prop, as the native kit has, when it moves `show.module.css` `.episodeRow`, and deletes these rules.

Changed in the final round before the owner's review (r6):

- `PlayerLive` upright (owner, 18 Nov): when its box is a short strip (a container query on its own height, so the live screen's full-window form is untouched until Stage 2 puts it in a strip), Guide, Record and More are icons in the top row, the transport with channel down and up is in the middle of the picture, and the time, how far behind live, Go to live and the options are along the bottom. The channel and programme are named on the page under the strip.
- `AudioPlayerFrame`: the artwork, title, scrubber and transport are one group; from 1024 the group is centred in the height.
- The transport hides while the scrubber is held on a phone on its side too. A phone-width toast's message wraps and is never cut.
- The gallery's sign-in case sits on the window's backdrop like every other page.

Left for Stage 2, listed so it is not forgotten:

- `PlayerLive` draws its own top bar, bar and controls row instead of filling `PlayerChromeFrame`. It matches the
  mock-up as it is; folding it onto the frame means the frame's scrubber and read-out places taking the live bar.
  On an upright phone its title is cut to one line by the buttons beside it (no mock-up draws that form).
- `PlayerOptions`, `PlayerEpisodes` and `PlayerUpNext` keep the fixed sizes they had in the screen (their CSS files
  moved with them). Reading them from `frameSpec.player` is Stage 2 work with the rest of the watch screen.
- An episode with no still shows the drawer's own blank, not the rule 12 placeholder.
- Still in `screens/watch`: Chapters (`ChaptersDrawer.tsx`, which shares the Episodes drawer's styles), the queue
  (`QueueList.tsx`), Playback info, the version chooser, Still watching and the end card.
- Audio player, still short of the mock-up (`final3-desktop-13-now-playing.jpg`): the volume line under the transport, the queue as a panel with Up next and Lyrics tabs, and Save as playlist at its foot.
- The home hero's pager is text and lines; the mock-up (`mock.html` home, `final-desktop-1-home.jpg`) draws four thumbnails with names. Part: `HomeHero`.
- A card's own menu (⋯) has no gallery case with fixed inputs: `motion-11-desktop-pages-menus-panels.jpg` draws it. Stage 2 adds the case with the first screen that opens one.

Round 7 (18 Nov): in the upright live strip Go to live sits on a row of its own above the bar's live end, so it covers neither the bar nor the read-out; the gallery's page under the strip carries the channel mark, the Live mark, Guide and Record. The Record glyph (`i-rec`) is a solid dot. A sheet's handle shows keyboard focus by its own colour, not a ring.

## Round 8: the kinds of screen that had no frame

Each frame's required regions are data: `frameRegions` in `packages/design/src/frame-regions.ts` (keys `search`, `live`,
`split`, `reader`, and the named uses `saved`, `downloads`, `music`, `audiobooks`, each listing the BrowseFrame regions it
fills). Gallery cases: `frames-search-empty`, `-search-results`, `-search-none`, `-live-guide`, `-live-details`,
`-live-list`, `-live-upcoming`, `-live-recorded`, `-live-rules`, `-saved`, `-downloads`, `-music`, `-music-songs`,
`-audiobooks`, `-reader`, `-reader-contents`, `-reader-display` (family `frames-newframes`, `dev/families/frames-newframes.tsx`).

- `SearchFrame`: `title?` (phone, before typing), `field`, `cancel?` (phone, with text), `scopes?` (the type chips with counts), `notice?` (part of a search ran late), `none?` (`{title, body?, action?}`: no matches, drawn large at the top of the results), `children` (the flexible region).
- `LiveFrame`: `title`, `back?` (a phone's Recordings page), `switcher?` (All channels), `facts?` (expanded: the source, channels, how far the guide reaches), `actions?` (a phone's List | Guide and Recordings; TV's Today, Now, Recordings), `tabs?` (Guide | Recordings; a phone's Recordings views), `chips?` (groups, or Recordings' views and storage), `lead?` (expanded: On now), `toolbar?` (day, 2 h steps, Now, sort, Grid | List), `children`, `details?` (a `FramePane`), `hints?`, `state?`. The header is the kit `LibraryHeader` from 600, as `kit/dvr` RecordingsLayout draws it.
- `SplitFrame`: `children` (the page, a frame) and `pane?` (`FramePane`: `title`, `eyebrow?`, `subtitle?`, `close`, `children`, `footer?`). From tablet width the pane is a raised column on the right and the page takes the rest; on a phone it is a bottom sheet; on TV the same column (Back closes it). Pane width is `frameSpec.pane.width` (`--frame-pane-width`).
- `ReaderFrame` is the kit's `ReaderChrome` under the frame's name (bar, tools, page, page arrows, progress, `panel`, and the display settings as `ReaderDisplayPopover` / `ReaderDisplaySheet` / `ReaderDisplaySidePanel`). One copy: the native apps draw it in their reading guest.

Parts, the same names in both apps:

| Part | Where | What it is |
|---|---|---|
| `SearchHistory` | `kit/search` | Recent searches: rows with × on a phone, chips wider; Clear in the heading |
| `TopResultCard` | `kit/search` | The phone Top result (moved from `screens/search/TopResult.tsx`); from 600 the Top result is the kit title's `SearchResultHero` |
| `GuideGrid` | `kit/live` | The guide: channel column, time ruler with now, rows of programmes, the now-line |
| `ChannelRow` | `kit/live` | One channel in the list: mark and number, what is on, time left and bar, what is next |
| `AiringRow` | `kit/live` | One airing: More airings, Later on, Coming up |
| `RecordingRow`, `ConflictCard` | `kit/live` | Upcoming and Recording now; a tuner conflict |
| `RuleCard` | `kit/dvr` (already in the kit) | One series rule |
| `StorageMeter` | `kit/saved` | How much room is used: Downloads (and a phone's Recordings; wider, Recordings uses `kit/dvr` RecordingStorage) |
| `DownloadRow` | `kit/saved` | One download: poster, facts, where it is, its bar, Play / Try again / More |

`Section` takes `subtitle?` (a quieter line beside the heading). `SearchHistory` and `TopResultCard` were moved out of `screens/search`; the Search screen draws them. The reader's bar title and author take a second line instead of an ellipsis (no cut text). 

Left for Stage 2, so it is not forgotten:
- `screens/live/GuideScreen.tsx`, `ProgrammePanel.tsx` and `RecordingsScreen.tsx` still draw their own grid, list, rows and pane, and `kit/dvr` RecordingsLayout is LiveFrame's header for those pages; `screens/saved/Downloads.tsx` its own rows. Each is bound to its controller and moves onto these parts with its screen.
- Music, Audiobooks and Saved are named uses of `BrowseFrame`: the library switcher, counts line, tabs, toolbar and grid are the library frame's regions (stage1/r8-library). Until they land, the gallery cases draw the kit `Tabs` at the head of the rows and rows in place of the grid.

## Decided by the owner (Justin, 18 Nov)

- **Skip intro on an upright phone** sits in the picture's bottom row between the time and the options. It never covers the transport and is hidden while a panel or Up next is open.
- **Scrubbing on a phone:** the transport hides while the scrubber is held, and the knob follows the previewed time.
- **Live TV on an upright phone:** Guide, Record and More are icons in the top row; channel down and up sit either side of the transport; the time, how far behind live, Go to live and quality are in the bottom row.
- **TV playback options open in place** under their headings, as the TV mock-up and today's TV app do. This is a requirement on the shared panel: it is built into `PlayerPanelGroup` when the TV player moves onto it in Stage 2. Until then the gallery's TV panel shows one list at a time.
- **Episodes panel:** a drawer on the web, a column on a tablet. Both stay.
- **Tablet title page:** the hero layout, the same as the web and a phone. tablet.html frame 7's header-bar layout is not used.
- **Button labels are never shrunk and never cut.** Buttons are sized so standard labels fit on every form; a long translation makes the button wider or takes a second line. An ellipsis on a button label is a fault. In the native kit: no label is shrunk to fit, and every labelled `Button` lets its label take a second line, its height being a minimum.
- Also settled: the Watch Together pill comes in Stage 2; phone transport is 76 / 76 / 58; TV player type takes the nearest roles; Start over is a button on a tablet; the synopsis is two lines; both missing-art looks stay; the tablet gutter is 28; web Settings is a full page at phone width; the audio queue uses the kit's song row; web cards carry their second line; the native backdrop comes in Stage 2.

## The backdrop (owner, 17 and 18 Nov; R10)

ONE backdrop everywhere: `body` draws it (`kit/backdrop.css`) and nothing else does. A frame, a screen and a page draw
no backdrop and no page colour. A sheet, menu or dialog is a plain kit surface. A full-window cover that must hide the
page beneath it is the one class `portico-cover` (opaque, with the same backdrop); its two named forms are the audio
player (`data-cover="audio"` with the kit `ArtworkGlow` as its first child: a glow from the playing item's artwork)
and the video player (`data-cover="player"`, black). `ColourBackdrop` and `.portico-backdrop` are gone.
`web/test/one-backdrop.test.ts` lists the covers and fails when the backdrop, the cover or the glow is used elsewhere.

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
  the `--type-*` roles (rule 6's: `page-title`, `bar-title`, `row-heading`, `group-label`, `list-label`,
  `row-label`, `row-help`, `intro`, `card-title`, `card-subtitle`, `tab`), spacing is the frame's and `--space-*`.

## What the raw-value guard should forbid in `screens/` (Stage 2)

`scripts/lint-design.mjs` already refuses raw colours. Add, for `src/screens/**`:

- `background` or `background-color` on a screen's root (the backdrop itself is already held by `test/one-backdrop.test.ts`).
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
  standalone page takes `--page-gutter`, which is now 28 from 600 to 1023.
- Settings at phone width is a full page with the compact bar (as the web is today), not a sheet as on the phone app.
- The kit `SettingsRow` is about 58 high at 1440 where rule 7 says 76 (`--list-row-one-line`): the Settings family
  applies it. A `Shelf` on a frame no longer adds padding of its own (R5).
- Not built, because nothing calls for it yet: a full-height sheet on the web.
- Measurements the pages do not give are filled in `packages/design/src/frames.ts` from the nearest drawn form;
  correct them there, nowhere else.

## Round 8, family E: overlays by form (R9), Settings, sign-in, account, page states

### R9: which overlay a task takes on each form

One table, `overlayForms` in `packages/design/src/overlay-forms.ts`. A screen names the TASK; the kit picks the kind,
the bar and the foot. Bar: start · title · end. Foot: **halves** (equal shares), **full** (one button the width),
**end** (own width at the end, primary last), never hugging the left. An anchored overlay opens below or beside its
trigger and leaves it in view; a modal one dims the page. On TV (detail-tv 5c, 5d, tv-focus 4, 9, 21, 38, 43) a menu, a
picker or a short task is the right-hand side sheet with key hints at its foot (foot `hints`: Back closes, and the foot
says so); Filters is the side panel with its two buttons; a confirmation is a centred dialog starting on Cancel; File info
is a full-height side sheet with Done; the profile menu is the one anchored flyout, right of the rail; Settings is a page.
No mock-up draws Notifications on TV, so that one cell is `null`.

| Task | Phone | Tablet (medium) | Wide (expanded, web) | TV |
| --- | --- | --- | --- | --- |
| `actions` (More / long press) | sheet, modal; bar: title + item · Done | popover beside the card, modal | popover, not dimmed | side sheet, modal; eyebrow + title; foot: key hints |
| `choice` (select list, switcher, sort) | popover under the trigger, modal | popover, modal | popover, not dimmed | side sheet, modal; eyebrow + title; foot: key hints |
| `confirm` (Delete…?) | sheet; foot halves (Cancel, the action; destructive is a red fill) | dialog; foot at the end | dialog; foot at the end | dialog; foot at the end, Cancel first |
| `task` (Add source, Save as playlist) | sheet; bar: Cancel · centred title; foot full (the action) | side panel, modal; title · round ×; foot halves | dialog; title · round ×; foot at the end | side sheet, modal; title; foot: key hints (no Cancel) |
| `details` (File info) | sheet; bar: title + item · Done | side panel; title + item · round × | side panel | full-height side sheet; eyebrow + title; foot full (Done) |
| `filters` | sheet; title · Done; foot halves | side panel; title · round ×; foot halves | side panel | side panel; title; foot halves (Clear all, Show N) |
| `settings` | full sheet; index "Settings · Done", a page "‹ Settings · centred title · its action" | a page beside the list | a page beside the list (web: full page at phone width, ruled) | a page |
| `account` | sheet; title · Done | popover under the avatar | popover under the avatar | flyout right of the rail |
| `notifications` | a page in the Settings sheet: "‹ Settings · Notifications · Mark all read" | popover under the bell | popover under the bell | not drawn |

Kit: `SheetFrame` takes `task` (native `kind: 'task'`), `title`, `subtitle?` (the item), `action?`, `foot?`,
`anchorRef?`. Sheet and side-panel feet share the width; a modal popover draws a scrim; focus placed on open draws
no ring until a key is pressed (web: the document's last input; Android's touch rule is the rules hand's); a menu row
that does not fit takes a second line.

### Regions added (`frameRegions` in `packages/design/src/frame-regions.ts`)

- `SettingsListFrame`: `profile?`, `shortcuts?` (phone), and on native `search?` (the frame draws search, profile,
  tiles, then the list; pass `SettingsNav` no search of its own). The web's `SettingsNav` keeps its own search and
  draws the frame's profile and tiles under it.
- `SettingsPageFrame`: `status?` ('saving' | 'saved', "✓ Saved"), `actions?` (header buttons), `server?` (the kit
  `ServerHeader`: the strip over the title wide, the card under the bar on a phone), `tabs?` (kit `Tabs`). A page
  shown alone (not beside the list) keeps Back, the gutter and the page's column limit (tablets SC-8).
- `SectionCard`: `help?`, the section's help paragraph under its label.
- `SignInFrame`: `kicker?` ("Sign in to" over the server's name).
- `PageStateFrame`: `downloads?` (kit `DownloadsStillPlay`, moved from the restored shell on native) and `help?`.
  On a tablet the block sits at the gutter with buttons at their own width (SC-11); on a wide web window it starts
  high under the bar and grows downwards.

Gallery: `frames-settings` (web `dev/families/frames-settings.tsx`; native `dev/families/frames/settings.gallery.tsx`):
settings index (Saved, help, a switch with dependent rows, inline selects, the matrix with Changed/Reset, Advanced,
the subtitle preview), search with highlighted results, an owner page (server strip, buttons, tabs, a device to
approve, accounts), a page alone, each overlay task over a real page, the sign-in steps (web: the kit's own
pages: local server with chips, Show and links; locked out with its countdown; the code boxes with the recovery
link; the Portico Account hand-off), Who's watching, the profile PIN, the account menu, Notifications, and the page
states (can't reach with downloads, Details and help; not found; something went wrong; playback failed).
