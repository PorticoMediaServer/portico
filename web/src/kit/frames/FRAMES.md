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
| Now playing (music, audiobooks) | `AudioPlayerFrame` | `panel`: the queue, chapters or Lyrics |
| The whole page can't load | the frame's own `state`, or `PageStateFrame` | the sentence |

## Props

- `BrowseFrame` (`BrowseFrame.tsx`; its regions are `frameRegions.browse` and `.home` in packages/design, drawn below per
  width): `header: {title, back?, trailing?, switcher?, menu?, tabs?, intro?, actions?}` or `hero` (Home; `null` while there
  is nothing to feature); `toolbar?`; `filters?: {open, onClose, onClear, count?, save?, children}`; one flexible region,
  `children` (kit `Shelf`s; the frame spaces them) or `grid` (a self-scrolling kit `WindowedGrid` at `height="100%"`) with
  `alphabet?`; `state?: {kind: 'loading'} | PageStateProps`. A `Shelf` draws its own heading; `Section` is only for a block
  that is not a row of cards, and a `Shelf` never goes inside one.
- `HomeHero`: the kit `Hero`'s data and render props only (`candidates`, `renderArtwork`, `renderActions`, ...).
  Its sizes are rule 14's.
- `TitleFrame`: its regions are `frameRegions.title` in `packages/design/src/frame-regions.ts` (which forms draw each, and
  which regions each kind of title page fills: `titleKinds`). `art` (the kit `AmbientArt treatment="title"`) or `cover`
  (`{node, shape: 'poster' | 'square' | 'circle'}`: a person, artist, album or book), `back`, `topAction?`, `kicker?`
  (`{label, icon?, detail?, onPress?}`: "Dune collection · 2 films", "Acting", "Album"), `title`, `metadata?`, `picker?`
  (`TrackPicker`, up to 1024), `primary?` (with `progress` and `remaining` it is Resume), `actions?`
  (`labelled`, `active`, `pinned` for the collapsed bar; one the title doesn't support stays with `disabled`),
  `tagline?` and `credits?` (from 1024), `synopsis?`, `readMore?`, `cast?` (an open foldable's left
  page), `tabs?`, `children` (the tab panel), `aside?` (beside the panel on wide forms, under it otherwise),
  `collapsed?` (set by the frame from the scroll; pass it to hold it), `state?`.
- `SettingsListFrame`: `bar`, `list`, `page?`.
- `SettingsPageFrame`: `title`, `back?`, `action?`, `status?` (Saving, Saved), `intro?`, `children`
  (`SectionCard`s), `state?`.
- `SheetFrame`: `open`, `onOpenChange`, `task` (R9, below), `title`, `subtitle?`, `action?`, `foot?`, `anchorRef?`, `children`.
- `SignInFrame`: `back?`, `title`, `lede?`, `error?`, `children`. Terms and Privacy are the stage's.
- `PlayerChromeFrame` (`PlayerFrames.tsx`): the regions in `frameRegions.player`: `picture`, `close`, `title?`, `tools?`, `scrubber`,
  `times`, `transport`, `leading?`, `trailing?`, `prompts?`, `card?`, `panel?` (`panelCovers?`), `body?`; states `chromeVisible`, `scrubbing`.
- `AudioPlayerFrame` (`PlayerFrames.tsx`): the regions in `frameRegions.audio`: `close`, `context?` (`source?`), `device?`, `more`,
  `notice?`, `artwork?` (`icon`), `title`, `badge?`, `byline?` (parts), `favourite?`, `titleMore?`, `chapter?`, `scrubber`, `times`,
  `transport` (five slots), `volume?`, `next?`, `lyrics?`, `queue?`, `speed?`, `sleep?`, `chapters?`, `panel?`; `loading?`.
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

The parts are lifted from the built web player (`screens/watch/WatchScreen.tsx`). Moved (21 Nov): the watch screen draws
its chrome with `PlayerChromeFrame fill` (the frame fills the chrome's box over the retained video, whose geometry still
places the picture and an upright phone's watch page) and these parts; its own title, controls, read-outs and pills are
deleted. The prompts and notices keep their own layers (their focus ownership is the screen's). Apply the same test to any other screen that needs more than stock
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
- `SeasonBar` and the episode list (in `kit/frames`): the bar over a show's episodes, and the kit `EpisodeTile`s laid out
  as rows. Round 8 moved the show page's own bar and rows into the kit in their place.
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
- The live guide, Recordings and Series rules are drawn on `LiveFrame` (Recordings and Series rules through `screens/live` RecordingsFrame); `GuideScreen.tsx`, `ProgrammePanel.tsx` and `RecordingsScreen.tsx` still draw their own grid, list, rows and pane inside it; `screens/saved/Downloads.tsx` its own rows. Each is bound to its controller and moves onto these parts with its screen.
- Music, Audiobooks and Saved are named uses of `BrowseFrame`: they fill its `header.switcher` (libraries), `header.menu`, `header.trailing` (Shuffle all), `header.tabs` (client-core `libraryTabs`; Saved's seven), `toolbar` (Saved's My List) and rows. Saved's My List grid sits in the page's flow at `frameSpec.browse.grid`'s column width and gaps, because its Music and audiobooks row follows it (desktop-search saved). The counts line under a library's title waits on a `header.counts` region (RULE-REQUESTS, newframes part 2). The web's Downloads tab is the server's prepared files (collections-34, -36); a device's own downloads with Rules are the apps'.

## The library and Home (round 8)

The owner's largest regression was the library: the frame had a title, Back and one button. The required regions are now
data (`frameRegions.browse`, `frameRegions.home`): each region's id is the frame's prop of that name, the forms that draw it and
the mock-up frames it comes from. How the web frame draws each:

| Region | Phone width (under 600) | 600 and up |
| --- | --- | --- |
| `header.title`, `header.switcher` | the compact bar: the switcher (the library's name and ⌄) centred (`StickyBrowseHeader`) | the large title with the round chevron beside it (`LibraryHeader`) |
| `header.back` | ‹ Libraries at the left of the bar; on a plain page Back over the title | on a plain page Back over the title (See all); a library has none |
| `header.trailing` | not drawn | at the right before ⋯ (Shuffle on Music; on a touch tablet Select) |
| `header.counts` | not drawn | the counts line under the title ("10 artists  10 albums  40 songs", final3-desktop-12) |
| `header.menu` | ⋯ at the right of the bar | the glass ⋯ square at the right of the title row |
| `header.tabs` | inside the bar, the strip scrolling | under the title from the gutter, the rule across the page |
| `header.intro`, `header.actions` | a plain page: the line under the title and Play / Shuffle under it (desktop-browse seeall) | same |
| `toolbar` | the kit `BrowseToolbar`: Filters and sort chips with the quick chips scrolling, then the count with grid / list | Filters (with its count), the quick chips, the count, sort with order, grid / list, card size, Save view; applied chips with Clear all on a second line |
| `filters` | a sheet: Filters and Clear all, the fields, Save view and Show 19 movies | a side panel over the page (520; 420 under 1024), docked beside the grid on a touch tablet (380) |
| `grid` + `alphabet` | the grid fills the window under the toolbar and scrolls itself; the A–Z rail stands in the right gutter | same; columns of at least `frameSpec.browse.grid.card` stretched to both gutters, `grid.columnGap` / `grid.rowGap` |
| `children` (rows) | the frame owns the space between rows: its rows compose the kit's `framedRows` (R5), so a kit `Row` adds no margin of its own | same |
| `state` | loading keeps the header, tabs and toolbar and draws the grid's boxes in the grid's own columns (or two rows' boxes) | same |
| `hero` (Home) | the moderate hero under the shell's wordmark bar; the pager is dots | the pager is the thumbnails with their names (`renderThumbnail`) |

Kit rules that came with it: a row's heading is never squeezed by its subtitle (the subtitle moves under it); at phone width a
row's See all is the chevron after its heading; a phone's 16:9 card says the episode number only, wider cards add its name; a
library's title wraps and is never cut. The Filters surface (`kit/browse/FiltersSurface.tsx`) moved from `screens/browse/Filters.tsx`,
which draws it from the kit. The gallery's cases are the `frames-library` family (`dev/families/frames-library.tsx`).

Left for Stage 2: `screens/browse/Library.tsx` and `Discovery.tsx` still compose these parts themselves; they move onto the
frame's regions, and `BrowseResults` becomes the frame's `grid`. A list view (table) has no case here: it is `BrowseResults`'
list rows, bound to the browse controller.

## Round 8: the title page's forms (family C, 18 Nov)

One frame for every kind of title page, its regions written once as data (`frameRegions.title`, `titleKinds`).
`TitleFrame` now lives in `TitleFrame.tsx` with `title.module.css`; its parts are `title-parts.tsx`.

| Width | Layout |
|---|---|
| Up to 1023, taller than wide | One centred column: the art box with Back and Play on as glass discs, the kicker, title and facts hanging from its bottom edge; the action row (round buttons), the played line with Start over, the synopsis, the picker row; the tabs; the panel; the side under it. A cover page puts the cover centred above the words (phone-detail d1, d3; tablet f8) |
| 600 to 1023, wider than tall | Two pages (foldable f2): the title and the cast at the left, sticky; the tabs and their panel at the right |
| From 1024 | The words over the art at the left: kicker, title, facts, tagline, synopsis, credits, then the row with labelled buttons and Resume carrying its played part. A cover page: square or poster at the left, circle at the right (desktop-listen, desktop-detail f7). The side beside the panel (desktop-detail f8) |

The collapsed bar is sticky under the shell's bar and takes no room until the tabs pass under it: at phone width Back,
the title centred and Play on with the tabs under it; from 600 the title, the tabs beside it, the primary and the
`pinned` actions (desktop-detail f3, f4). A screen that holds `collapsed` (a tab opened directly) gets the bar in place
of the hero and the page's tabs, with the panel under it. An action marked `wide` (Watched) is drawn only from 1024; up
to 1024 it is in More (phone-detail d1). Loading keeps the boxes, says nothing it does not know (the time left) and
keeps skeleton cards in the panel.

Parts (`title-parts.tsx`): `PanelBlock`, `PanelHeading`, `PanelText`, `AboutTable`, `CastRow`, `TrackPicker`,
`NumberedRow`, `OwnerCard`, `EpisodeNeighbour`, `VersionRows` (Play a different version, R9 `versions`), `EpisodeDisplay`
(the owner's Episode display, R9 `choice`), and, moved from the show page (one copy; `screens/detail/Show.tsx` and
`SeasonMenu.tsx` draw them): `SeasonBar` (season trigger, count, watched progress, episode order, Filters, Hide watched,
list or tiles, the season's ⋯; this replaces the stand-in), `SeasonMenu` (every season with its years, count and
progress, Specials, a box to type a season number past 30), `EpisodeList` with `episodeRowClass` and
`EpisodeListOpened` (the episodes as a list or tiles, an episode opened under its row). The list's rules now name the
tile's parts (`[class*="episodeCopy"]`) instead of counting its children: the tile has hidden accessible spans first, so
the counted rules had put the synopsis under the picture on the show page's list.

Moved onto the frame (19 Nov): the film page (`screens/detail/Detail.tsx` `FilmTitle`). For it the frame gained: `back`
optional (no way back, no disc); `TitleAction.wrap` (the screen's own menu around the frame's More button);
`TitlePrimary.note` (why a disabled Play can't play, under the row, J-23); the synopsis draws the server's inline bold,
italic and line breaks; the tabs and their panel are linked (tab ids, `role="tabpanel"`).

Gallery: `dev/families/frames-title.tsx` (family `frames-title`): the same cases as native, with versions, file-info
(the kit's `FileInfoBody` on the `details` form) and episode-display. The frames family's old
`title`, `title-missing` and `title-loading` cases are these now.

The person, collection, album, artist, audiobook, book and series pages are on the title frame (a series' volumes are
the `stack` cover, the kit's `CoverStack`; a book's first lines are `opening`); `CoverHero` is gone and only its name link,
`CoverHeroLink`, is left. Still for Stage 2: the show page's Details (`ShowDetails`, `ShowPeople`) and the episode opened in place
(`EpisodeDetailsLayout`, `EpisodeBody`) still draw their own tables and people; `AboutTable`, `CastRow` and
`EpisodeNeighbour` are their kit forms (`EpisodeNeighbour` is already the show page's).

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

Round 8 part 2: inside a side panel, a dialog or a popover the frame sets the column (native `FrameColumn`), so the page
gutter and card inset are zero there (the surface's own padding is the edge); a phone sheet keeps them. On TV a task's own
action is the last stop of the side sheet, the sheet's width, with the key hints still at the foot; File info on TV takes
the side sheet's own heading. Every focusable in a TV sheet needs a stable testID (the frame names its Cancel and action
`<testID>-cancel` / `-action`).

### R12: feedback (toasts) by form

`toastCards` and `toastPill` in `packages/design/src/overlay-forms.ts`, beside R9; the web reads them as `--toast-*`
(tokens.mjs). A toast with an action is the form's CARD: on a phone the window's width less 8 each side, above the tab bar
or the player's controls, padding 10/8/10/14, the action a tinted button 36 high (phone-player); wider, centred 36 above
the edge (or above the bar or the dock), radius 14, padding 14/14/14/22, gap 22, the action an accent text button 36 high
(desktop-browse); inside the player a wider form stands it 24 in from the start (desktop-player; `placement="player"` on
`ToastHost`). A confirmation with no action is the PILL, 40 high, radius 20, padding 0 16, at the top centre (under the
desktop top bar on web; desktop-listen). TV keeps its own toast (top trailing). Kit: native `ToastHost` / `Toast`, web
`ToastHost` / `Toast`; a screen passes only the message, the action and, in the player, the placement.
Gallery: web `frames-settings` toast-card, toast-pill, toast-player; native `overlays-toast` (pill), `overlays-toast-action`
(card), `overlays-toast-player` (tablet). Also new in `frames-settings`: settings-integrations (+ -failed: the switches'
refused save as rule 9's error row) and overlay-two-step (+ -refused: the two-step code box over the page it guards; web
kit `TwoStepCode`, shared with the sign-in step).

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

## The players, round 8 (family D)

Required regions are data: `frameRegions.player` and `frameRegions.audio` in `packages/design/src/frame-regions.ts`
(each region's id, the forms that draw it and the mock-up frames it comes from). Both apps' `PlayerChromeFrame` and
`AudioPlayerFrame` take exactly those regions as props. Gallery: `dev/families/frames/player.gallery.tsx` (native),
`dev/families/frames-player.tsx` (web, family `frames-player`).

Rules this round (frames.ts `player` and `audio` blocks):
- **R6, R4 (interim): the transport's icon classes per form** (`frameSpec.player.transport`): Play or Pause's disc and
  glyph, the other buttons' box, the 10/30 arrows drawn larger than Previous and Next (`skipIcon` > `stepIcon`), a
  legible numeral, the gap. All of the transport is primary (white). The bar's own tools (subtitles, picture in
  picture, full screen) are `tool`: secondary, smaller and dimmer. The upright strip has its own row (`transport.upright`:
  a phone's 58 disc, a tablet's 84). The audio player's are `frameSpec.audio.icons` (shuffle and repeat secondary). The
  web values are the mock-ups' (desktop-player 1, phone-player 1 and 4), not the old built player's.
- **R6: the scrubber** is one continuous line (`player.track`: 6 on the web and a tablet, 4 on a phone, 8 on TV); the
  recap, intro and credits are tinted on it; desktop draws no chapter marks. The knob grows while held (`knobHeld`).
- **R8: the upright form is a window taller than wide** (`player.upright.maxAspect` 1). An open foldable (wider than
  tall) gets the full-window player. On the web a window 500 or less high is a phone on its side and takes the phone's
  sideways forms whatever its width (`sidewaysMaxHeight`).
- **The screen may choose the form** (`PlayerChromeFrame form`: `upright`, `sideways` or `wide`; a `data-form` attribute the
  forms' CSS reads). Left out, it is the window's, as above. The watch screen passes it because a phone's Full screen makes
  an upright phone the full-window player.
- **SC-10: beside an open panel the bar drops its spare parts** (`PlayerBeside`; a `PlayerPill` or `PlayerControl`
  marked `spare`, the speed pill on a tablet), so nothing overlaps.
- **The button whose panel is open shows it** (`open`): a raised disc with a dot (a control), the raised fill (a pill).
- **TV playback options open in place under their headings** (owner ruling 4): `PlayerPanelGroup heading` (the list's
  name and its current value in the accent; its rows follow while open). A tablet beside the picture does the same
  (tablet-play 2b); a phone and the upright form drill in. Subtitles carry Timing and Search for subtitles (with the
  reason it cannot search); quality says two lines; More holds Subtitle style, Chapters and Playback info.
- **The audio player's wide forms are their own layout** (R7): a column as wide as the cover (title with the heart and
  More, the album after the artist, the scrubber with its times at its ends, a compact transport, volume on the web or
  the pill row on an upright tablet) beside a raised queue panel; the device at the top right beside More. The
  audiobook puts its cover beside its title (Unabridged badge, "Read by"), a chapter line, a book-long scrubber and
  Speed and Sleep timer as rows of choices. TV puts the regions at final3-tv-12's places (96 title, the queue to the
  safe edge, titles never cut: a title takes its second line). **The frame draws no backdrop**: the audio player's
  layer is the glow from the artwork (`Layer kind="audio"`; the gallery stands in for that host).

Audio frame inputs for the screen move (lead, 19 Nov): `cover` (the screen's own view in the artwork's box, the shared
element the dock's morph paints into), `signal` (the music signal line under the words), and `data-group` 0/1/2 on the words,
the scrubber + transport + volume, and the panel, the three groups the open motion fades in turn. The favourite's failure
banner and the equaliser are `panel` content.

Parts added: `AudioPanel` (tabs, list, footer; TV: heading and count), `QueueGroup`, `QueueRow` (playing / handle /
done / number lead, two-line title, a chapter's progress, touch More or pointer actions), `AudioChoiceRow`
(Speed, Sleep timer), `PlayerPrompt note` (Skipped the intro + Undo). Moved into the kit (one copy, the screen imports
it back): native `StillWatchingCard` (`screens/watch` -> `kit/player`), web `EndCard` (`screens/watch` -> `kit/player`
as `PlayerEndCard`).

Cases: player (playing, paused, buffering, scrubbing, missing), Skip recap, Skipped + Undo, options / subtitles /
audio / quality / speed, Episodes (the Episodes button open, "Paused at"), Up next, Up next with Skip credits and the
scene after the credits, Up next counting down, Still watching? (native), the end of a film (web), live TV at the
live edge with live data (native), live controls, the short guide (web); audio music, Lyrics (none), choose lyrics
(web), Save as playlist (web), the queue sheet (phone), missing, audiobook, loading.

Still in the screens, with why (part 2 moved the rest, below): the web's Live TV chrome is the kit `PlayerLive`, its
own chrome rather than this frame.

**Part 2 (18 Nov): the player's missing cases.**
- **Episodes as each form draws it (owner ruling 5):** the drawer across the bottom (`PlayerEpisodes`, desktop-player f6:
  the season as the kit toolbar `Chip`, Close as the player's glass disc, the air date after the number); on a phone on
  its side (any window 500 or less high) the drawer takes the whole window so a row of cards fits; upright, Episodes is
  the watch page's own tab under the picture (phone-player 4), so the case is that page. The Chapters drawer shares the
  surface and its Close.
- **Moved into the kit:** `PlayerStillWatching` (`kit/player/StillWatching.tsx` with its CSS; `screens/watch/StillWatchingPrompt`
  keeps the policy, the opening and the actions). Playback info was already the kit's `PlaybackInfo` (`kit/media`).
- **Dock and casting:** the dock family's own case (`dev/families/dock`, `galleryCase`) at the foot of a page
  (`player-dock`, `-dock-music`, `-casting`, `-casting-connecting`). The video dock's art slot is the live picture and
  stays black with no video, by the dock's rule (never a poster in place of the picture).
- Cases added: `player-still-watching`, `player-info`, `player-dock*`, `player-casting*`; `player-episodes` per form.
