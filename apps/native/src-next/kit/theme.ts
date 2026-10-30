import React, {createContext, useContext, useMemo} from 'react';
import {Platform, useWindowDimensions, type TextStyle} from 'react-native';
import {colors, componentSpec, layers, layout, motion, platformRadii, radii, spacing, targets, typography, typeScale, type TypeRole, type TypeStyle} from '@portico/design';

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

/** Native fonts supply their own normal line metrics. Tracking is specified in em. */
export function nativeType(style: TypeStyle, textScale = 1): TextStyle {
  return {
    fontFamily: fontFamilies[style.weight],
    fontSize: style.size * textScale,
    letterSpacing: style.tracking * style.size * textScale,
    ...(style.lineHeight === 'normal' ? {} : {lineHeight: style.lineHeight * textScale}),
  };
}

export type ThemeOptions = {isTV: boolean; width: number; reduceMotion?: boolean; textScale?: number; fontScale?: number};
export function createTheme({isTV, width, reduceMotion = false, textScale = 1, fontScale = 1}: ThemeOptions) {
  // componentSpec names its desktop column web; layoutMode names the actual shell layout.
  const platform: 'phone' | 'tablet' | 'web' | 'tv' = isTV ? 'tv' : width >= layout.breakpoint.desktop ? 'web' : width < layout.breakpoint.phone ? 'phone' : 'tablet';
  const layoutMode: 'phone' | 'desktop' | 'tv' = isTV ? 'tv' : platform === 'web' ? 'desktop' : 'phone';
  const scale = typeScale[isTV ? 'tenFoot' : layoutMode === 'desktop' ? 'desktop' : 'handheld'];
  const type = Object.fromEntries(Object.entries(scale).map(([role, value]) => [role, nativeType(value, textScale)])) as Record<TypeRole, TextStyle>;
  // TV page and panel headings are drawn at 44/800 with -.03 tracking (tv-more .pg,
  // the TV panel h3); the shared ten-foot scale publishes 44/700 at -.027. Both
  // Android TV and Apple TV draw this one correction, scaled once with the rest.
  if (isTV) type.title = {...type.title, fontFamily: fontFamilies[800], fontWeight: '800', letterSpacing: -0.03 * 44 * textScale};
  return {
    platform, layoutMode, width, isTV, colors, space: spacing, type, spec: componentSpec, layout, layers, targets,
    radii: isTV ? platformRadii.tv : platformRadii.phone, baseRadii: radii,
    gutter: isTV ? layout.gutter.tv : layoutMode === 'desktop' ? width >= layout.designWidth.desktop ? layout.gutter.desktop : layout.gutter.desktopNarrow : layout.gutter.phone,
    // Layout consumes the OS scale; Text/TextInput continue to apply it themselves.
    motion, reduceMotion, textScale, fontScale: Number.isFinite(fontScale) ? Math.max(1, fontScale) : 1,
  };
}
export type Theme = ReturnType<typeof createTheme>;
const ThemeContext = createContext<Theme | undefined>(undefined);

export function ThemeProvider({children, width: surfaceWidth, reduceMotion = false, textScale, textSize}: {children: React.ReactNode; width?: number; reduceMotion?: boolean; /** Numeric scale used by specimens and the confirmed preference itself. */ textScale?: number; /** Confirmed `appearance.textSize`; ignored outside TV per ruling R46. */ textSize?: TextSizePreference}) {
  const dimensions = useWindowDimensions();
  const width = surfaceWidth ?? dimensions.width;
  const isTV = Platform.isTV === true;
  const scale = textScale ?? textScaleFor(textSize, isTV);
  publishedTextScale = scale;
  const fontScale = dimensions.fontScale;
  const theme = useMemo(() => createTheme({isTV, width, reduceMotion, textScale: scale, fontScale}), [isTV, width, reduceMotion, scale, fontScale]);
  return React.createElement(ThemeContext.Provider, {value: theme}, children);
}

export function useTheme(): Theme {
  const theme = useContext(ThemeContext);
  if (!theme) throw new Error('A native theme provider is required.');
  return theme;
}
