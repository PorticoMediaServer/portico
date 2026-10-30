export {generatedCoverTint, type GeneratedCoverTint} from './generated-cover.ts';

/**
 * Portico's shared design projection. Web (packages/design/scripts/tokens.mjs → web/src/kit/tokens.css)
 * and native (src-next/kit/theme.ts) read every colour, type role, radius and layer from
 * here, so the platforms can't drift. Contrast is pinned by
 * packages/client-core/test/design-tokens.test.ts: every text tone must stay
 * ≥ 4.5:1 on canvas, slate, raised and overlay surfaces, and control
 * boundaries ≥ 3:1 (WCAG 1.4.3 / 1.4.11, PC-VISUAL §5 and §18).
 */
export const colors = {
  /** Surfaces, darkest to brightest: canvas → slate → raised → bright. */
  ink: "#071018",
  white: "#ffffff",
  /** Windows caption Close hover, desktop-app frame 2. */
  windowsCaptionClose: "#c42b1c",
  shadow: "#000000",
  collection: "#0a2233",
  generatedGreen: "#1e5b52",
  /** states#14 library-kind tiles. */
  libraryKindAnime: "#4b4f8f",
  libraryKindMusic: "#6a3f73",
  libraryKindAudiobook: "#8c4a32",
  dim: "#03080c",
  raised: "#10212d",
  /** Hover, focus and selected surfaces (blue slate, never grey — X-07). */
  raisedHigh: "#162c3a",
  /** The highest ordinary surface (chips, badges, tooltips). */
  raisedBright: "#1d3848",
  /** Subdued containers (sunken wells, segmented tracks). */
  raisedSoft: "#0c1a25",
  /** Player notices; the approved Undo and Up next contexts apply their own alpha. */
  playerNotice: "#0c1923",
  /** owner-server message log inset well. */
  messageLog: "#050d14",
  /** Live-player top and bottom scrim colour, with per-stop alpha supplied by chrome. */
  livePlayerScrim: "#04090d",
  /** The dark player-side panel used by the live programme and DVR sheets. */
  panel: "#0b1822",
  /**
   * Sheets, dialogs, menus, popovers and the expanded rail. Opaque by
   * decision V-1 / X-16: text behind an overlay must never show through.
   */
  overlaySurface: "#132634",
  /**
   * The only translucent overlay option: desktop web with working
   * backdrop blur. Native glass follows the platform recipe in componentSpec.button.glass.
   */
  overlaySurfaceTranslucent: "#132634F5",
  ambient: "#153749",
  /** Action blue: rest, hover/focus (strong) and pressed (deep). */
  accent: "#409fd4",
  accentStrong: "#5cb3e2",
  accentDeep: "#2f86bb",
  /** owner-server feedback inbox: open-report text, distinct from the action blue. */
  feedbackOpen: "#8cc6ea",
  text: "#edf5fa",
  textSecondary: "#bdccd6",
  textTertiary: "#96a9b5",
  /** Same value as textTertiary: "muted" never lowers contrast (X-06). */
  muted: "#96a9b5",
  placeholder: "#8298a6",
  /** Hairlines: soft (inside surfaces), standard, strong (emphasis). */
  lineSoft: "#1c3341",
  line: "#28404f",
  lineStrong: "#3b5668",
  /** Boundary of an input or other control at rest: ≥ 3:1 on canvas and slate (X-11). */
  lineControl: "#637580",
  danger: "#f38b88",
  /** Lighter danger text on a danger-tinted surface (live flags and conflicts). */
  dangerText: "#f9b1b1",
  dangerTextStrong: "#f6a3a3",
  focus: "#d5efff",
  scrim: "#071018E8",
  material: "#10212DF2",
  amber: "#e8bc6a",
  record: "#f17979",
  healthy: "#86cfb3",
  /** Dashboard category, hardware and rank colours from the approved 812 frames. */
  lilac: "#a99be8",
  gold: "#e0b66e",
  rose: "#e8929f",
  hardwareText: "#f3cf86",
  reportRankStart: "#96d4f4",
  accountAvatar1Start: "#2f86bb", accountAvatar1End: "#153749",
  accountAvatar2Start: "#7a5cc2", accountAvatar2End: "#2d2250",
  accountAvatar3Start: "#2f9b86", accountAvatar3End: "#123f3a",
  accountAvatar4Start: "#b9773a", accountAvatar4End: "#4a2a14",
  accountAvatar5Start: "#b8527a", accountAvatar5End: "#47182f",
  accountAvatar6Start: "#5f7fd0", accountAvatar6End: "#1d2b57",
  trackRest: "#35505f",
  /** Text and glyphs on action blue (all three accent states). */
  actionInk: "#03121c",
} as const;

export type ColorToken = keyof typeof colors;

/** Spacing in CSS px / native logical points. See kit.css and the page-local rules. */
export const space = {0: 0, 1: 4, 2: 8, 3: 12, 4: 16, 6: 24, 8: 32, 12: 48} as const;
export const spacing = {xs: 8, sm: 16, md: 24, lg: 32, xl: 48, hairline: 1, tight: 6, controlGap: 10, rowHeadingGap: 14, metaGap: 18, sectionGap: 44, pageBottom: 72} as const;
/** Includes the odd-pixel insets drawn in page-local controls, not just multiples of four. */
export const spacingGrid = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 22, 24, 26, 28, 30, 32, 34, 36, 38, 40, 42, 44, 46, 48, 50, 52, 54, 56, 58, 60, 64, 70, 72, 76, 80, 84, 88, 90, 92, 96, 100, 112, 120, 140, 150, 160, 178, 200] as const;

export const typography = {
  regular: 'Manrope-Regular', medium: 'Manrope-Medium', semibold: 'Manrope-SemiBold', bold: 'Manrope-Bold', extraBold: 'Manrope-ExtraBold',
  monoFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
  webFamily: 'Manrope, system-ui, sans-serif', mobileBody: 16, tvBody: 24,
} as const;

/** Existing page-local fixed metrics, lifted without changing their authored CSS units.
 * Semantic typeScale roles still own responsive text; these preserve specific lockups/overrides. */
export const fixedTypeMetrics = {
  "line-1": "1",
  "line-1point-05": "1.05",
  "line-1point-1": "1.1",
  "line-1point-15": "1.15",
  "line-1point-2": "1.2",
  "line-1point-3": "1.3",
  "line-1point-35": "1.35",
  "line-1point-4": "1.4",
  "line-1point-45": "1.45",
  "line-1point-5": "1.5",
  "line-1point-55": "1.55",
  "line-point-98": ".98",
  "size-11point-5px": "11.5px",
  "size-11px": "11px",
  "size-12point-5px": "12.5px",
  "size-12px": "12px",
  "size-13point-5px": "13.5px",
  "size-13px": "13px",
  "size-14point-5px": "14.5px",
  "size-14px": "14px",
  "size-15point-5px": "15.5px",
  "size-15px": "15px",
  "size-16px": "16px",
  "size-17px": "17px",
  "size-18px": "18px",
  "size-19px": "19px",
  "size-20px": "20px",
  "size-21px": "21px",
  "size-22px": "22px",
  "size-24px": "24px",
  "size-25px": "25px",
  "size-26px": "26px",
  "size-27px": "27px",
  "size-2rem": "2rem",
  "size-30px": "30px",
  "size-32px": "32px",
  "size-36px": "36px",
  "size-40px": "40px",
  "size-44px": "44px",
  "size-56px": "56px",
  "tracking-0": "0",
  "tracking-minus-point-005em": "-.005em",
  "tracking-minus-point-015em": "-.015em",
  "tracking-minus-point-01em": "-.01em",
  "tracking-minus-point-025em": "-.025em",
  "tracking-minus-point-02em": "-.02em",
  "tracking-minus-point-035em": "-.035em",
  "tracking-minus-point-03em": "-.03em",
  "tracking-minus-point-045em": "-.045em",
  "tracking-point-01em": ".01em",
  "tracking-point-04em": ".04em",
  "tracking-point-08em": ".08em",
  "weight-400": "400",
  "weight-500": "500",
  "weight-600": "600",
  "weight-700": "700",
  "weight-800": "800"
} as const;

export type TypeStyle = Readonly<{
  size: number;
  /** Logical pixels; display lockups deliberately have a line box smaller than the em. */
  lineHeight: number | 'normal';
  weight: 400 | 500 | 600 | 700 | 800;
  /** Letter spacing in em; native renderers multiply by size. */
  tracking: number;
  uppercase?: boolean;
}>;
const type = (size: number, lineHeight: number | 'normal', weight: TypeStyle['weight'], tracking = 0): TypeStyle => ({size, lineHeight, weight, tracking});
const handheldType = {
  display: type(34, 38, 700, -.026), title: type(26, 31, 700, -.023), heading: type(20, 25, 600, -.0175), subheading: type(17, 22, 600, -.012),
  body: type(15, 21, 400), bodyStrong: type(15, 21, 600), caption: type(13, 18, 400), captionStrong: type(13, 18, 600), micro: type(11, 14, 500, .018),
  label: type(12, 16, 600), lockup: type(46, 43.24, 800, -.045), lockupLong: type(42, 39.48, 800, -.045),
  rowHeading: type(19, 'normal', 700, -.015), nav: type(15, 'normal', 600), cardTitle: type(13, 'normal', 600), cardSubtitle: type(12, 'normal', 400),
  wideTitle: type(16, 'normal', 700), meta: type(13, 'normal', 400), eyebrow: type(13, 'normal', 600), synopsis: type(14, 21, 400),
  button: type(15, 'normal', 700), compactButton: type(15, 'normal', 700), tab: type(15.5, 'normal', 700), chip: type(13.5, 'normal', 700), tabBar: type(10.5, 'normal', 600),
  badge: type(11.5, 'normal', 700), tag: type(13, 'normal', 600), episodeTitle: type(15, 'normal', 700, -.01), episodeBody: type(13, 18.85, 400),
  liveTitle: type(16, 'normal', 700, -.01), liveChannel: type(13, 'normal', 600), liveLabel: type(12, 'normal', 700),
  bannerTitle: type(32, 32, 800, -.04), bannerBody: type(14, 21, 400), rank: type(120, 93.6, 800, -.08),
  initials: type(30, 'normal', 800), fallbackLockup: type(46, 43.24, 800, -.045), generatedGlyph: type(300, 300, 800, -.06),
  activityCount: type(14, 14, 800), activityTitle: type(17, 'normal', 800, -.01), activityDetail: type(13.5, 20, 400),
  rowSubtitle: type(14, 'normal', 400), seeAll: type(14, 'normal', 600), thumbnailLabel: type(12, 'normal', 600),
  liveChromeTitle: type(34, 36.72, 800, -.035), liveChromeTabletTitle: type(24, 25.92, 800, -.035),
  liveChromePhoneTitle: type(18, 19.44, 800, -.035), liveChromeNumber: type(11, 'normal', 800, -.01),
  liveMiniTitle: type(15.5, 'normal', 700),
  libraryKindTitle: type(20, 'normal', 800, -.02),
  liveOwnerSourceTitle: type(16.5, 'normal', 700),
  // Existing mobile-web audiobook detail metrics; consumers lift these without redesign.
  audiobookDiscGlyph: type(54, 'normal', 800, -.05), audiobookDetailTitle: type(28, 30.8, 800, -.035),
  audiobookDetailEyebrow: type(13.5, 18.9, 700), audiobookDetailAuthor: type(16, 22.4, 700),
  audiobookDetailSection: type(19, 'normal', 700, -.015), audiobookChapter: type(15, 19.5, 600),
  audiobookRelatedTitle: type(15.5, 'normal', 700, -.01),
  audiobookDetailMeta: type(13, 'normal', 400), audiobookDetailProgress: type(12.5, 'normal', 600),
  audiobookDetailSynopsis: type(14, 21, 400), audiobookDetailReadMore: type(14, 21, 700),
  audiobookChapterNumber: type(13.5, 'normal', 600), audiobookDetailFact: type(15, 'normal', 400),
  // owner-dashboard phone composition: preserve the drawn metrics when lifting raw CSS.
  dashboardPhoneBarTitle: type(16.5, 19.8, 800, -.015), dashboardPhoneBarDetail: type(12, 14.4, 600),
  dashboardPhoneHeading: type(40, 40, 800, -.04), dashboardPhoneName: type(19, 'normal', 800, -.02),
  dashboardPhoneAlert: type(15.5, 20.15, 700), dashboardPhoneAlertBody: type(14, 20.3, 400),
  dashboardPhoneTask: type(13, 18.2, 400), dashboardPhoneCalm: type(15, 19.5, 700),
  dashboardPhoneFigure: type(34, 37.4, 800, -.03), dashboardPhoneAvatar: type(10, 'normal', 800),
  liveMonogram: type(13, 'normal', 800), bannerKind: type(14, 'normal', 600), fallbackLabel: type(12, 'normal', 700),
  personTitle: type(14, 'normal', 700), detailHeading: type(17, 'normal', 700), starring: type(14, 'normal', 400),
  reportSection: type(20, 'normal', 700, -.015), reportTitle: type(17, 21.25, 700, -.015),
  reportMeta: type(13.5, 'normal', 600), reportDetail: type(12.5, 'normal', 600), reportDevice: type(12.5, 'normal', 700),
  reportHardware: type(11, 'normal', 700), reportValue: type(32, 33.6, 800, -.03), reportPerson: type(14.5, 'normal', 700),
  reportAvatar: type(12.5, 'normal', 800), reportRank: type(128, 102.4, 800, -.07),
  // owner-server frames 3–7: exact type metrics of the drawn Troubleshooting panels.
  troubleshootingHeading: type(18, 'normal', 800, -.02), troubleshootingLede: type(14, 20.3, 400),
  troubleshootingPermissionLede: type(13, 18.85, 400),
  troubleshootingMessagesLede: type(14, 21, 400), troubleshootingMessagesLog: type(12.5, 18.75, 400),
  troubleshootingMessagesText: type(13.5, 19.575, 500),
  troubleshootingLog: type(13, 19.5, 400), troubleshootingReason: type(13, 18.2, 400),
  troubleshootingMessage: type(15, 22.5, 400), troubleshootingCheck: type(15, 18, 400),
  troubleshootingLevel: type(12, 'normal', 800, .02),
  // owner-server API-key rows (frame16).
  integrationKeyName: type(15.5, 'normal', 700, -.005), integrationKeyFacts: type(13.5, 19.575, 400),
  stopBody: type(15, 23.25, 500), stopField: type(16, 'normal', 600), stopHelp: type(13.5, 'normal', 400),
};
/** The end-card (PostPlay) type roles from desktop-player.html's post-play block:
 * kicker 15, title 76, compact title 56, feature 32, name 14 and year 13, with
 * the drawn line heights and tracking. These are separate from the ordinary
 * type roles because the card uses compact-title and split name/year values. */
export const watchEndType = Object.freeze({
 kickerSize: 15, titleSize: 76, titleCompactSize: 56, titleLine: 1.02, titleTracking: -0.035,
 featureSize: 32, nameSize: 14, nameLine: 1.25, yearSize: 13,
});
export type TypeRole = keyof typeof handheldType;
/** The legacy web's desktop display cap (42px); TitleHero and Text clamp up to it. */
export const displayDesktopSize = 42;

/** The desktop, phone and TV drawings are separate columns; tablet uses the phone layout. */
export const typeScale: Readonly<Record<'desktop' | 'handheld' | 'tenFoot', Readonly<Record<TypeRole, TypeStyle>>>> = {
  handheld: handheldType,
  desktop: {...handheldType,
    lockup: type(84, 78.96, 800, -.045), lockupLong: type(64, 60.16, 800, -.045), rowHeading: type(22, 'normal', 700, -.015),
    cardTitle: type(14, 'normal', 600), cardSubtitle: type(13, 'normal', 400), meta: type(15, 'normal', 400), eyebrow: type(15, 'normal', 600),
    synopsis: type(17, 26.35, 400), button: type(17, 'normal', 700), tab: type(17, 'normal', 700), chip: type(15, 'normal', 700),
    liveTitle: type(18, 'normal', 700, -.01), bannerTitle: type(52, 52, 800, -.04), bannerBody: type(16, 24, 400), rank: type(168, 131.04, 800, -.08),
    episodeTitle: type(16, 'normal', 700, -.01), episodeBody: type(14, 21, 400), fallbackLockup: type(52, 48.88, 800, -.045),
  },
  tenFoot: {...handheldType,
    display: type(62, 68, 700, -.029), title: type(44, 52, 700, -.027), heading: type(32, 40, 600, -.019), subheading: type(27, 34, 600, -.011),
    body: type(24, 33, 400), bodyStrong: type(24, 33, 600), caption: type(20, 27, 400), captionStrong: type(20, 27, 600), micro: type(17, 22, 500, .018), label: type(18, 24, 600),
    lockup: type(120, 112.8, 800, -.045), lockupLong: type(120, 112.8, 800, -.045), rowHeading: type(32, 'normal', 700, -.019),
    rowSubtitle: type(20, 'normal', 400),
    nav: type(24, 'normal', 700), cardTitle: type(22, 'normal', 600), cardSubtitle: type(20, 'normal', 400), wideTitle: type(26, 'normal', 700),
    meta: type(24, 'normal', 400), eyebrow: type(24, 'normal', 600), synopsis: type(24, 34.8, 400), button: type(26, 'normal', 700), tab: type(26, 'normal', 700), chip: type(22, 'normal', 700),
    badge: type(18, 'normal', 800), tag: type(20, 'normal', 600), episodeTitle: type(24, 'normal', 700, -.01), episodeBody: type(22, 33, 400),
    liveTitle: type(26, 'normal', 700, -.01), liveChannel: type(22, 'normal', 600), liveLabel: type(20, 'normal', 700),
  },
};

export { mediaIcons, mediaFamilyIcon } from './media-icons.ts';
export type { MediaFamily, MediaIconName, IconPrimitive, MediaIconDefinition } from './media-icons.ts';
export { icons, iconIds, isIconId } from './icons.ts';
export type { IconId, IconDefinition } from './icons.ts';
export { componentSpec, focus as focusSpec, button as buttonSpec, transport as transportSpec, field as fieldSpec, toggle as toggleSpec, chip as chipSpec, segmented as segmentedSpec, row as rowSpec, overlay as overlaySpec, menu as menuSpec, feedback as feedbackSpec, confirmation as confirmationSpec, hero as heroSpec, card as cardSpec, shelf as shelfSpec, banner as bannerSpec, episode as episodeSpec, generatedArt as generatedArtSpec, navigation as navigationSpec, activity as activitySpec, tag as tagSpec, tabs as tabsSpec } from './components.ts';
export type { ComponentSpec, Fill, ButtonVariant, ButtonSize, ButtonState, RowVariant, OverlayKind, ScrimStop } from './components.ts';

/** Named surface roles, including the exact 6/10/12/14/16 px kit radii. */
export const radii = {small: 6, control: 8, artwork: 10, surface: 10, panel: 12, overlay: 14, tag: 6, thumbnail: 8, heroButton: 12, liveCard: 12, fallback: 14, banner: 16, popover: 18, sheet: 24, pill: 999} as const;
/** TV uses drawn radii per role, never a global multiplier. */
export const platformRadii = {
  web: {artwork: 10, heroButton: 12, chip: 10, banner: 16, popover: 18},
  phone: {artwork: 10, heroButton: 10, chip: 9, group: 14, sheet: 24},
  tv: {artwork: 14, heroButton: 14, chip: 14, tag: 8, panel: 24},
} as const;

/** motion.html#1 is binding over the older kit's timings and easing. */
export const motion = {quick: 120, standard: 180, hover: 260, slow: 320, morph: 420, hero: 900} as const;
export const easings = {out: 'cubic-bezier(.2, .8, .2, 1)', in: 'cubic-bezier(.4, 0, 1, 1)', inOut: 'cubic-bezier(.4, 0, .2, 1)', linear: 'linear'} as const;
export const springs = {
  settle: {damping: 26, stiffness: 260, mass: 1}, player: {damping: 24, stiffness: 200, mass: 1}, snap: {damping: 30, stiffness: 420, mass: 1},
} as const;
export type Spring = Readonly<{damping: number; stiffness: number; mass: number}>;
/** Analytic response to a unit step; velocity is units/second toward the destination. */
export function springPosition(c: Spring, seconds: number, velocity = 0): number {
  if (seconds <= 0) return 0;
  const w = Math.sqrt(c.stiffness / c.mass), z = c.damping / (2 * Math.sqrt(c.stiffness * c.mass));
  if (z < 1) {
    const wd = w * Math.sqrt(1 - z * z), b = (velocity - z * w) / wd;
    return 1 + Math.exp(-z * w * seconds) * (-Math.cos(wd * seconds) + b * Math.sin(wd * seconds));
  }
  if (z === 1) return 1 + (-1 + (velocity - w) * seconds) * Math.exp(-w * seconds);
  const r1 = -w * (z - Math.sqrt(z * z - 1)), r2 = -w * (z + Math.sqrt(z * z - 1)), c2 = (velocity + r1) / (r2 - r1);
  return 1 + (-1 - c2) * Math.exp(r1 * seconds) + c2 * Math.exp(r2 * seconds);
}
/** Same 0.002 rest threshold and 33 samples as motion.html:3308–3323. */
export function springTiming(c: Spring): Readonly<{durationMs: number; css: string}> {
  let last = 0;
  for (let ms = 0; ms < 3000; ms++) if (Math.abs(springPosition(c, ms / 1000) - 1) >= .002) last = ms;
  const durationMs = last + 1;
  const samples = Array.from({length: 33}, (_, i) => i === 32 ? 1 : Number(springPosition(c, durationMs * i / 32000).toFixed(4)));
  return {durationMs, css: `linear(${samples.join(', ')})`};
}
export const reducedMotion = {durationMs: 160, easing: 'linear', transforms: false, springs: false, parallax: false, heroRotation: false, heroDrift: false, fingerTracking: true} as const;
export const motionRules = {animatedProperties: ['transform', 'opacity'], interruptFromCurrent: true, queue: false, ambientLiveDots: false, ambientSkeletons: false, ambientLevelMeters: false, staggerMs: [60, 80], maxStaggerSteps: 3} as const;
export const layout = {
  breakpoint: {phone: 600, desktop: 1024}, designWidth: {desktop: 1440, phone: 390, tv: 1920}, designHeight: {phone: 844, tv: 1080},
  gutter: {desktop: 64, desktopNarrow: 32, phone: 20, tablet: 20, tv: 200, tvRight: 96, tvAuth: 120, tvPlayer: 96},
} as const;
export const layers = {liveChrome: 3, liveGuide: 4, liveNotice: 5, base: 0, content: 2, artLabel: 3, rowArrow: 4, heroNavigation: 5, phoneTabBar: 6, tvFocus: 8, sticky: 10, navigation: 20, player: 30, menu: 40, dialog: 50, notice: 60} as const;
/** Visual sizes may be smaller; renderers extend touch/focus hit areas to these minima. */
export const targets = {touch: 44, pointer: 32, primaryTouch: 48, tenFoot: 64, menuRowPointer: 40} as const;

/** Profile identity choices, shared across clients with light initials. */
export const profileArtColors = {
  blue: "#28587a",
  violet: "#685294",
  mint: "#28665d",
  coral: "#985243",
  gold: "#796120",
  slate: "#4a596a",
  rose: "#854d73",
  sky: "#376f8c",
} as const;

/** WCAG 2.x contrast ratio between two opaque `#rrggbb` colours. */
export function contrastRatio(a: string, b: string): number {
  const luminance = (hex: string): number => {
    const n = hex.replace('#', '').slice(0, 6);
    const [r, g, bl] = [0, 2, 4].map(i => parseInt(n.slice(i, i + 2), 16) / 255).map(v => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
  };
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}
