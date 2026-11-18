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
| Now playing (music, audiobooks) | `AudioPlayerFrame` | `panel`: the queue, chapters or Lyrics |
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
- `SheetFrame`: `open`, `onOpenChange`, `title`, `footer?`, `children`.
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

## Decided by the owner (Justin, 18 Nov)

- **Skip intro on an upright phone** sits in the picture's bottom row between the time and the options. It never covers the transport and is hidden while a panel or Up next is open.
- **Scrubbing on a phone:** the transport hides while the scrubber is held, and the knob follows the previewed time.
- **Live TV on an upright phone:** Guide, Record and More are icons in the top row; channel down and up sit either side of the transport; the time, how far behind live, Go to live and quality are in the bottom row.
- **TV playback options open in place** under their headings, as the TV mock-up and today's TV app do. This is a requirement on the shared panel: it is built into `PlayerPanelGroup` when the TV player moves onto it in Stage 2. Until then the gallery's TV panel shows one list at a time.
- **Episodes panel:** a drawer on the web, a column on a tablet. Both stay.
- **Tablet title page:** the hero layout, the same as the web and a phone. tablet.html frame 7's header-bar layout is not used.
- **Button labels are never shrunk and never cut.** Buttons are sized so standard labels fit on every form; a long translation makes the button wider or takes a second line. An ellipsis on a button label is a fault. In the native kit: no label is shrunk to fit, and every labelled `Button` lets its label take a second line, its height being a minimum.
- Also settled: the Watch Together pill comes in Stage 2; phone transport is 76 / 76 / 58; TV player type takes the nearest roles; Start over is a button on a tablet; the synopsis is two lines; both missing-art looks stay; the tablet gutter is 28; web Settings is a full page at phone width; the audio queue uses the kit's song row; web cards carry their second line; the native backdrop comes in Stage 2.

## The backdrop (owner, 17 and 18 Nov)

ONE backdrop everywhere: `body` draws it (`kit/backdrop.css`). A frame, a screen and a page draw no backdrop and no page
colour. A sheet, menu or dialog is a plain kit surface. The exceptions: the audio player is a glow from the playing
item's artwork, the video player is black. Round 8 removes the remaining local draws (`ColourBackdrop` in boot, the
account pages and states, and three stylesheets that repeat the glow variables) and adds the guard the native app has.

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
  standalone page takes `--page-gutter`, which is now 28 from 600 to 1023.
- Settings at phone width is a full page with the compact bar (as the web is today), not a sheet as on the phone app.
- The kit `SettingsRow` is about 58 high at 1440 where rule 7 says 76, and a row of `Shelf` cards carries 24 of its
  own padding on top of the 28 or 44 between sections. Both are the shared components' own; fix them there.
- Not built, because nothing calls for it yet: a full-height sheet on the web.
- Measurements the pages do not give are filled in `packages/design/src/frames.ts` from the nearest drawn form;
  correct them there, nowhere else.

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
