/**
 * Portico component specification.
 *
 * Exact measurements and states for the shared primitives, so web
 * (`web/src/kit`) and native (`apps/native/src-next/kit`) build the
 * same components. Numbers are CSS px on web and points on iOS/iPadOS; the
 * `tv` column is design pixels on the 1920×1080 ten-foot canvas. Colours are
 * token names from `colors` (never literals). The prose rules are in
 * `packages/design/COMPONENTS.md`.
 *
 * Platform columns: `web` (desktop/tablet browsers), `phone` (iPhone and
 * phone-width web), `tablet` (iPad), `tv` (Apple TV, Android TV).
 */
import type {ColorToken, TypeRole} from './index.ts';

type Platforms<T> = Readonly<{web: T; phone: T; tablet: T; tv: T}>;
const all = <T>(web: T, phone: T, tablet: T, tv: T): Platforms<T> => ({web, phone, tablet, tv});
const same = <T>(value: T, tv: T): Platforms<T> => all(value, value, value, tv);

export type Fill = Readonly<{background: ColorToken | 'transparent'; backgroundAlpha?: number; foreground: ColorToken; border?: ColorToken; borderAlpha?: number}>;

/** Focus: one system per control (PC-VISUAL §8.4–8.5). Web/iPad pointer: a ring; TV: ring + scale. */
export const focus = {
  ringWidth: same(3, 4),
  ringColor: 'focus' as ColorToken,
  /** Free-standing controls (buttons, chips, switches, card art). */
  offsetOuter: all(3, 2, 2, 4),
  /** Items inside a container (rows, menu items, tabs, segments, table rows). */
  offsetInner: same(-3, -4),
  /** Only television scales on focus. */
  scale: {button: same(1, 1.05), row: same(1, 1.02), card: same(1, 1.08), chip: same(1, 1.05)},
  /** Never add a fill as well as the ring on keyboard focus; hover may change the fill. */
  fillOnFocus: false,
  glow: {color: 'accentStrong' as ColorToken, alpha: 0.6, x: 0, y: 26, blur: 64, spread: -12},
  durationMs: 120,
  exactlyOneOnTv: true,
} as const;

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'destructive' | 'link';
export type ButtonSize = 'sm' | 'md' | 'lg';
export type ButtonState = 'rest' | 'hover' | 'pressed' | 'selected' | 'disabled';

/** Buttons (CON-13, CON-14, §4.7). On TV nothing focusable is below 64 px (APL-SYS-01), so `sm` and `md` share a height there. */
export const button = {
  height: {sm: all(32, 32, 32, 64), md: all(40, 44, 44, 64), lg: all(48, 50, 50, 72)} as Readonly<Record<ButtonSize, Platforms<number>>>,
  /** Minimum hit area; a 32 px/pt `sm` button extends its hit area to this on touch. */
  minHit: all(32, 44, 44, 64),
  paddingX: {sm: same(12, 20), md: same(16, 28), lg: same(24, 40)} as Readonly<Record<ButtonSize, Platforms<number>>>,
  gap: same(8, 12),
  icon: {sm: same(16, 26), md: same(18, 30), lg: same(20, 32)} as Readonly<Record<ButtonSize, Platforms<number>>>,
  label: {sm: 'captionStrong', md: 'bodyStrong', lg: 'bodyStrong'} as Readonly<Record<ButtonSize, TypeRole>>,
  /** Sizes use drawn radii; hero geometry has its own platform columns. */
  radius: all(10, 10, 10, 14),
  hero: {height: all(54, 46, 46, 72), radius: all(12, 10, 10, 14), paddingX: all(26, 20, 20, 38), gap: all(10, 8, 8, 14), icon: all(22, 20, 20, 30), label: 'button' as TypeRole},
  compact: {height: 46, radius: 12, paddingX: 26, gap: 10, label: 'compactButton' as TypeRole},
  phoneMockGap: 10,
  glass: {color: 'text' as ColorToken, alpha: 0.14, hoverAlpha: 0.22, focusedTvAlpha: 0.28, blur: 20, nativeBlur: {phone: true, appleTv: true, androidTv: false, fireTv: false}, fallback: {background: 'text' as ColorToken, backgroundAlpha: 0.14}},
  /** Icon-only buttons are square (width = height) and always carry a label (tooltip on web, accessibilityLabel on Apple). */
  iconOnlySquare: true,
  variants: {
    primary: {rest: {background: 'text', foreground: 'ink'}, hover: {background: 'white', foreground: 'ink'}, pressed: {background: 'text', foreground: 'ink'}, selected: {background: 'text', foreground: 'ink'}, disabled: {background: 'text', foreground: 'ink'}},
    secondary: {rest: {background: 'text', backgroundAlpha: 0.14, foreground: 'text'}, hover: {background: 'text', backgroundAlpha: 0.22, foreground: 'text'}, pressed: {background: 'text', backgroundAlpha: 0.14, foreground: 'text'}, selected: {background: 'accent', backgroundAlpha: 0.28, foreground: 'text', border: 'accent', borderAlpha: 0.45}, disabled: {background: 'text', backgroundAlpha: 0.14, foreground: 'text'}},
    ghost: {rest: {background: 'transparent', foreground: 'textSecondary'}, hover: {background: 'raisedHigh', foreground: 'text'}, pressed: {background: 'raised', foreground: 'text'}, selected: {background: 'accent', backgroundAlpha: 0.16, foreground: 'text'}, disabled: {background: 'transparent', foreground: 'textSecondary'}},
    destructive: {rest: {background: 'danger', backgroundAlpha: 0.12, foreground: 'danger', border: 'danger', borderAlpha: 0.3}, hover: {background: 'danger', backgroundAlpha: 0.16, foreground: 'danger', border: 'danger', borderAlpha: 0.45}, pressed: {background: 'danger', backgroundAlpha: 0.18, foreground: 'danger', border: 'danger', borderAlpha: 0.45}, selected: {background: 'danger', backgroundAlpha: 0.16, foreground: 'danger'}, disabled: {background: 'danger', backgroundAlpha: 0.12, foreground: 'danger'}},
    link: {rest: {background: 'transparent', foreground: 'accent'}, hover: {background: 'transparent', foreground: 'accentStrong'}, pressed: {background: 'transparent', foreground: 'accent'}, selected: {background: 'transparent', foreground: 'accentStrong'}, disabled: {background: 'transparent', foreground: 'accent'}},
  } as Readonly<Record<ButtonVariant, Readonly<Record<ButtonState, Fill>>>>,
  disabledOpacity: 0.45,
  /** Link press is shown by an underline, not a darker blue (accentDeep text fails contrast on dark surfaces). */
  /** Press feedback: web/phone scale 0.97; TV uses focus scale instead. */
  pressedScale: same(0.97, 1),
  /** Loading keeps the geometry and the label; a spinner the size of the icon replaces the leading icon. Labels never change to "Saving…". */
  loading: {keepsLabel: true, spinnerSize: 'icon'},
} as const;

/**
 * Player transport controls (TV-PLAYER-02): round, icon-only. `primary` is Play/Pause; `control`
 * is everything beside it (skip, previous/next, captions). TV: 72 px with 40 px icons, 88 px Play/Pause.
 */
export const transport = {
  control: {size: all(44, 44, 48, 72), icon: all(24, 24, 26, 40)},
  primary: {size: all(64, 64, 72, 88), icon: all(32, 32, 36, 48)},
  gap: all(16, 16, 20, 32),
} as const;

/** Text fields (X-11, PC-VISUAL §11). */
export const field = {
  height: all(40, 44, 44, 64),
  paddingX: same(12, 20),
  radius: same(8, 14),
  fill: {rest: 'raised', hover: 'raisedHigh', focus: 'raisedHigh', disabled: 'raised'} as Readonly<Record<string, ColorToken>>,
  border: {rest: 'lineControl', hover: 'lineControl', focus: 'accent', invalid: 'danger'} as Readonly<Record<string, ColorToken>>,
  borderWidth: same(1, 2),
  text: 'body' as TypeRole,
  placeholder: 'placeholder' as ColorToken,
  label: {role: 'captionStrong' as TypeRole, color: 'textSecondary' as ColorToken, gap: same(8, 8)},
  help: {role: 'caption' as TypeRole, color: 'textTertiary' as ColorToken},
  error: {role: 'caption' as TypeRole, color: 'danger' as ColorToken, icon: 'error'},
  /** Keyboard focus shows the focus ring (focus.offsetOuter) on top of the accent border. No glow. */
  focusGlow: false,
  search: {height: all(80, 44, 44, 80), radius: all(18, 12, 12, 16), paddingX: all(26, 12, 12, 24), gap: all(18, 10, 10, 16), focusRing: 4, focusAlpha: 0.16},
} as const;

/** Drawn switch geometry; focus/interaction belongs to the containing row on TV. */
export const toggle = {
  track: {width: all(38, 48, 48, 56), height: all(22, 29, 29, 32)},
  thumb: all(16, 23, 23, 24),
  inset: same(3, 4),
  on: {track: 'accent', thumb: 'text'} as Readonly<Record<string, ColorToken>>,
  off: {track: 'raisedBright', trackAlpha: 1, thumb: 'textTertiary'},
  disabledOpacity: 0.45,
  /** A row with a switch toggles when the row is pressed; role = switch; the accessible name never changes with state ("Show X on Home"). */
  wholeRowToggles: true,
  motionMs: 120,
  compact: {track: {width: 38, height: 22}, thumb: 16, inset: 3},
  phone: {track: {width: 48, height: 29}, thumb: 23, inset: 3, onLeft: 22},
} as const;

/** Chips: quick filters and removable filter tokens (CON-13 browse bar). */
export const chip = {
  height: all(40, 34, 34, 64), minHit: all(40, 44, 44, 64), paddingX: all(16, 13, 13, 26), gap: all(8, 6, 6, 10), icon: same(16, 24),
  radius: all(10, 9, 9, 14), label: 'chip' as TypeRole,
  rest: {background: 'raisedHigh', foreground: 'text'} as Fill,
  hover: {background: 'raisedBright', foreground: 'text'} as Fill,
  selected: {background: 'accent', backgroundAlpha: 0.28, foreground: 'text', border: 'accent'} as Fill,
  selectedTv: {background: 'accent', backgroundAlpha: 0.24, foreground: 'text', border: 'accentStrong', borderAlpha: 0.7} as Fill,
  removableIcon: 'close', barGap: same(8, 12),
  quickFilter: {height: 40, radius: 20, paddingX: 15, gap: 7},
  facet: {height: all(34, 34, 34, 56), radius: all(17, 17, 17, 12), paddingX: same(12, 20)},
} as const;

/** Segmented control (peer choices, 2–5 options). */
export const segmented = {
  height: all(32, 32, 32, 64),
  /** Touch: the 32 pt control extends its hit area vertically to 44. */
  minHit: all(32, 44, 44, 64),
  padding: same(4, 4),
  radius: same(8, 14),
  segmentRadius: same(6, 10),
  track: 'raisedSoft' as ColorToken,
  selected: {background: 'text', backgroundAlpha: 0.16, foreground: 'text'} as Fill,
  browseSelected: {background: 'raisedBright', foreground: 'text'} as Fill,
  rest: {background: 'transparent', foreground: 'textTertiary'} as Fill,
  label: 'captionStrong' as TypeRole,
} as const;

export type RowVariant = 'navigation' | 'value' | 'toggle' | 'action' | 'menu' | 'static' | 'media';

/** Rows and grouped lists (CON-04/05/08/09/10/11, §4.2). */
export const row = {
  minHeight: {single: all(48, 44, 44, 64), withSubtitle: all(64, 56, 56, 80), media: all(64, 64, 64, 96)},
  paddingX: all(16, 12, 16, 20),
  paddingY: same(8, 12),
  leadingBox: same(24, 32),
  leadingIcon: all(20, 22, 22, 30),
  leadingGap: same(12, 20),
  /** Row artwork width. Phone/iPad/TV columns are the Apple sizes tuned on 17 Sep (episode stills 116 / 220). */
  artwork: {poster: all(40, 52, 52, 96), square: all(48, 52, 52, 96), landscape: all(96, 116, 116, 220)},
  title: 'bodyStrong' as TypeRole,
  subtitle: {role: 'caption' as TypeRole, color: 'textTertiary' as ColorToken, lines: {settings: 3, media: 1}},
  trailing: {
    navigation: {glyph: 'forward', size: same(16, 24), color: 'textTertiary'},
    value: {text: 'textSecondary', glyph: 'chevronUpDown', size: same(16, 24)},
    toggle: {control: 'toggle'},
    action: {glyph: null, title: 'accent'},
    destructive: {glyph: null, title: 'danger'},
    menu: {glyph: 'more', size: same(18, 26), color: 'textTertiary'},
    static: {glyph: null, focusable: false},
  },
  hover: {background: 'raisedHigh' as ColorToken},
  selected: {background: 'accent' as ColorToken, backgroundAlpha: 0.16},
  /** One group = one surface; rows are never individually bordered (PC-VISUAL §12.5). */
  group: {
    radius: all(10, 14, 14, 18),
    background: 'raised' as ColorToken,
    border: 'lineSoft' as ColorToken,
    separator: 'lineSoft' as ColorToken,
    /** The separator starts where the row's text starts (paddingX + leadingBox + leadingGap when there is a leading element). */
    separatorInsetToText: true,
    header: {role: 'label' as TypeRole, color: 'textTertiary' as ColorToken, gapBelow: same(8, 12)},
    footer: {role: 'caption' as TypeRole, color: 'textTertiary' as ColorToken, gapAbove: same(8, 12)},
    gapBetweenGroups: same(24, 40),
  },
} as const;

export type OverlayKind = 'form' | 'picker' | 'acknowledge';

/** Sheets and dialogs (CON-01, CON-02, CON-29, §4.1). */
export const overlay = {
  surface: 'raised' as ColorToken,
  /** Only desktop web with working backdrop blur may use `overlaySurfaceTranslucent`. */
  translucentAllowed: all(true, false, false, false),
  border: {color: 'lineStrong' as ColorToken, alpha: 0.7, width: 1},
  radius: all(16, 24, 24, 24),
  scrim: {color: 'ink' as ColorToken, alpha: all(0.62, 0.72, 0.62, 0.8)},
  inset: all(20, 16, 20, 28),
  width: {sm: all(440, 0, 440, 520), md: all(520, 0, 520, 640), lg: all(640, 0, 640, 760)},
  /** `side` placement (the episode panel): a full-height panel on the trailing edge. Phones use the bottom sheet instead. */
  sideWidth: all(440, 0, 440, 640),
  /** Phones: a `half` detent opens the sheet at this fraction of the screen; dragging up expands it to maxHeight. */
  halfDetent: 0.5,
  /** Phone: bottom sheet, full width (0 = full width), with a grabber. Others: centred. */
  presentation: all('centered', 'bottomSheet', 'centered', 'centered'),
  maxHeight: all(0.88, 0.9, 0.82, 0.82),
  grabber: {width: 36, height: 4, color: 'textTertiary' as ColorToken, alpha: 0.5, top: 8},
  title: 'heading' as TypeRole,
  subtitle: {role: 'caption' as TypeRole, color: 'textSecondary' as ColorToken},
  headerPaddingTop: all(20, 16, 20, 28),
  headerGap: same(4, 8),
  /** ✕ appears only on pickers (and never on TV): 44 × 44 (web 40), top-trailing, label "Close". */
  closeButton: {size: all(40, 44, 44, 0), kinds: ['picker'] as readonly OverlayKind[]},
  /** Body horizontal padding = inset − row.paddingX, so row text aligns with the title; non-row content adds row.paddingX back. */
  bodyAlignsRowsToTitle: true,
  footer: {
    kinds: ['form', 'acknowledge'] as readonly OverlayKind[],
    background: 'overlaySurface' as ColorToken,
    hairline: 'lineSoft' as ColorToken,
    paddingY: same(12, 20),
    gap: same(8, 16),
    /** Order: leading ghost action (Reset, Clear all) at the leading edge · spacer · Cancel (ghost) · primary. TV: primary first and focused. Phone web: stacked full width, primary on top. */
    order: ['leading', 'spacer', 'cancel', 'primary'] as const,
    buttonSize: all('md', 'lg', 'md', 'md') as Platforms<ButtonSize>,
  },
  motion: {enterMs: 180, exitMs: 120, sideMs: 320, translate: 24, scaleFrom: 0.98, spring: 'settle', reducedMs: 160},
} as const;

/** Menus (card menu, More, sort, pickers on web; anchored panels on iPad). CON-05. */
export const menu = {
  surface: 'raisedHigh' as ColorToken,
  radius: all(14, 14, 14, 24),
  phoneMaterial: {color: 'raisedBright' as ColorToken, alpha: 0.96, blur: 30},
  padding: same(8, 12),
  width: {min: same(220, 360), max: same(320, 520)},
  maxVisibleRows: 12,
  visualRowHeight: all(36, 42, 42, 64),
  row: {minHeight: all(40, 44, 44, 64), paddingX: same(12, 20), icon: same(18, 28), gap: same(12, 16), label: 'body' as TypeRole, radius: same(8, 14)},
  highlight: {background: 'accent' as ColorToken, backgroundAlpha: 0.16},
  sectionLabel: {role: 'label' as TypeRole, color: 'textTertiary' as ColorToken, paddingTop: same(8, 12), paddingBottom: same(4, 8)},
  separator: {color: 'lineSoft' as ColorToken, insetX: same(4, 8), marginY: same(4, 8)},
  destructive: {foreground: 'danger' as ColorToken, groupLast: true},
  selected: {glyph: 'check', color: 'accent' as ColorToken},
  submenu: {glyph: 'forward'},
  /** Section order for entry actions (shared model, CON-24). */
  entryGroups: ['play', 'together', 'personal', 'organize', 'open', 'manage'] as const,
} as const;

/** Inline notices, state views and toasts (CON-07, CON-17, CON-18). */
export const feedback = {
  notice: {
    padding: same(12, 20), gap: same(12, 16), radius: same(10, 18), icon: same(18, 28), text: 'caption' as TypeRole, title: 'bodyStrong' as TypeRole,
    tones: {
      info: {icon: 'info', color: 'accent', tintAlpha: 0.12, borderAlpha: 0.3},
      success: {icon: 'success', color: 'healthy', tintAlpha: 0.12, borderAlpha: 0.3},
      warning: {icon: 'warning', color: 'amber', tintAlpha: 0.12, borderAlpha: 0.3},
      error: {icon: 'error', color: 'danger', tintAlpha: 0.12, borderAlpha: 0.3},
    },
    action: {variant: 'secondary' as ButtonVariant, size: 'sm' as ButtonSize},
  },
  stateView: {
    iconCircle: same(56, 88), icon: same(24, 38), gap: same(12, 20), maxWidth: same(440, 760), title: 'subheading' as TypeRole, body: 'body' as TypeRole,
    action: {variant: 'primary' as ButtonVariant, size: 'md' as ButtonSize}, secondaryAction: {variant: 'secondary' as ButtonVariant},
  },
  toast: {
    surface: 'overlaySurface' as ColorToken, radius: same(12, 20), paddingX: same(16, 28), paddingY: same(12, 20), maxWidth: all(560, 0, 560, 900),
    position: all('bottomCenter', 'aboveTabBar', 'bottomCenter', 'topTrailing'), durationMs: 4000, durationWithActionMs: 8000,
    action: {variant: 'secondary' as ButtonVariant},
  },
  /** Loading label copy is "Loading {thing}" without an ellipsis; skeletons match final geometry and don't pulse under Reduce Motion. */
  spinner: {size: {inline: same(16, 26), region: same(28, 44), screen: same(44, 64)}, stroke: same(2, 3), track: 'textTertiary' as ColorToken, trackAlpha: 0.35, arc: 'accent' as ColorToken, periodMs: 1000},
} as const;

/** Confirmation tiers (CON-06, §4.5). */
export const confirmation = {
  tiers: {
    0: {dialog: false, undoToast: true, examples: ['Remove from queue', 'Remove from My List', 'Archive', 'Mark as read']},
    1: {dialog: true, confirmVariant: 'destructive' as ButtonVariant, typed: false, examples: ['Delete profile', 'Remove device', 'Revoke key', 'End group', 'Sign out everywhere']},
    2: {dialog: true, confirmVariant: 'destructive' as ButtonVariant, typed: true, examples: ['Delete files from disk', 'Empty trash', 'Clean up a folder', 'Remove a library']},
  },
  /** Non-destructive checks (verify a backup, disconnect) use a normal form with a primary and no typed field. */
  typedTokenIsHumanWord: true,
  /** Title "Verb object?"; body = the consequence; confirm label = the verb ("Delete profile"), never "OK". */
  copy: {title: 'verbObjectQuestion', confirmLabel: 'verb'},
} as const;

/** A scrim stop: `at` is 0–1 along the gradient, `alpha` the ink opacity there. */
export type ScrimStop = Readonly<{at: number; alpha: number}>;

/** Hero geometry follows the frames, independent of viewport height. kit.css:40–93,164–172,247–265. */
export const hero = {
  mobileWeb: {homeHeight: 486, titleHeight: 330},
  homeHeight: all(720, 560, 560, 1080), titleHeight: all(660, 396, 396, 1080),
  backdropWidth: all(1920, 800, 800, 1920), backdropOpacity: same(1, 1),
  backdrop: {widthFraction: all(0.76, 1, 1, 1500 / 1920), titleWidthFraction: all(0.72, 1, 1, 1500 / 1920), tvHeight: 900, position: {home: 'center 30%', title: '30% 25%', phoneHome: '38% 30%', phoneTitle: '44% 20%'}},
  ambient: {insetFraction: -0.12, sizeFraction: 1.24, blur: all(90, 60, 60, 90), saturation: 1.4, alpha: all(0.32, 0.45, 0.45, 0.3)},
  mask: {leftOpaqueAt: 0.38, bottomOpaqueAt: 0.42, phoneBottomOpaqueAt: 0.48, composite: 'intersect', webkitComposite: 'source-in'},
  scrim: {
    color: 'ink' as ColorToken,
    phoneOverlay: [{at: 0, alpha: 0}, {at: 0.38, alpha: 0}, {at: 0.62, alpha: 0.7}, {at: 1, alpha: 0.96}] as readonly ScrimStop[],
    tvLeft: [{at: 0, alpha: 0.86}, {at: 0.3, alpha: 0.62}, {at: 0.56, alpha: 0.12}, {at: 0.7, alpha: 0}] as readonly ScrimStop[],
  },
  paddingX: all(64, 20, 20, 200),
  copy: {width: all(600, 350, 350, 960), titleWidth: all(620, 350, 350, 960), homeBottom: all(150, 24, 24, 0), titleBottom: all(64, 4, 4, 0)},
  identity: {eyebrow: 'eyebrow' as TypeRole, title: 'lockup' as TypeRole, longTitle: 'lockupLong' as TypeRole, gap: same(20, 22)},
  facts: {role: 'meta' as TypeRole, strongWeight: 700, color: 'textSecondary' as ColorToken, gap: all(18, 12, 12, 26), rowGap: all(8, 6, 6, 10), separatorWidth: same(12, 16)},
  synopsis: {role: 'synopsis' as TypeRole, color: 'textSecondary' as ColorToken, lines: all(3, 2, 2, 2), maxWidth: all(540, 350, 350, 800)},
  /** kit.css `.actions` draws 12 px on web, phone and tablet; TV `.tv .actions` draws 18. */
  actions: {gap: all(12, 12, 12, 18), phonePrimaryFullWidth: false, progress: {height: 4, insetX: 0, track: 'ink' as ColorToken, trackAlpha: 0.14, fill: 'accent' as ColorToken}},
  rotation: {intervalMs: 8000, crossfadeMs: 900, copyMs: 600, copyTranslate: 6, copyStaggerMs: 60, driftMs: 11000, driftScaleFrom: 1.07, pausesOnHover: true, pausesOnFocus: true, tvOnlyWhileHeroFocused: true, reducedMs: 160, reducedRotates: false},
  thumbnail: {width: 132, height: 74, radius: 8, gap: 12, bottom: 64, opacity: 0.62, hoverOpacity: 0.9, progressHeight: 3, progressGap: 8, labelGap: 6, labelSize: 12, label: 'thumbnailLabel' as TypeRole},
  phoneDots: {size: 6, activeWidth: 18, gap: 6, marginTop: 18, alpha: 0.3},
} as const;

/** Artwork scales; the label stays still. Shadow is a fixed layer faded in (motion.html#1). */
export const card = {
  width: {poster: all(190, 118, 118, 214), wide: all(316, 262, 262, 400), phoneMockWide: 260, square: all(190, 132, 132, 214), artist: all(172, 100, 100, 214)},
  aspect: {poster: 2 / 3, wide: 16 / 9, square: 1, circle: 1, magazine: 3 / 4}, radius: same(10, 14),
  title: 'cardTitle' as TypeRole, subtitle: 'cardSubtitle' as TypeRole, titleMargin: all(10, 8, 8, 14), subtitleMargin: same(2, 3),
  hover: {artScale: 1.045, liveScale: 1.03, episodeScale: 1.03, durationMs: 260, playMs: 200, playTranslate: 6,
    shadow: {x: 0, y: 22, blur: 40, spread: -16, color: 'shadow' as ColorToken, alpha: 0.85}, ring: {width: 1, color: 'text' as ColorToken, alpha: 0.16}},
  play: {size: 40, wideSize: 52, episodeSize: 48, inset: 10, wideScaleFrom: 0.9},
  /** kit.css `.gap` between on-art facts: 12 px, 16 on TV. */
  wide: {insetX: same(14, 20), insetBottom: same(14, 18), title: 'wideTitle' as TypeRole, scrimStart: 0.45, scrimAlpha: 0.85, factsGap: all(12, 12, 12, 16)},
  progress: {height: same(4, 6), marginTop: same(10, 12), track: 'text' as ColorToken, trackAlpha: 0.22, fill: 'accentStrong' as ColorToken},
  badge: {radius: same(6, 8), inset: same(8, 12), paddingX: all(8, 8, 8, 11), paddingY: same(3, 4), role: 'badge' as TypeRole},
  live: {width: {web: 316, phone: 244}, height: {web: 178, phone: 140}, padding: {web: 18, phone: 14}, radius: 12, channelGap: 10, monogramSize: 34, monogramRadius: 8, title: 'liveTitle' as TypeRole, channel: 'liveChannel' as TypeRole, timeGap: 3, dotSize: 7, dotGap: 6, dotAnimated: false, monogram: 'liveMonogram' as TypeRole},
} as const;

export const shelf = {
  marginTop: all(44, 28, 28, 44), heading: 'rowHeading' as TypeRole, subtitle: 'rowSubtitle' as TypeRole, seeAll: 'seeAll' as TypeRole, headingGap: all(14, 14, 14, 18), headingMargin: all(14, 4, 4, 22),
  gap: all(16, 12, 12, 28), paddingTop: same(10, 10), phonePaddingTop: 8, paddingBottom: same(14, 14), phonePaddingBottom: 10,
  homeOverlap: -40, pageBottom: 72, arrowWidth: 56, arrowTop: 38, arrowBottom: 50, revealMs: 200,
  rank: {width: 262, numberWidth: 92, numberHeight: 255, overlap: -22, posterWidth: 170, strokeWidth: 2, role: 'rank' as TypeRole},
} as const;
export const banner = {
  height: 300, radius: 16, surface: 'collection' as ColorToken, imageWidthFraction: 0.7, imagePosition: '60% 30%', maskOpaqueAt: 0.45,
  hoverScale: 1.03, hoverMs: 600, paddingLeft: 56, copyMaxWidth: 520, kind: 'bannerKind' as TypeRole, title: 'bannerTitle' as TypeRole, body: 'bannerBody' as TypeRole,
  titleMarginTop: 8, titleMarginBottom: 10, bodyMarginBottom: 22, miniGap: 8, miniMarginBottom: 24, miniWidth: 52, miniHeight: 78, miniRadius: 6,
} as const;
export const episode = {
  width: all(316, 136, 136, 384), aspect: 16 / 9, radius: same(10, 14), title: 'episodeTitle' as TypeRole, body: 'episodeBody' as TypeRole,
  titleMargin: same(14, 16), metaMargin: same(3, 4), bodyMargin: 8, bodyLines: 3,
  fallback: {cropScale: 2.6, saturation: 0.55, brightness: 0.42, numeral: false, labelBelowTitle: true},
  duration: {inset: same(12, 14), paddingX: same(8, 10), paddingY: same(3, 4), radius: same(6, 8), inkAlpha: 0.65, size: same(12, 18)},
  progressHeight: same(4, 6),
} as const;
export const generatedArt = {
  height: 236, radius: 14, surface: 'raisedSoft' as ColorToken, marginBottom: 20,
  label: {right: 16, top: 14, paddingX: 9, paddingY: 4, radius: 6, inkAlpha: 0.7, role: 'fallbackLabel' as TypeRole},
  copy: {left: 36, bottom: 34, width: 420, role: 'fallbackLockup' as TypeRole},
  ambient: {insetFraction: -0.2, sizeFraction: 1.4, blur: 70, saturation: 1.5, alpha: 0.55},
  backdrop: {widthFraction: 0.72, maskOpaqueAt: 0.4, position: 'center 30%'},
  poster: {right: 44, top: 26, height: 184, radius: 10}, glyph: {role: 'generatedGlyph' as TypeRole, right: 56, bottom: -58, alpha: 0.09},
  gradient: {blue: 'accentDeep' as ColorToken, green: 'generatedGreen' as ColorToken},
  people: {width: 120, circle: 96, gap: 28, role: 'initials' as TypeRole, labelMargin: 10, title: 'personTitle' as TypeRole, subtitle: 'cardSubtitle' as TypeRole},
  forbiddenBackgroundGlyph: {auth: true, state: true, serverBand: true},
} as const;
export const tag = {radius: same(6, 8), paddingX: same(9, 12), paddingY: 3, borderColor: 'text' as ColorToken, borderAlpha: 0.28, borderWidth: same(1, 1.5), role: 'tag' as TypeRole} as const;
export const tabs = {gap: all(34, 24, 24, 44), paddingTop: all(16, 12, 12, 0), paddingBottom: all(14, 11, 11, 16), underline: same(3, 4), role: 'tab' as TypeRole} as const;
export const navigation = {
  // Justin's web logo override, 28 Oct: shared by auth, Services and the player shell.
  webWordmarkScale: 1.25,
  top: {height: 76, gap: 40, linkGap: 26, linkPaddingY: 6, underlineHeight: 2, markHeight: 20, toolsGap: 6, iconSize: 40, iconDrawing: 22, iconHoverAlpha: 0.1, iconRadius: 999, avatarSize: 34, avatarMarginLeft: 8, overArtAlpha: 0.78, solidAlpha: 0.88, solidBlur: 22, solidSaturation: 1.2},
  phone: {height: 100, paddingTop: 50, solidHeight: 96, solidPaddingTop: 46, markHeight: 16, avatarSize: 30, solidAlpha: 0.82, blur: 24, saturation: 1.3,
    tabBar: {height: 84, paddingTop: 9, paddingX: 14, gap: 3, alpha: 0.82, blur: 24, borderAlpha: 0.08, label: 'tabBar' as TypeRole}},
  mobileWeb: {topHeight: 56, topPaddingLeft: 20, topPaddingRight: 16, tabBar: {height: 56, paddingTop: 7, paddingX: 14, gap: 2, alpha: 0.9, blur: 24, borderAlpha: 0.08}},
  tv: {width: 260, openWidth: 560, itemSize: 64, openItemWidth: 320, insetLeft: 72, paddingTop: 58, paddingBottom: 56, itemGap: 8, labelGap: 22, itemRadius: 14, contentShift: 270, dimAlpha: 0.5},
} as const;
/** desktop-activity.html#1–6. Glyph A is selected by accepted Q38. */
export const activity = {
  glyph: 'levels', height: 40, minWidth: 40, icon: 22, gap: 5, paddingX: 9, countPaddingRight: 3, countPaddingEnd: 11, radius: 20,
  hoverAlpha: 0.1, busyAlpha: 0.14, busyHoverAlpha: 0.22, openAlpha: 0.12, count: 'activityCount' as TypeRole,
  dot: {size: 7, top: 9, left: 23, ring: 2}, underline: {width: 16, height: 2, bottom: -6},
  panel: {width: 420, radius: 18, surface: 'raised' as ColorToken, alpha: 0.94, blur: 30, saturation: 1.3, paddingBottom: 8,
    headerPadding: [20, 20, 14], headerGap: 14, title: 'activityTitle' as TypeRole, rowMargin: 8, rowPaddingX: 12, rowRadius: 12, rowGap: 14, icon: 32,
    dashboardHeight: 46, dashboardRadius: 10, dimAlpha: 0.62, dimColor: 'dim' as ColorToken, navDimAlpha: 0.4,
    motion: {enterMs: 180, exitMs: 120, scaleFrom: 0.98, translateY: -4, reducedMs: 160}},
  progress: {height: 3, trackAlpha: 0.12, ringSize: 32, ringStroke: 2.6, ringTrackAlpha: 0.13, indeterminate: false},
} as const;

export const componentSpec = {focus, button, transport, field, toggle, chip, segmented, row, overlay, menu, feedback, confirmation, hero, card, shelf, banner, episode, generatedArt, tag, tabs, navigation, activity} as const;
export type ComponentSpec = typeof componentSpec;
