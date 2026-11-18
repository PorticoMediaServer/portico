/**
 * The screen frames' measurements: the system rules (design/ui-v1 `system-rules.html`, rules 3 to 14) as data.
 * Gutters are `layout.gutter` and type roles are `typeScale` (index.ts); this file holds what neither has.
 *
 * Written once here; the native frames (`apps/native/src-next/kit/frames`) read it directly and the web frames
 * (`web/src/kit/frames`) read its CSS projection (`--frame-*` in tokens.css). A screen never types one of these
 * numbers: it picks a frame, and the frame applies them.
 *
 * Forms are widths, not devices: `phone` under 600 (a phone, a folded foldable, phone-width web), `medium`
 * 600 to 1023 (a small tablet, an open foldable), `expanded` 1024 and up (a large tablet, the web), `tv` the
 * 1920 x 1080 ten-foot canvas.
 */

export type FrameForm = 'phone' | 'medium' | 'expanded' | 'tv';
type Forms<T> = Readonly<Record<FrameForm, T>>;
const forms = <T>(phone: T, medium: T, expanded: T, tv: T): Forms<T> => ({phone, medium, expanded, tv});
export function frameForm(width: number, isTV = false): FrameForm {
  return isTV ? 'tv' : width < 600 ? 'phone' : width < 1024 ? 'medium' : 'expanded';
}

export const frame = {
  /** Rule 4: on a phone a grouped card sits 16 from the edge and its text 14 inside it. On wider forms a card starts at the gutter. */
  card: {phoneInset: 16, padding: forms(14, 14, 24, 28), radius: forms(14, 14, 14, 20)},
  /** Rule 5. */
  rhythm: {
    section: forms(28, 28, 44, 44),
    headingGap: forms(8, 8, 14, 22),
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
      copyBottom: {...forms(0, 36, 36, 0), web: 64},
      synopsisLines: forms(2, 2, 2, 2),
      /** A tablet's words sit on the artwork: a shade of the ink behind their column, strongest at the gutter and gone by `end` of the width, and one rising from the bottom edge. Stops are alpha, then share. */
      shade: {side: [[.94, 0], [.78, .32], [0, .68]], foot: [[.9, 0], [0, .5]]},
      /** The tagline over the synopsis and the credits line under it are drawn inside the art box from tablet width. */
      creditsGap: 6,
      /** The fixed action row: 18 under the art box on a phone, under the synopsis inside it elsewhere. Its height is the hero button's. */
      actionGap: forms(18, 24, 24, 26),
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
  /** Frame 8 and rule 12: every region is placed from the frame's edges, so nothing moves when content is missing. */
  audio: {
    topBar: forms(46, 46, 46, 40),
    /** A phone's and a TV's artwork size; a tablet's and the web's is the room there is, up to this. */
    artwork: {...forms(334, 600, 600, 560), web: 600},
    inset: forms(28, 40, 56, 96),
    /** Two title lines and a byline, bottom-aligned. */
    titleSlot: forms(96, 104, 104, 200),
    /** The scrubber line and the time read-outs under it. */
    scrubber: forms(48, 48, 48, 72),
    transport: forms(80, 80, 80, 120),
    nextLine: forms(22, 22, 22, 32),
    bottomRow: forms(64, 64, 64, 90),
    /** Wide forms: the title, scrubber and controls column beside the artwork. */
    /** TV: wide enough for its three labelled buttons at their full labels. */
    controlsWidth: {...forms(0, 0, 300, 600), web: 420},
    upNextWidth: {...forms(0, 280, 340, 360), web: 360},
    gap: {...forms(12, 12, 30, 64), web: 56},
  },
  /** Frame 7, the video player's chrome (desktop-player, tablet-play, phone-player and tv-player mock-ups; the web
   * values are the built web player's). Scrims are stops of the dim colour: alpha, then how far from the edge. */
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
    portrait: {aspect: 16 / 9, top: 4, edge: 6, bottom: 8, timeInset: 14, scrubber: 3, shade: .44, control: 38, controlIcon: 22, primary: 58, primaryIcon: 26,
      /** A window narrower than this for its height is upright: a phone, a tablet in portrait, an open foldable (tablet-play 3). */
      maxAspect: 4 / 3, promptHeight: 34, promptPaddingX: 12,
      /** The web places the prompt from the strip's right edge, clear of the quality pill and full screen. */
      promptRight: 168},
    /** The scrubber's knob: a small dot. The numeral inside a skip arrow is `typeScale.playerSkip`. */
    knob: forms(12, 14, 14, 20),
    /** TV scrubbing (tv-player 2): five pictures over the bar, the middle one large; chapter names under the bar; key hints in the controls row's place. */
    filmstrip: {small: {width: 256, height: 144}, large: {width: 480, height: 270}, gap: 20, radius: 12, lift: 12, dim: .78, labelGap: 12},
    /** TV: an options entry is a two-line tile (its name over its value) with the card's corners (tv-player 1). */
    tile: {radius: 14, paddingX: 16},
    /** With a panel open the TV picture is dimmed by this much of the dim colour. */
    panelDim: .5,
    /** Tints of the text colour: the scrubber's track, what has buffered and a marked stretch; a pill's fill; a preview's ring. */
    tint: {track: .16, buffered: .26, mark: .5, pill: .1, previewRing: .9, previewBand: .6},
    /** The thumbnail over the scrubber while scrubbing, and how far above the bar it sits. */
    preview: {width: forms(178, 256, 256, 400), height: forms(100, 144, 144, 225), radius: forms(9, 10, 10, 14), lift: forms(22, 40, 30, 44)},
    /** The player's own panels (options, Subtitles, Audio, Quality, Speed, Episodes): a column on the picture's right edge from
     * tablet width (tablet-play 2, tv-player 4), a sheet from the bottom on a phone (phone-player 3). `still` is an episode row's picture. */
    panel: {width: forms(356, 380, 380, 680), padding: forms(16, 24, 24, 56), row: forms(52, 52, 52, 64), still: forms(112, 132, 132, 200), iconTile: forms(30, 30, 30, 44), grab: {width: 36, height: 5, top: 8}},
    /** Skip intro and Up next: the white prompt and the round dismiss beside it, and how far the corner sits above the bottom edge. */
    prompt: {height: forms(44, 52, 52, 72), radius: forms(10, 12, 12, 14), paddingX: forms(18, 24, 24, 36), dismiss: forms(0, 48, 44, 0), gap: 10, bottom: forms(26, 64, 64, 96)},
  },
  /** Rule 9: the in-section state row's tile, and the error tile's tint of the record colour. */
  stateRow: {tile: forms(30, 30, 30, 40), iconShare: .6, errorTint: .16},
  /** Rule 12: the no-artwork placeholder's icon is a third of the box's short side. */
  placeholder: {iconShare: 1 / 3, lineAlpha: .08},
} as const;
export type FrameSpec = typeof frame;
