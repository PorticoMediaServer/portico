import React, {forwardRef, useSyncExternalStore} from 'react';
import {Platform, View} from 'react-native';
import {getLiteEffects, subscribeLiteEffects} from './motion-policy';
import {BlurView as NativeBlurView, type BlurViewProps} from '@react-native-community/blur';

/** Default dark translucent glass tint when no fallback colour was authored. */
export const DEFAULT_TRANSLUCENT_FALLBACK = 'rgba(12, 24, 34, 0.88)';

/** Root§71 U-MOB-005: the three authored Apple strengths and the public material
 * each one names in the pinned dependency (`BlurViewProps.blurType` union of
 * @react-native-community/blur 4.4.1: ultraThinMaterialDark, thinMaterialDark,
 * materialDark). No fourth tier is invented here: 14, 20, 26 and every other
 * amount keep the caller's own explicit blurType. */
const APPLE_MATERIAL = Object.freeze({18: 'ultraThinMaterialDark', 24: 'thinMaterialDark', 30: 'materialDark'} as const);
export type AppleMaterial = (typeof APPLE_MATERIAL)[keyof typeof APPLE_MATERIAL];

/** The public material for one authored Apple amount, or undefined when nothing
 * should be rewritten. Two guards keep this honest: an explicit style the caller
 * drew (light, xlight, a chrome/material variant) always wins over the tiers, and
 * only an explicit dark or an absent style is mapped. Reads nothing but its two
 * arguments. */
export function appleMaterialFor(amount: number | undefined, style: string | undefined): AppleMaterial | undefined {
  if (style !== undefined && style !== 'dark') return undefined;
  return amount === 18 ? APPLE_MATERIAL[18] : amount === 24 ? APPLE_MATERIAL[24] : amount === 30 ? APPLE_MATERIAL[30] : undefined;
}

/** Supported scope, stated plainly:
 * - Android renders a clean translucent dark surface `<View>` using
 *   `reducedTransparencyFallbackColor` (or `DEFAULT_TRANSLUCENT_FALLBACK`), avoiding
 *   `@react-native-community/blur`'s per-frame root capture on Android.
 * - Apple platforms (iOS / iPadOS / tvOS) keep hardware `UIBlurEffect` unless the
 *   `lite` performance tier is active.
 * - On non-TV iOS the three authored strengths (18, 24, 30) select their own public
 *   material (`ultraThinMaterialDark`, `thinMaterialDark`, `materialDark`); tvOS keeps
 *   the caller's style because the pinned 4.4.1 bridge gates system-material enums
 *   with `!TARGET_OS_TV` (`ios/BlurView.mm:142`). */
export const BlurView = forwardRef<View, BlurViewProps>(function BlurView(props, ref) {
  // Lite (performance) and Android draw a translucent surface instead of a live blur.
  const lite = useSyncExternalStore(subscribeLiteEffects, getLiteEffects, getLiteEffects);
  if (Platform.OS === 'android' || lite) {
    const fallback = (props as {reducedTransparencyFallbackColor?: string}).reducedTransparencyFallbackColor || DEFAULT_TRANSLUCENT_FALLBACK;
    return <View ref={ref} pointerEvents={props.pointerEvents} style={[props.style, {backgroundColor: fallback}]}>{props.children}</View>;
  }
  const appleMaterialSupport = Platform.OS === 'ios' && Platform.isTV !== true;
  const material = appleMaterialSupport ? appleMaterialFor(props.blurAmount, props.blurType) : undefined;
  // The union of the iOS and Android prop types is why the mapped object is cast
  // back to the exported props type: the material names are members of the iOS
  // blurType union.
  const mapped = material ? ({...props, blurType: material} as BlurViewProps) : props;
  return <NativeBlurView {...mapped} ref={ref}/>;
});

export {glassRecipe, phoneTabBarRecipe} from './glass';
