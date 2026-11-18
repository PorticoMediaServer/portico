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
      rowOffset: {...forms(18, 18, 18, 20), web: -40},
    },
    title: {
      height: {...forms(330, 420, 420, 628), web: 560, mobileWeb: 290},
      lockup: {...forms(40, 52, 52, 100), web: 72},
      copyBottom: {...forms(0, 36, 36, 0), web: 64},
      synopsisLines: forms(2, 2, 2, 2),
      /** The fixed action row: 18 under the art box on a phone, under the synopsis inside it elsewhere. Its height is the hero button's. */
      actionGap: forms(18, 24, 24, 26),
    },
    lockupLine: .94, lockupTracking: -.045,
  },
  /** Settings list and page (frames 3 and 4). */
  settings: {listWidth: forms(0, 280, 280, 480), webListWidth: 296, gap: forms(0, 28, 32, 80), webGap: 64, pageMax: 952, paragraphMax: 720},
  /** Rule 2: a full-height sheet starts 62 from the top with corners of 16; a sheet's last row clears the home indicator by 34.
   * The bottom sheet's own surface, grab and scrim are the kit sheet's. */
  sheet: {fullTop: 62, fullRadius: 16, footerClear: 34},
  /** Frame 6: the centred column on a tablet. A phone uses its width; the web's column and inset are the auth stage's. */
  signIn: {column: forms(0, 380, 380, 0)},
  /** Frame 8 and rule 12: every region is placed from the frame's edges, so nothing moves when content is missing. */
  audio: {
    topBar: forms(46, 46, 46, 40),
    artwork: {...forms(334, 334, 460, 600), web: 520},
    inset: forms(28, 28, 80, 200),
    /** Two title lines and a byline, bottom-aligned. */
    titleSlot: forms(104, 104, 130, 330),
    /** The scrubber line and the time read-outs under it. */
    scrubber: forms(48, 48, 48, 72),
    transport: forms(80, 80, 80, 120),
    nextLine: forms(22, 22, 22, 32),
    bottomRow: forms(64, 64, 64, 90),
    /** Wide forms: the title, scrubber and controls column beside the artwork. */
    controlsWidth: {...forms(0, 0, 300, 500), web: 420},
    upNextWidth: {...forms(0, 0, 210, 360), web: 256},
    gap: {...forms(12, 12, 30, 80), web: 56},
  },
  /** Frame 7, the video player's chrome (desktop-player, tablet-play, phone-player and tv-player mock-ups; the web
   * values are the built web player's). Scrims are stops of the dim colour: alpha, then how far from the edge. */
  player: {
    edge: forms(14, 32, 40, 96), top: forms(4, 26, 30, 64), bottom: forms(8, 32, 24, 56),
    /** Scrubber to the time read-outs, read-outs to the controls row, and between controls at either end. */
    timesGap: forms(8, 8, 8, 10), controlsGap: forms(10, 8, 10, 22), endGap: forms(2, 6, 6, 18), titleGap: forms(6, 16, 18, 24),
    scrim: {top: [[.84, 0], [.38, 130], [0, 240]], bottom: [[.94, 0], [.62, 150], [0, 340]]},
    pill: {height: forms(34, 44, 34, 56), paddingX: forms(12, 16, 12, 22)},
    volumeWidth: 84,
    /** Tints of the text colour: the scrubber's track, what has buffered and a marked stretch; a pill's fill; a preview's ring. */
    tint: {track: .16, buffered: .26, mark: .5, pill: .1, previewRing: .9, previewBand: .6},
    /** The thumbnail over the scrubber while scrubbing, and how far above the bar it sits. */
    preview: {width: forms(178, 256, 256, 400), height: forms(100, 144, 144, 225), radius: forms(9, 10, 10, 14), lift: forms(22, 40, 30, 44)},
    /** Skip intro and Up next: the white prompt and the round dismiss beside it, and how far the corner sits above the bottom edge. */
    prompt: {height: forms(44, 52, 52, 72), radius: forms(10, 12, 12, 14), paddingX: forms(18, 24, 24, 36), dismiss: forms(0, 48, 44, 0), gap: 10, bottom: forms(26, 64, 64, 96)},
  },
  /** Rule 9: the in-section state row's tile, and the error tile's tint of the record colour. */
  stateRow: {tile: forms(30, 30, 30, 40), iconShare: .6, errorTint: .16},
  /** Rule 12: the no-artwork placeholder's icon is a third of the box's short side. */
  placeholder: {iconShare: 1 / 3, lineAlpha: .08},
} as const;
export type FrameSpec = typeof frame;
