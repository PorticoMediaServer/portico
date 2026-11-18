# Screen frames (native)

The brief for building a screen. The rules are in the design package (`design/ui-v1`: `system-rules.html`,
`system-frames.html`, `notes/system-rules.md`); their numbers are data in `packages/design/src/frames.ts`; the
frames here apply them. The web has the same frames with the same names in `web/src/kit/frames`.

A screen picks one frame and fills its slots. It never draws a backdrop, a header, page padding, a gutter, the
space between sections, or a page-level loading or error state: the frame does. Each frame has fixed regions,
which never move, and one flexible region, the only place that may change when content is missing or added.

## The rules (round 8, 18 Nov)

Every rule is data in `packages/design` and is inherited; a frame or a screen never types one of these numbers or
colours. Names and values: `packages/design/src/scale.ts`, `index.ts` (`formScale`) and `frames.ts`.

- **R2 form scale.** Type, card, button and row sizes come from the FORM (`phone | medium | expanded | tv`), not the
  width: one table, `formScale` (form x role). `frameForm(width, isTV, height)` is the one test and `createTheme` uses it,
  so the theme and the frames cannot disagree. A touch window from 1024 wide is `expanded`: a tablet, with the tablet's
  own column (page title 44, row heading 21, poster 150, button 48), never the web's. Read `theme.form`,
  `theme.type[role]`, `theme.scale` (`.card`, `.button`, `.row`, `.icon`) and, for the component spec's other sizes,
  `theme.spec.x[theme.column]`. `theme.platform` and `theme.layoutMode` name the STRUCTURE only (`'web'` / `'desktop'`
  is the wide arrangement a tablet shares with the desktop); never index a size by them.
- **R3 emphasis.** Four levels: primary, secondary, tertiary, accent (`theme.emphasis`). A text role with a level
  carries its colour in `theme.type[role]` (`rowHelp` tertiary, `intro` and `meta` secondary, `eyebrow` accent...), so
  text drawn with a role needs no colour. A value at its default is tertiary and medium, a chosen one accent and bold
  (`emphasis.value`, `.valueChosen`); a control that is on is accent (`emphasis.active`); a row's label is `rowLabel`
  (semibold); "S1 E2" is `playerSubtitle` and the episode's name `playerSubtitleStrong`; progress and the New badge are
  at accent strength (`emphasis.progress`, `.badge`).
- **R4 icon classes.** `primary` (the transport), `secondary` (shuffle, repeat, tool glyphs), `tertiary` (inline
  glyphs): a size per form and a level of emphasis each (`theme.scale.icon`). The kit `Button` takes `iconClass` and
  `active`; dimming is the level's colour, never an opacity.
- **R5 rhythm.** The frame is the one owner of the space between sections. On a frame (`FrameRhythm`) a kit `Row` adds
  no spacing of its own; the frame applies `rowSection` and `rowHeadingGap` (rows of cards) and `sectionGap` and
  `headingGap` (other blocks) from `useFrameMetrics()`. A card's caption is a reserved two-line slot
  (`theme.scale.card.caption`, `cardCaptionHeight(theme)`), so a row's height includes its captions.
- **The Android line.** A focus ring is drawn only for keyboard or D-pad focus, never for focus a touch handed over
  (`Focusable`, and the cards). A bold title's line box is never tighter than 1.2 of its size on Android
  (`nativeType`); sheet and menu titles have a line box of their own. A sheet's footer adds the bottom inset.
- **R10 layers.** See "The backdrop" below.

## Which frame

| Screen | Frame | Flexible region |
|---|---|---|
| Home | `BrowseFrame` with `hero={<HomeHero/>}` | the rows |
| A library, See all, any list or grid | `BrowseFrame` with `header` | the rows or grid |
| Search: before typing, results, no matches, TV with its keyboard | `SearchFrame` | the results |
| Live TV: the guide, the channel list, Recordings (Upcoming, Recorded, Series rules) | `LiveFrame` (its `details` is `SplitFrame`'s pane) | the guide, the list or the recordings |
| Saved: My List and its tabs | `BrowseFrame` with `header` (named use `saved`) | the rows or grid |
| Saved: Downloads | `SplitFrame` with Rules as its pane from tablet width, around `BrowseFrame` (named use `downloads`) | the rows |
| Music and Audiobooks libraries, Discover, Songs | `BrowseFrame` with `header` (named uses `music`, `audiobooks`) | the rows or grid |
| A page with a second pane: a programme, a rule, Downloads' Rules | `SplitFrame` | the page |
| The reader (books, comics, PDFs) | `ReaderFrame`: the web kit's, inside the reading guest's web view (`screens/read/NativeReadingGuestHost.tsx`); there is no native reader frame | the page |
| A film, show, season, person, album, artist, book, collection | `TitleFrame` | the synopsis and the tab panel |
| Settings (the list) | `SettingsListFrame` | the groups |
| One settings or owner page, a form | `SettingsPageFrame` | the sections |
| A menu, picker, confirmation or short task | `SheetFrame kind="task"` with its R9 `task` | the rows |
| An editor that takes the whole phone | `SheetFrame kind="full"` | its page |
| Welcome, sign in, local server, codes, profiles | `SignInFrame` (TV: the existing `tv/auth/TVAuthLayout`) | the form |
| The video player | `PlayerChromeFrame` | none |
| Now playing (music, audiobooks) | `AudioPlayerFrame` | `panel`: the queue, chapters or Lyrics |
| The whole page can't load | the frame's own `state`, or `PageStateFrame` | the sentence |

## Props

- `BrowseFrame` (its regions are `frameRegions.browse` and `.home` in packages/design, drawn below per form): `header: {title,
  back?, trailing?, switcher?, menu?, tabs?, intro?, actions?}` or `hero` (Home; `null` while there is nothing to feature);
  `toolbar?`; `filters?: {open, onClose, onClear, count?, onDone, save?, children}`; one flexible region, `children` (kit `Row`s;
  the frame scrolls and spaces them) or `grid` (a self-scrolling kit `WindowedGrid`) with `alphabet?`; `state?: {kind:
  'loading'} | PageStateProps`. A `Row` draws its own heading; `Section` is only for a block that is not a row of cards, and
  a `Row` never goes inside one.
- `HomeHero`: the kit `Hero`'s data and render props only (`candidates`, `renderArtwork`, `renderActions`, ...), and on TV
  `focusedEntry` and `hints`: the hero follows the focused card (owner, 18 Nov), with no spotlight, buttons or pager, and the
  key hints under its words. Its sizes are rule 14's.
- `TitleFrame`: `art` (the kit `AmbientArt treatment="title"`), `back`, `topAction?`, `title`, `metadata?` (facts and the
  rating badge), `primary` (with `progress` and `remaining` it is Resume, with the played part along its lower edge),
  `actions?` (in order: `labelled` ones such as Start over and My List, then round ones such as watched and More; one
  the title doesn't support stays with `disabled`), `tagline?`, `synopsis?`, `credits?` (Directed by, Starring), `tabs?`,
  `children` (the tab panel), `state?`.
- `SettingsListFrame`: `bar`, `open`, `onOpenChange`, `list`, `page?`.
- `SettingsPageFrame`: `title`, `back?`, `action?`, `intro?`, `children` (`SectionCard`s), `state?`.
- `SheetFrame`: `open`, `onOpenChange`, `children`, and `kind: 'task'` with `task` (R9, below), `title`, `subtitle?`,
  `action?`, `foot?`, `anchorRef?`, or `kind: 'full'` with `bar?`.
- `SignInFrame`: `back?`, `title`, `lede?`, `error?`, `children`, `terms?`.
- `PlayerChromeFrame`: the regions in `frameRegions.player`: `picture`, `close`, `title?`, `tools?`, `scrubber`, `times`,
  `transport`, `leading?`, `trailing?`, `prompts?`, `card?`, `panel?`, `hints?`, `body?`; states `chromeVisible`, `scrubbing`.
- `AudioPlayerFrame`: the regions in `frameRegions.audio`: `close`, `context?` (`source?`), `device?`, `more`, `notice?`,
  `artwork?` (`icon`), `title`, `badge?`, `byline?` (parts), `favourite?`, `titleMore?`, `chapter?`, `scrubber`, `times`,
  `transport` (five slots), `volume?`, `next?`, `lyrics?`, `queue?`, `playlist?`, `speed?`, `sleep?`, `chapters?`, `panel?`; `loading?`.
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

The built video player (`screens/watch/WatchChrome.tsx`, `WatchTimeline.tsx`, `TrickplayStrip.tsx`)
still draws its own versions; they are bound to the playback runtime. Stage 2 moves that screen onto the frame and
these parts and deletes its private drawing. Apply the same test to any other screen that needs more than stock
parts (the reader, the guide grid, the equaliser): name the parts, put them in the kit, draw them in the Kit page.

The player's own panels are kit parts too, small ones: a header and a list.

| Part | What it is | Form |
| --- | --- | --- |
| `PlayerPanel` | A panel's header (title, back, close, a note) and its scrolling list | A column on the picture's right edge on a tablet and TV; a sheet from the bottom on a phone |
| `PlayerPanelGroup` | One list in a panel: its label, the current choice beside it, its rows on one card | |
| `PlayerPanelRow` | A choice (ticked when selected, dimmed in place when unavailable) or an options entry (icon, current value, chevron) | |
| `PlayerUpNext` | The Up next card over the credits. Moved from `screens/watch/UpNextCard.tsx`; the watch screen draws this same part | TV card; `compact` on a phone and tablet |
| `PlayerLiveActions` | Live TV: Record and More at the top of the player. Moved from `screens/watch/TVLiveActions.tsx`; the watch screen draws this same part | |
| `PlayerChannelStep` | Live TV: channel down or up with the channel it goes to | |

Guide is the kit Button and Go to live is a `PlayerPill`, in the frame's `leading` and `trailing` places.

Changed in the fix round after the first device captures (the reviewer's S-1 to S-18):

- `PlayerChromeFrame` takes `panel` and `card` and places them itself. A panel is a sheet under the picture in the
  upright form, a column beside the picture on a tablet (the picture and its controls take the room left), and lies
  over the picture's right side on a phone on its side and a TV, where the controls hide. `card` (Up next) sits alone
  in the picture's bottom right corner with the controls hidden. `scrubbing` hides the transport for the preview.
- The upright form is chosen by the window's shape (`usePlayerUpright`; round 8: taller than wide, so an open foldable
  gets the full-window player).
- `PlayerControl` is a plain glyph on a phone and tablet and sits on a disc only where the mock-ups draw one (a TV's
  controls, a tablet's top bar); `skip` draws 10 or 30 inside the arrow; `plain` is TV's bare shuffle and repeat.
- `PlayerPill` with a `name` is a two-line tile on TV. `PlayerScrubBar` has the spec's small knob, `tone="white"` and
  `knob={false}` for the audio player, and `liveEdge` for live TV. `PlayerTitle` takes `mark`, `live` and `detail`.
- `PlayerPanel` fills the box the frame gives it (its top no longer moves with its content), has a grab handle as a
  sheet, and is titled by its own list. `PlayerPanelGroup` rules its rows with hairlines and takes a `note`;
  `PlayerPanelRow` marks the choice with its tick alone and takes `qualifier` and `trailing`.
- An episode row is the kit `EpisodeTile layout="row"` and the bar over the list is the kit `SeasonBar` (moved from
  `screens/watch/TitleSeasonBar.tsx`; the title screen draws the same part). `PlayerEpisodeRow` is gone.
- Kit `Button`: no label is shrunk to fit any more (`adjustsFontSizeToFit` drew labels at 4 to 6 px on devices); an
  icon-only button keeps the button's corners unless it is the round `icon` variant. Kit `Tabs`: the rule runs the
  page's width. Kit `HeroMetadata`: facts with the ids `runtime`, `seasons` and `rating` are outlined chips.
- `AudioPlayerFrame` is re-laid per form (the title follows the artwork; a tablet keeps the column beside an Up next
  panel under a bar across the window) and takes `favourite`. `SettingsListFrame` gives its list a column, so cards in
  it start at the column's edge. `TitleFrame`'s action row has the page's width.

Changed in the second fix round (r5):

- TV scrubbing (tv-player 2): `PlayerFilmstrip` (five pictures, the landing one large), `PlayerScrubBar` `filmstrip` and `labels` (chapter names under the bar), and the frame's `hints` (kit `KeyHints`, in the controls row's place while scrubbing). The watch screen's `NativeTrickplayStrip` now draws through `PlayerFilmstrip` and only fills its tiles from the trickplay sheets. `screens/home` `HomeRemoteHint` still draws its own key caps: Stage 2 moves it onto `KeyHints`.
- `AudioPlayerFrame`: `source` (what it is playing from, said strongly), `chapter` (an audiobook's line over the scrubber), `next` as a label and a title, options as tiles when they carry a value, `inline` for the device, and labelled buttons on TV. `PlayerTimes` takes `middle`. The queue in the gallery is the kit `SongRow`.
- `TitleFrame` loading reserves the progress line and the words' lines, so the tabs do not move when the page arrives.
- The Settings index in the gallery is the shell's own `SettingsNav` and `ShortcutTiles`, passed as the frame's `list`.

Live screens whose look changes with this foundation (they are in the app today, not only in the Kit):

- every native hero's metadata line: the rating, runtime and season count are outlined chips;
- every native `Tabs`: the rule runs the page's full width;
- every native labelled `Button`: a label that does not fit on one line wraps to a second, and the button grows; none shrinks or is cut;
- the glass icon-only buttons on `BookScreen`, `AuthorSeriesScreen` and `CollectionScreen`: a rounded square, not a circle;
- TV scrubbing: the strip is the kit `PlayerFilmstrip` (no tile shadows; its times in the player's type roles);
- native TV Live: Record's glyph is red;
- an episode row that is playing has its title in the accent.

Changed in the final round before the owner's review (r6):

- `AudioPlayerFrame`: the artwork, title, scrubber and transport are one group. What is left of the height is under the group on a phone and around it on a tablet; on TV the words sit against the scrubber. A TV's buttons wrap to a second row before any label is cut.
- `TitleFrame`: on a tablet the picture is shaded behind the words (`frameSpec.hero.title.shade`); `TitleAction.active` tints an action that is on (in My List).
- `PlayerScrubBar`: the filmstrip's landing picture stays over the knob. `PlayerChromeFrame`'s `body` under an upright live picture is the channel's page (the gallery draws one).
- Kit: `SettingsNav`'s TV group no longer clips its rows' focus ring, and `SettingsListFrame` leaves the ring its room; the server card's name takes two lines; a banner's action never shrinks under its message; an episode row that is playing carries the playing mark on its picture; a poster's progress bar sits inside the art's edges.
- Gallery: a wide tablet counts as a tablet for which sections it has (its sections showed "not drawn on this kind of device" in landscape); on Android a section that is not a whole screen keeps clear of the status bar; TV pages draw the shell's own rail. TV sign-in is the `tv-account-*` sections of the accounts family.

Left for Stage 2, listed so it is not forgotten:

- `screens/watch/WatchPanels.tsx` (options, Subtitles, Audio, Quality, Speed, sleep timer, subtitle style) and
  `EpisodePanel.tsx` still draw their own header, groups and rows. They read the playback adapter and the title
  controller directly, so they could not be moved as they are. Stage 2 puts them on `PlayerPanel`, `PlayerPanelGroup`,
  `PlayerPanelRow` and `PlayerEpisodeRow` and deletes their private rows.
- `WatchChrome.tsx` still draws its own channel down and up buttons and Go to live; Stage 2 replaces them with
  `PlayerChannelStep` and the pill.
- The short guide (`TVMiniGuide.tsx`, `HandheldMiniGuide.tsx`) is still the screen's: it opens in its own modal layer
  and reads the guide directly.
- `PlayerUpNext` and `PlayerLiveActions` keep the sizes and tints they had in the screen. Reading them from
  `frameSpec.player` is Stage 2 work.
- The TV panel opens each list in place under its heading (owner's decision, below): built into `PlayerPanelGroup` when the TV player moves in Stage 2.
- Audio player, still short of the mock-ups (`final3-desktop-13-now-playing.jpg`; tablet-play frames 9 and 10): the volume line under the transport, the queue as a panel with Up next and Lyrics tabs, and Save as playlist at its foot. The frame has the places (`body`, the side column); the parts are Stage 2.
- A card's own menu (long press, or ⋯ on a pointer) has no gallery case here: `detail-phone-5-menu-file-info.jpg` and `detail-tv-5c-episode-menu.jpg` draw it. Stage 2 adds the case with the first screen that opens one.

Round 7 (18 Nov): a wide tablet's hero and title artwork now fades out by its alpha mask as TV's does (`kit/artwork/Surfaces.tsx`), so the title page has no hard edge where the picture starts. The TV audio player is one centred row (artwork 560) with no empty lower half. The Record glyph (`i-rec`) is a solid dot.

## Round 8: the kinds of screen that had no frame

Each frame's required regions are data: `frameRegions` in `packages/design/src/frame-regions.ts` (keys `search`, `live`,
`split`, `reader`, and the named uses `saved`, `downloads`, `music`, `audiobooks`, each listing the BrowseFrame regions it
fills). Gallery cases: `frames-search-empty`, `-search-results`, `-search-none`, `-live-guide`, `-live-details`,
`-live-list`, `-live-upcoming`, `-live-recorded`, `-live-rules`, `-saved`, `-downloads`, `-music`, `-music-songs`,
`-audiobooks` (`dev/families/frames/newframes.gallery.tsx`).

- `SearchFrame`: `title?` (phone, before typing), `field`, `cancel?` (phone, with text), `keyboard?` (TV: the field and the on-screen keyboard in a column on the left), `scopes?` (the type chips with counts), `notice?` (part of a search ran late), `none?` (`{title, body?, action?}`: no matches, drawn large at the top of the results), `children` (the flexible region).
- `LiveFrame`: `title`, `back?` (a phone's Recordings page), `switcher?` (All channels), `facts?` (expanded: the source, channels, how far the guide reaches), `actions?` (a phone's List | Guide and Recordings; TV's Today, Now, Recordings), `tabs?` (Guide | Recordings; a phone's Recordings views), `chips?` (groups, or Recordings' views and storage), `lead?` (expanded: On now), `toolbar?` (day, 2 h steps, Now, sort, Grid | List), `children`, `details?` (a `FramePane`), `hints?`, `state?`.
- `SplitFrame`: `children` (the page, a frame) and `pane?` (`FramePane`: `title`, `eyebrow?`, `subtitle?`, `close`, `children`, `footer?`). From tablet width the pane is a raised column on the right and the page takes the rest; on a phone it is a bottom sheet; on TV the same column (Back closes it). Pane width is `frameSpec.pane.width` (`--frame-pane-width`).

Parts, the same names in both apps:

| Part | Where | What it is |
|---|---|---|
| `SearchHistory` | `kit/search` | Recent searches: rows with × on a phone, chips wider; Clear in the heading |
| `TopResult` | `kit/search` | The Top result: a page-wide card with a round Play on a phone, words beside the picture with the title's buttons wider |
| `GuideGrid` | `kit/live` | The guide: channel column, time ruler with now, rows of programmes, the now-line |
| `ChannelRow` | `kit/live` | One channel in the list: mark and number, what is on, time left and bar, what is next |
| `AiringRow` | `kit/live` | One airing: More airings, Later on, Coming up |
| `RecordingRow`, `ConflictCard` | `kit/live` | Upcoming and Recording now; a tuner conflict |
| `RuleCard` | `kit/live` | One series rule |
| `StorageMeter` | `kit/saved` | How much room is used: Downloads (and Recordings) |
| `DownloadRow` | `kit/saved` | One download: poster, facts, where it is, its bar, Play / Try again / More |

`Section` takes `subtitle?` (a quieter line beside the heading). 

Left for Stage 2, so it is not forgotten:
- `screens/search/SearchScreen.tsx` still draws its own Recent searches rows and Top result (its tests read them); `screens/settings-live/LiveScreen.tsx` and `RecordingsScreen.tsx` their own guide, list, rows and programme details; `screens/downloads/DownloadsScreen.tsx` its own rows and Rules. Each is bound to its controller, so each moves onto these parts with its screen.
- TV search's keyboard is the screen's `NativeTVKeyboard` (`screens/search/TVKeyboard.tsx`), drawn by the gallery from there: it reads destination focus, so it moves to the kit with the TV search screen.
- Music, Audiobooks and Saved are named uses of `BrowseFrame`: the library switcher, counts line, tabs, toolbar and grid are the library frame's regions (stage1/r8-library). Until they land, the gallery cases draw the kit `Tabs` at the head of the rows and rows in place of the grid.

## TV rules (round 8: R1, R9's TV column, R11)

Each is fixed once in the kit; every TV screen inherits it.

- **One focus look (R1).** Every focusable thing takes the kit `Focusable`'s ring: the 4 px ring, a 4 px ink gap and the
  glow. A free-standing control (button, card, chip, tab, tile, Up next) draws it outside itself and grows a little. A row
  (`focusKind="row"`: settings rows, player panel rows, queue and chapter rows, the Settings nav, server rows) sits in a
  card or column that clips, so it draws the same ring inside its own edges, over a raised fill, and does not grow. Nothing
  draws an edge line or its own focus fill. A TV side sheet's and the Settings list's scroll boxes leave a free-standing
  ring its room.
- **Current is not focus (R1).** The open page (the rail's accent bar left of the icon, on every page; Settings nav's accent
  band), the chosen option (an accent tick before the label in a TV panel list; the tick in a menu row) and an active
  control (accent glyph) stay drawn when focus is elsewhere. Focus is only the ring.
- **Start focus (R1).** Every frame takes `startFocus`: the `focusId` or `testID` of the control the page starts on. The
  frame wraps its content in `StartFocusScope` (`kit/focus`), and that control asks for focus through the
  same preferred ask every kit control has (`useFocusTarget`), which now asks again on the next frames until focus lands
  or anything else takes it (Android TV drops an ask made before the view can take focus). The rail is outside every
  frame, so a page never starts in it. Defaults: `TitleFrame` starts on its primary (Play or Resume, a focus stop even
  while loading); `PageStateFrame` on its primary action; a `Menu` on its first enabled row. Where a page starts:
  Home, the first Continue Watching card; a list, its first card; a title, Play or Resume; Settings, the current page in
  the nav; a sheet, menu or Playback options, the first row; a picker or a panel's list, its current choice (the first
  row when nothing is chosen); a confirmation, Cancel; the player, Play or Pause; Up next, Play now.
- **The rail (R1, S3, S6).** Closed, its clip leaves room for the current page's bar. Open, it is the solid panel over the
  dimmed page, which moves right (the shell's `tvRailScrim`, drawn by the shell and by the gallery's `Page`).
- **Overlays on TV (R9, TV column).** A menu or picker is never a pointer drop-down: every `Popover` and `Menu` on TV is
  the right-hand side sheet (`BottomSheet`'s side form, 640 wide): eyebrow (`headerContext`), title, full-width rows
  with the gap between them, and the Back/Close key hints at the foot. The only anchored TV menu is the rail's own
  profile flyout (`placement="right-end"`). A dialog stays a centred dialog that starts on Cancel. These are the TV
  column of `overlayForms` (packages/design): `SheetFrame kind="task"` reads its cell, and where the foot is `hints` it
  draws key hints in place of buttons.
- **Key hints (R11).** `KeyHints` takes one or more key caps per hint and wraps before it cuts. Hint regions: the Home
  hero (in the actions' place: OK Resume, Hold OK More options), `SettingsListFrame`'s `hints` under the list,
  `SheetFrame`'s `hints` in place of its footer on TV (OK Choose, Back Close), `PlayerPanel`'s foot (Back Close, always
  on TV), every TV side sheet (Back Close), and `PlayerChromeFrame`'s `hints` while scrubbing.
- **Page inset (S6).** A settings page drawn on its own on a tablet or TV is a pushed page: Back, the large title and its
  sections at the page's gutter (x = 200 beside the rail on TV), held to the page's column; beside its list it starts at
  the column.
- **No cut text (S9).** A TV queue or chapter row's title takes two lines and the row grows; a TV menu row's label takes
  two lines.

## The library and Home (round 8)

The owner's largest regression was the library: the frame had a title, Back and one button. The required regions are now
data (`frameRegions.browse`, `frameRegions.home`): each region's id is the frame's prop of that name, the forms that draw it and
the mock-up frames it comes from. How the native frame draws each:

| Region | Phone | Tablet upright (`medium`) | Tablet landscape (`expanded`) | TV |
| --- | --- | --- | --- | --- |
| `header.title`, `.switcher` | the top bar with the switcher (name ⌄) centred (`StickyBrowseHeader`) | the 24 pt name with the round switcher (`LibraryMediumHeader`) | the large title with the round switcher (`LibraryExpandedHeader`) | the title ▾ with the tabs beside it (`LibraryHeader`) |
| `header.back` | ‹ Libraries in the bar; Back on a plain page | a plain page only | a plain page only | the remote's Back |
| `header.trailing` | not drawn | Select, before ⋯ | Select, before ⋯ | not drawn |
| `header.menu` | ⋯ in the bar | ⋯ at the right | ⋯ at the right | not drawn (a card's Hold OK menu) |
| `header.tabs` | inside the bar, scrolling | under the title from the gutter, the rule across | same | beside the title |
| `header.intro`, `.actions` | a plain page (See all): the line under the title, Play and Shuffle under it | same | same | same |
| `toolbar` | the kit `BrowseToolbar`, pinned under the header | same | same | sort, Filters, Jump to, the quick chips; applied chips and the count on the second line |
| `filters` | a sheet (Filters, Clear all; Save view, Show 6 movies) | a pane over the dimmed grid (`FiltersOver`, 420) | a pane docked beside the grid (`FiltersDock`, 380) | a side sheet (680) |
| `grid` + `alphabet` | the grid scrolls itself under the toolbar; the rail is centred in the right gutter | same | same | the grid; no rail (Jump to) |
| `children` (rows) | the frame scrolls and spaces them | same | same | same |
| `state` | loading keeps the header, tabs and toolbar and draws the grid's boxes (or two rows' boxes) | same | same | same |
| `hero` (Home) | the moderate hero under the shell's wordmark bar (`PhoneHomeHeader`) | the moderate hero | the moderate hero | the focus-led hero with OK Resume / Hold OK More options; the first Continue Watching card has the start focus |

The Filters pane and its dock and over-the-grid hosts (`kit/browse/FiltersPane.tsx`), the two tablet header forms
(`kit/browse/Library.tsx`) and a card caption's height (`kit/cards/caption-metrics.ts` `cardCaptionHeight`) moved here from
`screens/library`, which draws them from the kit. A row's height includes its cards' captions (`captionHeight`), so no caption
is clipped (the TV review's S5); a row's heading and subtitle wrap and are never cut. Gallery: `dev/families/frames/library.gallery.tsx`.

Left for Stage 2: `LibraryScreen` still composes these parts itself and moves onto the frame's regions; its own
`LibraryCompactHeader` (the kit `StickyBrowseHeader` drawn again), `LibraryTabs` (the kit `Tabs` with a focus region) and
`LibraryResultCount` go then. The list view's row (`screens/library/LibraryListRow.tsx`) reads the runtime and has no case here.
`screens/home/HomeRemoteHint` still draws its own key caps (the kit `KeyHints` is the hero's `hints`).

## Decided by the owner (Justin, 18 Nov)

- **Skip intro on an upright phone** sits in the picture's bottom row between the time and the options. It never covers the transport and is hidden while a panel or Up next is open.
- **Scrubbing on a phone:** the transport hides while the scrubber is held, and the knob follows the previewed time.
- **Live TV on an upright phone:** Guide, Record and More are icons in the top row; channel down and up sit either side of the transport; the time, how far behind live, Go to live and quality are in the bottom row.
- **TV playback options open in place** under their headings, as the TV mock-up and today's TV app do. This is a requirement on the shared panel: it is built into `PlayerPanelGroup` when the TV player moves onto it in Stage 2. Until then the gallery's TV panel shows one list at a time.
- **Episodes panel:** a drawer on the web, a column on a tablet. Both stay.
- **Tablet title page:** the hero layout, the same as the web and a phone. tablet.html frame 7's header-bar layout is not used.
- **Button labels are never shrunk and never cut.** Buttons are sized so standard labels fit on every form; a long translation makes the button wider or takes a second line. An ellipsis on a button label is a fault. In the kit: no label is shrunk to fit, and every labelled `Button` lets its label take a second line, its height being a minimum (the face every button already had at larger text). `labelLines` raises the limit.
- Also settled: the Watch Together pill comes in Stage 2; phone transport is 76 / 76 / 58; TV player type takes the nearest roles; Start over is a button on a tablet; the synopsis is two lines; both missing-art looks stay; the tablet gutter is 28; web Settings is a full page at phone width; the audio queue uses the kit's song row; web cards carry their second line; the native backdrop comes in Stage 2.

## The backdrop (owner, 17 and 18 Nov)

ONE backdrop everywhere. `kit/shell/Layer.tsx` is the only file that draws it (#4189). A layer is anything that must
hide what is beneath it (the app root, a stack screen, the TV route layer, a full-window page over the shell); whoever
mounts one puts it on `Layer`. A frame, a screen and a page draw no backdrop and no page colour: they are transparent on
their layer. The exceptions are in that file and nowhere else: the audio player's layer is a glow from the playing
item's artwork, the video player's is black. A sheet, menu or dialog is a plain kit surface. The same file sets the
status bar (light content; hidden only on the full-window video player). `test/next-one-backdrop.test.ts` holds the rule.

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

Still in the screens, with why: the docked mini player and dock bar, picture in picture and casting are kit `dock`
parts with their own gallery family (`dock`), drawn from the shell's dock host; a native film end card is
`screens/watch/PostPlayScreen.tsx`, which loads its own data (Stage 2 splits the drawing from the read); the live
mini guide on native (`HandheldMiniGuide`, `TVMiniGuide`) and Playback info (`PlaybackInfoPanel`, web `kit/media
PlaybackInfo`) read the live guide and the runtime directly. The web's Live TV chrome is the kit `PlayerLive`, its
own chrome rather than this frame.
