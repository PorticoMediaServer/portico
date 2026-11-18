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
| A menu, picker, confirmation or short task | `SheetFrame kind="task"` with its R9 `task` | the rows |
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
- The upright form is chosen by the window's shape (`usePlayerUpright`: narrower than 4:3), so a tablet in portrait
  and an open foldable get the 16:9 strip with the watch page under it, not only a phone.
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
trigger and leaves it in view; a modal one dims the page. The TV column is the TV builder's (family G); until it is
filled a TV cell is `null` and the kit keeps its TV form.

| Task | Phone | Tablet (medium) | Wide (expanded, web) | TV |
| --- | --- | --- | --- | --- |
| `actions` (More / long press) | sheet, modal; bar: title + item · Done | popover beside the card, modal | popover, not dimmed | TV builder |
| `choice` (select list, switcher, sort) | popover under the trigger, modal | popover, modal | popover, not dimmed | TV builder |
| `confirm` (Delete…?) | sheet; foot halves (Cancel, the action; destructive is a red fill) | dialog; foot at the end | dialog; foot at the end | TV builder |
| `task` (Add source, Save as playlist) | sheet; bar: Cancel · centred title; foot full (the action) | side panel, modal; title · round ×; foot halves | dialog; title · round ×; foot at the end | TV builder |
| `details` (File info) | sheet; bar: title + item · Done | side panel; title + item · round × | side panel | TV builder |
| `filters` | sheet; title · Done; foot halves | side panel; title · round ×; foot halves | side panel | TV builder |
| `settings` | full sheet; index "Settings · Done", a page "‹ Settings · centred title · its action" | a page beside the list | a page beside the list (web: full page at phone width, ruled) | TV builder |
| `account` | sheet; title · Done | popover under the avatar | popover under the avatar | TV builder |
| `notifications` | a page in the Settings sheet: "‹ Settings · Notifications · Mark all read" | popover under the bell | popover under the bell | TV builder |

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
