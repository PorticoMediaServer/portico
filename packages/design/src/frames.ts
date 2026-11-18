/**
 * The screen frames' measurements: the system rules (design/ui-v1 `system-rules.html`, rules 3 to 14) as data.
 * Gutters are `layout.gutter` and type roles are `typeScale` (index.ts); this file holds what neither has.
 *
 * Written once here; the native frames (`apps/native/src-next/kit/frames`) read it directly and the web frames
 * (`web/src/kit/frames`) read its CSS projection (`--frame-*` in tokens.css). A screen never types one of these
 * numbers: it picks a frame, and the frame applies them.
 *
 * Forms are size classes, not devices: `phone` under 600 wide (a phone, a folded foldable, phone-width web) and a
 * phone on its side (wider than 600 but under 600 high); `medium` 600 to 1023 (a small tablet, an open foldable);
 * `expanded` 1024 and up (a large tablet, the web); `tv` the 1920 x 1080 ten-foot canvas. `frameForm` is the one
 * test: the native theme (`createTheme`) and the frames both call it, so they cannot disagree about a window.
 */

export type FrameForm = 'phone' | 'medium' | 'expanded' | 'tv';
type Forms<T> = Readonly<Record<FrameForm, T>>;
const forms = <T>(phone: T, medium: T, expanded: T, tv: T): Forms<T> => ({phone, medium, expanded, tv});
export function frameForm(width: number, isTV = false, height?: number): FrameForm {
  if (isTV) return 'tv';
  if (width < 600 || (height !== undefined && width < 1024 && height < 600)) return 'phone';
  return width < 1024 ? 'medium' : 'expanded';
}

export const frame = {
  /** Rule 4: on a phone a grouped card sits 16 from the edge and its text 14 inside it. On wider forms a card starts at the gutter. */
  card: {phoneInset: 16, padding: forms(14, 14, 24, 28), radius: forms(14, 14, 14, 20)},
  /** Rule 5 and R5. The frame is the one owner of the space between sections: a kit `Row`, `Shelf`, `Section` or
   * `SectionCard` adds no margin or padding of its own, and a screen types none.
   * `section` and `headingGap` are for a block that is not a row of cards (a grouped list, a paragraph, a grid): the
   * block's edge to the next heading, and a heading to its block.
   * `rowSection` and `rowHeadingGap` are for a row of cards: the bottom of one row's captions to the next row's
   * heading, and a heading to the cards' art. They are the mock-ups' drawn distances (kit.css draws rule 5's 28 and 8
   * and then pads its rows by 8 and 10, which is how a phone's rows come to sit 38 apart). */
  rhythm: {
    section: forms(28, 28, 44, 44),
    headingGap: forms(8, 8, 14, 22),
    rowSection: {...forms(38, 40, 40, 44), web: 58},
    rowHeadingGap: {...forms(12, 16, 16, 22), web: 24},
    /** The room a row keeps round its cards for a card's hover or focus growth and its shadow. The row takes it back
     * with a negative margin of the same size, so it is not part of the rhythm. */
    rowBleed: {...forms(10, 10, 10, 28), web: 14},
    headerGap: forms(12, 12, 16, 28),
    /** The page title's top edge: under the status bar on a phone, 60 on a tablet, 112 on the web (under the 76 nav), 64 on TV. */
    titleTop: {...forms(0, 60, 60, 64), web: 112},
    /** What the last content clears, on top of the tab bar (phone) or the window's bottom edge. */
    bottomClear: forms(12, 12, 88, 56),
    /** A list row with a second line; an in-section state row is this high. */
    rowTwoLine: forms(64, 64, 76, 84),
  },
  /** Rule 3. */
  header: {bar: 56, backRow: 44, wordmarkBar: 50},
  /** Rule 14, the moderate hero, in fixed sizes (never a share of the window). `mobileWeb` is the phone form in a browser. */
  hero: {
    home: {
      height: {...forms(470, 470, 390, 500), web: 640, mobileWeb: 410},
      lockup: {...forms(42, 42, 52, 100), web: 76, webLong: 60},
      copyBottom: {...forms(14, 14, 36, 36), web: 120},
      copyTop: {tv: 110},
      synopsisLines: forms(0, 0, 2, 2),
      /** Where the first row's heading starts after the hero's bottom edge. The web's first row rides up over the art's fade. */
      rowOffset: {...forms(18, 18, 18, 56), web: -40},
    },
    title: {
      height: {...forms(330, 420, 420, 628), web: 560, mobileWeb: 290},
      lockup: {...forms(40, 52, 52, 100), web: 72},
      // R5 interim (family C): TV's action row ends about 72 above the art box's edge, and its tabs start a double header gap under it (detail-tv-2).
      copyBottom: {...forms(0, 36, 36, 72), web: 64},
      tabsGap: forms(0, 0, 0, 56),
      synopsisLines: forms(2, 2, 2, 2),
      /** A tablet's words sit on the artwork: a shade of the ink behind their column, strongest at the gutter and gone by `end` of the width, and one rising from the bottom edge. Stops are alpha, then share. */
      shade: {side: [[.94, 0], [.78, .32], [0, .68]], foot: [[.9, 0], [0, .5]]},
      /** The tagline over the synopsis and the credits line under it are drawn inside the art box from tablet width. */
      creditsGap: 6,
      /** The fixed action row: 18 under the art box on a phone, under the synopsis inside it elsewhere. Its height is the hero button's. */
      actionGap: forms(18, 24, 24, 26),
      /** R5 interim (family C): a biography with Read more shows this many lines (desktop-detail f7, phone-detail d3). */
      readMoreLines: 4,
      /** A cover page's cover, its width per form (a poster is 1.5 times as tall): phone-app 13 and phone-detail d3 frame 7
       * (220 square, 180 poster, 104 circle), tablet f8, desktop-listen album, artist and book (340). R5 interim. */
      cover: {square: {...forms(220, 280, 300, 400), web: 340}, poster: {...forms(180, 220, 260, 340), web: 340}, circle: {...forms(104, 160, 300, 280), web: 340}},
      /** Between a cover and its words, side by side (desktop-listen 56), and the played line's width under them. R5 interim. */
      coverGap: 56, coverResumeWidth: 320,
      /** The side column beside the panel (desktop-listen 360). R5 interim. */
      side: 360,
      /** The panel's parts (kit `title-parts`), R5 interim: About's label column (desktop-detail f3 140, phone-detail d1
       * 104); cast circles (phone-detail 64, tablet and web 96, TV 150); Films in order's number column and still
       * (desktop-detail f8 56 / 106, phone 56, TV 140); Previous / Next's still (desktop-detail f5, 112 wide, 16:9); a
       * choice row's label and the narrowest its choices go (desktop-detail f4 120 / 200). */
      parts: {aboutLabel: forms(104, 140, 140, 140), castCircle: forms(64, 96, 96, 150), number: 56, numberedArt: forms(56, 106, 106, 140), neighbourArt: 112, choiceLabel: 120, choiceMin: 200},
    },
    /** A lockup's line box. The mock-ups' one-line lockups are set at .94; at that a title that wraps has its descenders in the next line's capitals, so the frames use 1.04. */
    lockupLine: 1.04, lockupTracking: -.045,
  },
  /** Settings list and page (frames 3 and 4). */
  settings: {listWidth: forms(0, 280, 280, 480), webListWidth: 296, gap: forms(0, 28, 32, 80), webGap: 64, pageMax: 952, paragraphMax: 720},
  /** Rule 2: a full-height sheet starts 62 from the top with corners of 16; a sheet's last row clears the home indicator by 34.
   * The bottom sheet's own surface, grab and scrim are the kit sheet's. */
  sheet: {fullTop: 62, fullRadius: 16, footerClear: 34},
  /** Frame 6: the centred column on a tablet. A phone uses its width; the web's column and inset are the auth stage's. */
  signIn: {column: forms(0, 380, 380, 0)},
  /** Frame 1, a library's fixed regions under its header (desktop-browse grid and filters, phone-app 4 to 6, tablet 3 to 5,
   * final3-tv-08). The regions are `frameRegions.browse`. R5 interim: these numbers move to the rules hand's rhythm table. */
  browse: {
    /** The toolbar's last line to the grid's first row. The toolbar's own top padding is the gap under the tabs' rule (kit `BrowseToolbar`). */
    gridGap: forms(4, 12, 26, 24), // R5 interim (desktop-browse .pgrid: 26 under the toolbar)
    /** The poster grid: the narrowest a column's card may be (the columns stretch to both gutters), and the gaps between
     * columns and rows (desktop-browse .pgrid 20 / 30; phone-app 5; tablet 4 and 5). Its loading boxes use the same. */
    grid: {card: forms(100, 130, 160, 200), columnGap: forms(10, 16, 20, 28), rowGap: forms(16, 22, 30, 36)}, // R2 and R5 interim
    /** Filters: a sheet on a phone; over the grid on an upright tablet (tablet 5, 420); a column docked beside the grid on a
     * landscape tablet (tablet 4, 380); the web's side panel (desktop-browse filters, 520); TV's side sheet (680). */
    filtersWidth: {...forms(0, 420, 380, 680), web: 520},
  },
  /** Frame 8 and rule 12: every region is placed from the frame's edges, so nothing moves when content is missing.
   * The phone (phone-app 14 and 15) is one column; the wide forms (desktop-listen "now" and "book-player", tablet-play
   * 9, 10 and 12, final3-tv-12) are their own layout: a column of the cover's width (title, scrubber with its times at
   * its ends, a compact transport, volume on the web) beside a raised queue panel. The backdrop is the layer's glow. */
  audio: {
    topBar: {...forms(46, 64, 64, 40), web: 84},
    /** A phone's and a TV's artwork size; a tablet's and the web's is the room there is, up to this. */
    artwork: {...forms(334, 370, 420, 600), web: 480},
    inset: forms(28, 40, 56, 96),
    /** A phone's audiobook cover is narrower than a song's (phone-app 15). */
    bookArtShare: .86,
    /** Two title lines and a byline, bottom-aligned (the phone; the wide forms let the title take its lines). */
    titleSlot: forms(96, 104, 104, 200),
    /** The scrubber line and the time read-outs under it. */
    scrubber: forms(48, 48, 48, 72),
    transport: forms(80, 80, 80, 120),
    nextLine: forms(22, 22, 22, 32),
    bottomRow: forms(64, 64, 64, 90),
    /** TV: the title and controls column beside the artwork (final3-tv-12: 540, the scrubber and transport 500). */
    controlsWidth: {...forms(0, 0, 0, 540), web: 0},
    /** The queue panel (desktop 452, tablet 380, upright tablet 366; TV's list starts at 1440 and runs to the safe edge). */
    upNextWidth: {...forms(0, 366, 380, 384), web: 452},
    /** Between the artwork column and the queue panel; on TV between the artwork and the title column. */
    gap: {...forms(24, 20, 20, 80), web: 32},
    /** The wide forms' column (R7): its gaps from the mock-ups. The cover to the title, the title to the scrubber, the
     * scrubber to the transport, the transport to the volume or the pill row. `bookArt` is the audiobook's cover beside its title. */
    wide: {
      titleGap: {...forms(0, 26, 26, 0), web: 30}, scrubberGap: {...forms(0, 18, 18, 0), web: 20}, transportGap: {...forms(0, 16, 16, 0), web: 18}, belowGap: {...forms(0, 18, 18, 0), web: 12},
      transportSpacing: {...forms(0, 18, 18, 0), web: 24}, timesWidth: 34, bookTimesWidth: 58, volume: 160,
      bookColumn: {...forms(0, 600, 600, 0), web: 660}, bookArt: {...forms(0, 250, 250, 0), web: 300}, bookArtGap: 30, chapterGap: 34, rowsGap: 22, rowLabel: 132,
      /** The panel's corners and inset from the window, the segmented tabs, a queue row and its artwork, a group label's padding. */
      panelRadius: 20, panelInset: {...forms(0, 20, 20, 0), web: 32}, tabs: 38, row: 60, rowArt: 44, rowArtRadius: 6, handle: 22, foot: 70,
    },
    /** R4 interim: the music transport's classes. Play or Pause's disc, Previous and Next (primary, white), shuffle and
     * repeat (secondary: smaller and dimmer), on each form. A phone's are the owner's 76 (phone-app 14). */
    icons: {
      primary: {...forms(76, 74, 74, 96), web: 74}, primaryIcon: {...forms(32, 30, 30, 44), web: 30},
      control: {...forms(56, 52, 52, 72), web: 50}, stepIcon: {...forms(30, 26, 26, 34), web: 26}, secondaryIcon: {...forms(24, 22, 22, 32), web: 22},
      /** The heart and More beside the title (tertiary class, at the control's size). */
      inlineIcon: forms(24, 22, 22, 28),
    },
    /** TV (final3-tv-12): the title is a display line, the queue rows are 88 with 72 artwork. */
    tv: {fromTop: 124, artTop: 230, titleTop: 50, scrubberTop: 360, transportTop: 442, moreTop: 576, scrubberWidth: 500, row: 88, rowArt: 72, rowArtRadius: 10, moreHeight: 64},
  },
  /** Frame 7, the video player's chrome (desktop-player, tablet-play, phone-player and tv-player mock-ups; R6: the web's
   * values are its mock-ups', desktop-player 1 and 7). Scrims are stops of the dim colour: alpha, then how far from the edge. */
  player: {
    edge: forms(14, 32, 40, 96), top: forms(4, 26, 30, 64), bottom: forms(8, 32, 24, 56),
    /** Scrubber to the time read-outs, read-outs to the controls row, and between controls at either end. */
    timesGap: forms(8, 8, 8, 10), controlsGap: forms(10, 8, 10, 22), endGap: forms(2, 6, 6, 12), titleGap: forms(6, 16, 18, 24),
    scrim: {top: [[.84, 0], [.38, 130], [0, 240]], bottom: [[.94, 0], [.62, 150], [0, 340]]},
    pill: {height: forms(34, 44, 34, 56), paddingX: forms(12, 16, 12, 22)},
    volumeWidth: 84,
    /** The glyph in a pill and in a prompt. */
    pillIcon: 16, promptIcon: 20,
    /** A phone held upright (phone-player 4): the picture is a 16:9 strip under the status bar with the watch page below it. */
    portrait: {aspect: 16 / 9, top: 4, edge: 6, bottom: 8, timeInset: 14, scrubber: 3, shade: .44, control: 46, controlIcon: 24, primary: 58, primaryIcon: 26,
      promptHeight: 34, promptPaddingX: 12,
      /** The web places the prompt from the strip's right edge, clear of the quality pill and full screen. */
      promptRight: 168},
    /** R8: the upright form is a window taller than wide (a phone, a tablet in portrait); an open foldable, wider than
     * tall, gets the full-window player (foldable 3). On the web a window this short is a phone on its side and takes
     * the phone's sideways forms, whatever its width (phone-player 1). */
    upright: {maxAspect: 1}, sidewaysMaxHeight: 500,
    /** R4 interim and R6: the transport's icon classes per form (desktop-player 1, tablet-play 1 and 3, phone-player 1
     * and 4, tv-player 1). All of it is primary: white. `primary` is Play or Pause's disc and glyph; `control` the other
     * buttons' box; `skipIcon` the 10 and 30 arrows, drawn larger than `stepIcon` (Previous, Next, the channel steps),
     * and `numeral` the seconds in them; `gap` between transport buttons; `toolIcon` the bar's own glyphs (subtitles,
     * picture in picture, full screen: secondary). `upright` is the strip's: a phone's, then a tablet's (tablet-play 3). */
    transport: {
      primary: forms(76, 76, 62, 88), primaryIcon: forms(32, 32, 28, 38), control: forms(58, 58, 48, 64),
      skipIcon: forms(40, 40, 28, 34), stepIcon: forms(30, 30, 22, 28), numeral: forms(11.5, 11.5, 8.5, 12), gap: forms(72, 72, 14, 22), toolIcon: forms(22, 22, 24, 30),
      upright: {phone: {primary: 58, primaryIcon: 26, control: 46, skipIcon: 34, stepIcon: 24, numeral: 9.5, gap: 26}, tablet: {primary: 84, primaryIcon: 32, control: 62, skipIcon: 40, stepIcon: 30, numeral: 11.5, gap: 44}},
    },
    /** R6: the scrubber's track (6 on the web and a tablet, 4 on a phone, 8 on TV), one continuous line; a marked stretch is tinted on it. */
    track: forms(4, 6, 6, 8),
    /** The scrubber's knob: a small dot. The numeral inside a skip arrow is `typeScale.playerSkip`. */
    knob: {...forms(14, 20, 20, 20), web: 18},
    /** A tablet's knob under the finger (tablet-play 1 inset) and a TV's while scrubbing (tv-player 2). */
    knobHeld: forms(22, 30, 30, 44),
    /** TV scrubbing (tv-player 2): five pictures over the bar, the middle one large; chapter names under the bar; key hints in the controls row's place. */
    filmstrip: {small: {width: 256, height: 144}, large: {width: 480, height: 270}, gap: 20, radius: 12, lift: 12, dim: .78, labelGap: 12},
    /** TV: an options entry is a two-line tile (its name over its value) with the card's corners (tv-player 1). */
    tile: {radius: 14, paddingX: 16},
    /** With a panel open the TV picture is dimmed by this much of the dim colour. */
    panelDim: .5,
    /** Tints of the text colour: the scrubber's track, what has buffered and a marked stretch; a pill's fill; a preview's ring. */
    tint: {track: .16, buffered: .26, mark: .5, pill: .1, open: .2, previewRing: .9, previewBand: .6},
    /** The thumbnail over the scrubber while scrubbing, and how far above the bar it sits. */
    preview: {width: forms(178, 256, 256, 400), height: forms(100, 144, 144, 225), radius: forms(9, 10, 10, 14), lift: forms(22, 40, 30, 44)},
    /** The player's own panels (options, Subtitles, Audio, Quality, Speed, Episodes): a column on the picture's right edge from
     * tablet width (tablet-play 2, tv-player 4), a sheet from the bottom on a phone (phone-player 3). `still` is an episode row's picture. */
    panel: {width: forms(356, 380, 380, 680), padding: forms(16, 24, 24, 56), row: forms(52, 52, 52, 64), still: forms(112, 132, 132, 200), iconTile: forms(30, 30, 30, 44), grab: {width: 36, height: 5, top: 8}},
    /** Skip intro and Up next: the white prompt and the round dismiss beside it, and how far the corner sits above the bottom edge. */
    prompt: {height: forms(44, 52, 52, 72), radius: forms(10, 12, 12, 14), paddingX: forms(18, 24, 24, 36), dismiss: forms(0, 48, 44, 0), gap: 10, bottom: forms(26, 64, 64, 96)},
  },
  /** A page's second pane (`SplitFrame`): its width beside the page from tablet width (tablet-play 6 and 7, final3-tv-10;
   * the web from 1024 is desktop-live live-sheet). A phone draws the pane as a sheet. */
  pane: {width: {...forms(0, 380, 420, 640), web: 600}},
  /** TV Search: the keyboard column beside the results (final3-tv-09: 416 of 1600). */
  search: {keyboardWidth: 500},
  /** The Live TV guide grid: minutes to points (phone-guide g1, tablet-play 6, desktop-live live-guide, final3-tv-10), the
   * channel column, a row, a channel's mark, the time column of a list of airings, the ruler, the gap between cells. */
  guide: {
    perMinute: {...forms(3, 4, 4, 8), web: 8}, channel: {...forms(56, 176, 176, 300), web: 220}, row: {...forms(72, 72, 72, 100), web: 76},
    mark: forms(36, 40, 40, 56), timeColumn: forms(76, 76, 76, 120), ruler: 36, gap: 4, radius: 8, nowPill: 120,
  },
  /** The picture beside a row's words: a recording's 16:9 (a phone's row has none, phone-app 12) and a download's poster (tablet 11b, collections-34). */
  rowArt: {recording: forms(0, 132, 160, 220), download: forms(44, 48, 48, 72)},
  /** Rule 9: the in-section state row's tile, and the error tile's tint of the record colour. */
  stateRow: {tile: forms(30, 30, 30, 40), iconShare: .6, errorTint: .16},
  /** Rule 12: the no-artwork placeholder's icon is a third of the box's short side. */
  placeholder: {iconShare: 1 / 3, lineAlpha: .08},
} as const;
export type FrameSpec = typeof frame;
