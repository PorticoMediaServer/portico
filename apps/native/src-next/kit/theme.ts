import React, {createContext, useContext, useMemo} from 'react';
import {Platform, useWindowDimensions, type TextStyle} from 'react-native';
import {colors, componentSpec, emphasis as emphasisSpec, formScale, frameForm, layers, layout, motion, platformRadii, radii, spacing, targets, typography, type ByForm, type EmphasisLevel, type FrameForm, type TypeRole, type TypeStyle} from '@portico/design';

export const fontFamilies = {
  400: typography.regular,
  500: typography.medium,
  600: typography.semibold,
  700: typography.bold,
  800: typography.extraBold,
} as const;

/** Ruling R46-TV-TEXT-SIZE: the confirmed TV appearance preference maps to the
 * one numeric scale. Standard 1.0, Large 1.2. Handhelds keep 1.0 because the OS
 * accessibility scale is honoured separately by the text renderer. */
export const tvTextScale = Object.freeze({standard: 1, large: 1.2} as const);
export type TextSizePreference = keyof typeof tvTextScale;
export function textScaleFor(textSize: TextSizePreference | undefined, isTV: boolean): number {
  return isTV && textSize === 'large' ? tvTextScale.large : 1;
}

/** `shellType`, `browseType` and page-local helpers are plain functions, not
 * hooks. The mounted provider publishes the one confirmed scale so a caller
 * that omits it still honours the preference exactly once; an explicit numeric
 * argument always wins and no nested provider compounds the value. */
let publishedTextScale = 1;
export function activeTextScale(): number {return publishedTextScale;}

/** The Android line (round 8): a title's line box must clear its descenders. Android clips a Text to its line box, so
 * a bold title set tighter than this share of its size loses the tails of g, y and p on its last line. */
export const titleLineFloor = 1.2;
/** A sheet or menu title has no drawn line height ('normal'); on Android it is given this one, so the box is the same on every device. */
export const titleLine = (size: number): number => Math.round(size * 1.3);

/** The measured line (G2, 19 Nov, the dev gallery's `type-ladder` and `type-tv-*` cases): Manrope's ink, the ring of
 * Å and the tails of g, j and y, needs a line box of 1.34 of its size on Android and 1.36 on tvOS at extra bold (1.30
 * and 1.32 at regular). In a tighter box Android cuts it at both edges and tvOS at the top. The font's own line
 * ('normal', about 1.37) clears on both. One number for both systems and every weight: the largest measured. */
export const measuredLineFloor = 1.36;

/** Native fonts supply their own normal line metrics. Tracking is specified in em. `lineFloor`: a role with a set line
 * box is never drawn tighter than this share of its size (the measured forms pass `measuredLineFloor`); without it the
 * older Android-only floor for bold titles applies. */
export function nativeType(style: TypeStyle, textScale = 1, lineFloor?: number): TextStyle {
  const line = style.lineHeight === 'normal' ? undefined
    : lineFloor !== undefined ? Math.max(style.lineHeight, Math.ceil(style.size * lineFloor))
    : Platform.OS === 'android' && style.weight >= 700 ? Math.max(style.lineHeight, Math.ceil(style.size * titleLineFloor)) : style.lineHeight;
  return {
    fontFamily: fontFamilies[style.weight],
    fontSize: style.size * textScale,
    letterSpacing: style.tracking * style.size * textScale,
    ...(line === undefined ? {} : {lineHeight: line * textScale}),
  };
}

/** One form's column of a form-scale table: `pick(formScale.card, 'expanded').poster` is 150. */
type Picked<T> = T extends ByForm<infer V> ? V : {[K in keyof T]: Picked<T[K]>};
function pick<T>(table: T, form: FrameForm): Picked<T> {
  if (!table || typeof table !== 'object') return table as Picked<T>;
  if ('phone' in table && 'tv' in table) return (table as ByForm<unknown>)[form] as Picked<T>;
  return Object.fromEntries(Object.entries(table as object).map(([key, value]) => [key, pick(value, form)])) as Picked<T>;
}
const scales = Object.fromEntries((['phone', 'medium', 'expanded', 'tv'] as const).map(form =>
  [form, pick({card: formScale.card, button: formScale.button, row: formScale.row, icon: formScale.icon}, form)])) as Record<FrameForm, Picked<Pick<typeof formScale, 'card' | 'button' | 'row' | 'icon'>>>;
/** R3: the four levels as colours. */
const emphasisColors = Object.fromEntries(Object.entries(emphasisSpec.level).map(([level, token]) => [level, colors[token]])) as Record<EmphasisLevel, string>;

/** `height`: the window's, when known. A phone held sideways is wider than 600 but short; it stays
 * a phone, as the shell keeps it (shell/model.ts `shellLayout`), so its pages keep the phone's compositions (N2-4). */
export type ThemeOptions = {isTV: boolean; width: number; height?: number; reduceMotion?: boolean; textScale?: number; fontScale?: number};
export function createTheme({isTV, width, height, reduceMotion = false, textScale = 1, fontScale = 1}: ThemeOptions) {
  // R2. The FORM decides every size: `frameForm` is the one test, shared with the frames. A touch window from 1024 wide
  // is `expanded`, a tablet, and takes the tablet's type, cards, buttons and rows.
  const form = frameForm(width, isTV, height);
  // `column`: the component spec's column for this form's sizes (`theme.spec.chip.height[theme.column]`).
  const column: 'phone' | 'tablet' | 'tv' = form === 'tv' ? 'tv' : form === 'phone' ? 'phone' : 'tablet';
  // `platform` and `layoutMode` name the STRUCTURE, as they always have: 'web' and 'desktop' are the wide arrangement a
  // tablet from 1024 shares with the desktop (sidebar, side panels, the wide dock). Never index a size by `platform`.
  const platform: 'phone' | 'tablet' | 'web' | 'tv' = form === 'tv' ? 'tv' : form === 'phone' ? 'phone' : form === 'expanded' ? 'web' : 'tablet';
  const layoutMode: 'phone' | 'desktop' | 'tv' = isTV ? 'tv' : platform === 'web' ? 'desktop' : 'phone';
  // R3: a role with a named level of emphasis carries that level's colour, so text drawn with it needs no colour of its
  // own. A role without one (primary) carries none, and so still takes the colour of the text it sits in.
  const levels = formScale.emphasis[form];
  // G2: the TV column is measured (Android TV emulator, Apple TV simulator); the touch forms keep the older floor until
  // they are measured on the Pixel and the iPhone.
  const lineFloor = form === 'tv' ? measuredLineFloor : undefined;
  const type = Object.fromEntries(Object.entries(formScale.type[form]).map(([role, value]) => {
    const level = levels[role as TypeRole];
    const style = nativeType(value, textScale, lineFloor);
    return [role, level ? {...style, color: emphasisColors[level]} : style];
  })) as Record<TypeRole, TextStyle>;
  // TV page and panel headings are drawn at 44/800 with -.03 tracking (tv-more .pg,
  // the TV panel h3); the shared ten-foot scale publishes 44/700 at -.027. Both
  // Android TV and Apple TV draw this one correction, scaled once with the rest.
  if (isTV) type.title = {...type.title, fontFamily: fontFamilies[800], fontWeight: '800', letterSpacing: -0.03 * 44 * textScale};
  return {
    form, column, scale: scales[form], emphasis: emphasisColors,
    platform, layoutMode, width, isTV, colors, space: spacing, type, spec: componentSpec, layout, layers, targets,
    radii: isTV ? platformRadii.tv : platformRadii.phone, baseRadii: radii,
    gutter: isTV ? layout.gutter.tv : layoutMode === 'desktop' ? width >= layout.designWidth.desktop ? layout.gutter.desktop : layout.gutter.desktopNarrow : platform === 'tablet' ? layout.gutter.tablet : layout.gutter.phone,
    // Layout consumes the OS scale; Text/TextInput continue to apply it themselves.
    motion, reduceMotion, textScale, fontScale: Number.isFinite(fontScale) ? Math.max(1, fontScale) : 1,
  };
}
export type Theme = ReturnType<typeof createTheme>;
const ThemeContext = createContext<Theme | undefined>(undefined);

export function ThemeProvider({children, width: surfaceWidth, reduceMotion = false, textScale, textSize}: {children: React.ReactNode; width?: number; reduceMotion?: boolean; /** Numeric scale used by specimens and the confirmed preference itself. */ textScale?: number; /** Confirmed `appearance.textSize`; ignored outside TV per ruling R46. */ textSize?: TextSizePreference}) {
  const dimensions = useWindowDimensions();
  // A surface as wide as the window is the window: keep its height, so a phone held sideways stays the phone form even
  // under a nested provider that passes the inherited width (NativeSettingsAppearance wraps the whole shell).
  const width = surfaceWidth ?? dimensions.width, height = surfaceWidth === undefined || surfaceWidth === dimensions.width ? dimensions.height : undefined;
  const isTV = Platform.isTV === true;
  const scale = textScale ?? textScaleFor(textSize, isTV);
  publishedTextScale = scale;
  const fontScale = dimensions.fontScale;
  const theme = useMemo(() => createTheme({isTV, width, height, reduceMotion, textScale: scale, fontScale}), [isTV, width, height, reduceMotion, scale, fontScale]);
  return React.createElement(ThemeContext.Provider, {value: theme}, children);
}

export function useTheme(): Theme {
  const theme = useContext(ThemeContext);
  if (!theme) throw new Error('A native theme provider is required.');
  return theme;
}
