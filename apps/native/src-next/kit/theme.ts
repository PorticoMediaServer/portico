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

/** A sheet or menu title has no drawn line height ('normal'); it is given the measured floor (below), so the box is the
 * same on every device and the extra bold's ink is never cut. */
export const titleLine = (size: number): number => Math.ceil(size * measuredLineFloor);

/** The measured line (G2, 19 Nov, the dev gallery's `type-ladder`, `type-tv-*` and `type-phone-*` cases on the Android TV
 * emulator, the Apple TV simulator, the Pixel 6a and the iPhone 17 simulator): Manrope's ink, the ring of Å and the
 * tails of g, j and y, needs a line box of 1.34 of its size on Android and 1.36 on Apple at extra bold (1.30 and 1.32
 * at regular). In a tighter box Android cuts it at both edges and Apple at the top. The font's own line ('normal',
 * about 1.37) clears on both. One number for both systems, every form and every weight: the largest measured. */
export const measuredLineFloor = 1.36;

/** The height one line of a role draws at: its own line box, else the measured floor of its size (a part that must hold
 * N lines of a role, a list row's stride, reads it here). */
export function textLine(style: TextStyle): number {
  return typeof style.lineHeight === 'number' ? style.lineHeight : Math.ceil((style.fontSize ?? 0) * measuredLineFloor);
}

/** Native fonts supply their own normal line metrics. Tracking is specified in em. A text with a set line box is never
 * drawn tighter than `lineFloor` of its size: every caller gets `measuredLineFloor` (the theme's roles and the kit's own
 * lockups, sheet titles and states alike). */
export function nativeType(style: TypeStyle, textScale = 1, lineFloor = measuredLineFloor): TextStyle {
  const line = style.lineHeight === 'normal' ? undefined : Math.max(style.lineHeight, Math.ceil(style.size * lineFloor));
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
  const type = Object.fromEntries(Object.entries(formScale.type[form]).map(([role, value]) => {
    const level = levels[role as TypeRole];
    const style = nativeType(value, textScale);
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
