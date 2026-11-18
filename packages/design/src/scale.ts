/**
 * The form scale (round 8, rules R2 to R4): what a window's FORM decides, as one table of form x role.
 *
 * A form is the size class a window is drawn in, never its width alone: `phone` (under 600 wide, or a phone on
 * its side), `medium` (a tablet 600 to 1023 wide, an open foldable), `expanded` (a tablet from 1024: a touch
 * window is a tablet however wide it is), `web` (a browser window from 1024) and `tv` (the 1920 x 1080 canvas).
 * The native apps never take the `web` column; a browser takes `phone`, `medium` and `web`.
 *
 * Every number is the mock-ups' own CSS value (design/ui-v1: kit.css, tablet.html, tablet-play.html, tv*.html,
 * desktop-*.html, phone-*.html), measured from the pages, not from screenshots. Type is `formType` in index.ts
 * (it needs the type roles); `formScale` there joins the two, so a reader sees one table.
 */
import type {ColorToken} from './index.ts';
import type {FrameForm} from './frames.ts';

export type ScaleForm = FrameForm | 'web';
export type ByForm<T> = Readonly<Record<ScaleForm, T>>;
export const byForm = <T>(phone: T, medium: T, expanded: T, web: T, tv: T): ByForm<T> => ({phone, medium, expanded, web, tv});

/** R2: card, button and row sizes. */
export const formSizes = {
  card: {
    /** Art widths in a row of cards. A grid stretches its columns from these. */
    poster: byForm(118, 150, 150, 190, 214),
    wide: byForm(262, 296, 262, 316, 400),
    square: byForm(132, 150, 150, 190, 200),
    artist: byForm(100, 132, 132, 172, 214),
    /** Between cards in a row. */
    gap: byForm(12, 14, 14, 16, 28),
    radius: byForm(10, 10, 10, 10, 14),
    /** R5: the caption under the art is a reserved two-line slot (title, then year or subtitle), so every card in a
     * row is the same height and a row's height includes its captions. `gap` is art to title; the lines are fixed
     * line boxes, so the slot is `gap + titleLine + subtitleGap + subtitleLine` whatever the text. */
    caption: {gap: byForm(8, 10, 10, 10, 15), titleLine: byForm(18, 19, 19, 19, 32), subtitleGap: byForm(2, 2, 2, 2, 4), subtitleLine: byForm(17, 18, 18, 18, 29)},
  },
  button: {
    /** The page's buttons (Play, My List, Sign in): rule 8. Round ones are as wide as they are high. */
    standard: {height: byForm(46, 48, 48, 54, 72), radius: byForm(10, 12, 12, 12, 14), paddingX: byForm(20, 22, 22, 26, 38), gap: byForm(8, 8, 8, 10, 14), icon: byForm(20, 20, 20, 22, 30)},
    /** In bars, panels and footers. */
    small: {height: byForm(42, 44, 44, 42, 64), radius: byForm(10, 12, 12, 12, 14), paddingX: byForm(16, 18, 18, 18, 28), gap: byForm(8, 8, 8, 8, 12), icon: byForm(18, 18, 18, 18, 26)},
    /** In rows and section headers. A touch form never draws a target under 44. */
    extraSmall: {height: byForm(34, 44, 44, 34, 56), radius: byForm(9, 10, 10, 10, 12), paddingX: byForm(12, 14, 14, 14, 22), gap: byForm(6, 6, 6, 6, 10), icon: byForm(16, 16, 16, 16, 24)},
  },
  row: {
    /** A list row with one line, and with a second (rule 5 and rule 7). The web's rows are one height. */
    oneLine: byForm(48, 52, 52, 76, 84), twoLine: byForm(64, 69, 69, 76, 84),
    paddingX: byForm(14, 18, 18, 24, 28), paddingY: byForm(8, 12, 12, 18, 16),
    /** The icon tile at a row's start and its glyph. */
    tile: byForm(30, 30, 30, 30, 44), tileRadius: byForm(8, 8, 8, 8, 12),
    /** The control at a row's end: a select's height (a phone's value is plain text). */
    select: byForm(0, 44, 44, 44, 0),
    chip: byForm(34, 44, 44, 40, 64), tab: byForm(44, 48, 48, 48, 64),
  },
} as const;

/** R3: the four named levels of emphasis. A text role, a glyph and a value each take one; none types a colour. */
export type EmphasisLevel = 'primary' | 'secondary' | 'tertiary' | 'accent';
export const emphasis = {
  level: {primary: 'text', secondary: 'textSecondary', tertiary: 'textTertiary', accent: 'accentStrong'} as Readonly<Record<EmphasisLevel, ColorToken>>,
  /** What a control's state says, the same on every form:
   * - a value at its default ("Any", "Off", "Medium") is tertiary and medium weight; a value the viewer chose, and one
   *   that opens a picker, is accent and bold;
   * - a chosen or active control (repeat on, the open tab's bar, the current choice's tick, a playing row's title)
   *   is accent; the tab or segment it belongs to is primary, its neighbours tertiary;
   * - a row's label is primary and semibold; the current choice in a list is primary, the others secondary;
   * - "S1 E2" is secondary and regular, the episode's name beside it primary and bold. */
  value: {level: 'tertiary' as EmphasisLevel, weight: 500}, valueChosen: {level: 'accent' as EmphasisLevel, weight: 700},
  active: 'accent' as EmphasisLevel, on: 'primary' as EmphasisLevel, off: 'tertiary' as EmphasisLevel,
  /** Fills at accent strength: a progress bar's played part and its track; the New badge. */
  progress: {fill: 'accentStrong' as ColorToken, track: 'text' as ColorToken, trackAlpha: .22},
  badge: {background: 'accent' as ColorToken, foreground: 'actionInk' as ColorToken},
} as const;

/** R4: three icon classes, each a glyph size per form and a level of emphasis (the dimming is the level's colour,
 * not an opacity). `primary`: the transport (Play or Pause, Previous, Next, the skip arrows). `secondary`: shuffle,
 * repeat and tool glyphs (subtitles, cast, picture in picture, More, volume, lyrics, queue, a bar's buttons).
 * `tertiary`: inline glyphs (a chevron, a tick, the glyph in a row's tile, in a pill, beside a label). A control that
 * is on takes `emphasis.active`; one the item does not support keeps its class and is dimmed in place. */
export type IconClass = 'primary' | 'secondary' | 'tertiary';
export const iconClass = {
  primary: {size: byForm(34, 28, 28, 28, 38), level: 'primary' as EmphasisLevel},
  secondary: {size: byForm(22, 24, 24, 22, 28), level: 'secondary' as EmphasisLevel},
  tertiary: {size: byForm(18, 18, 18, 18, 24), level: 'tertiary' as EmphasisLevel},
  /** The round box a class's glyph sits in (the touch or focus target). The disc under Play or Pause is `componentSpec.transport.primary`. */
  box: {primary: byForm(48, 48, 48, 48, 64), secondary: byForm(48, 48, 48, 48, 64), tertiary: byForm(38, 44, 44, 34, 56)},
} as const;
