/**
 * The screen frames' measurements: the system rules (design/ui-v1 `system-rules.html`, rules 3 to 14) as data.
 *
 * Written once here; the native frames (`apps/native/src-next/kit/frames`) read it directly and the web frames
 * (`web/src/kit/frames`) read its CSS projection (`--frame-*` in tokens.css). A screen never types one of these
 * numbers: it picks a frame, and the frame applies them.
 *
 * Forms are widths, not devices: `phone` under 600 (a phone, a folded foldable, phone-width web), `medium`
 * 600 to 1023 (a small tablet, an open foldable), `expanded` 1024 and up (a large tablet, the web), `tv` the
 * 1920 x 1080 ten-foot canvas.
 */
import type {TypeStyle} from './index.ts';

export type FrameForm = 'phone' | 'medium' | 'expanded' | 'tv';
type Forms<T> = Readonly<Record<FrameForm, T>>;
const forms = <T>(phone: T, medium: T, expanded: T, tv: T): Forms<T> => ({phone, medium, expanded, tv});
const role = (size: number, lineHeight: number | 'normal', weight: TypeStyle['weight'], tracking = 0): TypeStyle => ({size, lineHeight, weight, tracking});
/** A type role per column: phone and tablet, the web at 1024 and up, TV. */
type Role = Readonly<{handheld: TypeStyle; web: TypeStyle; tv: TypeStyle}>;
const roles = (handheld: TypeStyle, web: TypeStyle, tv: TypeStyle): Role => ({handheld, web, tv});

export function frameForm(width: number, isTV = false): FrameForm {
  return isTV ? 'tv' : width < 600 ? 'phone' : width < 1024 ? 'medium' : 'expanded';
}

export const frame = {
  /** Rule 4. The web grows from 32 at 1024 to 64 at 1440; a native tablet at `expanded` stays at 32. */
  gutter: {...forms(20, 28, 32, 200), web: 64, tvRight: 96},
  /** Rule 4: on a phone a grouped card sits 16 from the edge and its text 14 inside it. Wider forms line cards up with the gutter. */
  card: {inset: forms(16, 28, 32, 200), padding: forms(14, 14, 24, 28), radius: forms(14, 14, 14, 20)},
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
  /** Rule 6: a screen picks a role, never a size. Colours are the roles' own. */
  type: {
    pageTitle: roles(role(34, 38, 800, -.03), role(56, 60, 800, -.03), role(44, 50, 800, -.03)),
    barTitle: roles(role(17, 'normal', 700), role(17, 'normal', 700), role(17, 'normal', 700)),
    sectionHeading: roles(role(19, 'normal', 700, -.015), role(22, 'normal', 700, -.015), role(32, 'normal', 700, -.015)),
    groupLabel: roles(role(15, 'normal', 700), role(22, 'normal', 700, -.015), role(20, 'normal', 700)),
    listLabel: roles(role(15, 'normal', 700), role(14, 'normal', 700), role(20, 'normal', 700)),
    rowLabel: roles(role(15.5, 20, 600), role(17, 22, 600), role(25, 32, 600)),
    rowHelp: roles(role(12.5, 17.5, 500), role(15, 21, 500), role(19, 27, 500)),
    body: roles(role(14.5, 21.75, 500), role(17, 25.5, 500), role(23, 33, 500)),
  },
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
    scrubber: forms(26, 26, 26, 40),
    transport: forms(80, 80, 80, 120),
    nextLine: forms(22, 22, 22, 32),
    bottomRow: forms(64, 64, 64, 90),
    upNextWidth: {...forms(0, 0, 210, 360), web: 256},
    gap: {...forms(12, 12, 30, 80), web: 56},
  },
  /** Rule 12: the no-artwork placeholder's icon is a third of the box's short side. */
  placeholder: {iconShare: 1 / 3, lineAlpha: .08},
} as const;
export type FrameSpec = typeof frame;
export type FrameTypeRole = keyof typeof frame.type;
