import React, {forwardRef, useSyncExternalStore} from 'react';
import {Platform, View} from 'react-native';
import {getLiteEffects, subscribeLiteEffects} from './motion-policy';
import {BlurView as NativeBlurView, type BlurViewProps} from '@react-native-community/blur';

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

/** Supported scope, stated plainly: the three material tiers exist only on
 * non-TV iOS. The pinned 4.4.1 bridge gates its system-material enum mappings
 * with !TARGET_OS_TV (ios/BlurView.mm:142), so a tvOS build cannot reach them and
 * silently falls back to its dark style (:169); tvOS therefore keeps the style it
 * was given, exactly as before. Android receives the original component and its
 * props unchanged, where blurAmount itself is the drawn value. On non-TV iOS the
 * three authored strengths select their own public material, so 18, 24 and 30 no
 * longer collapse into one dark style, while blurAmount stays passed through
 * because the pinned bridge still accepts it. Children, ref, style, layout,
 * pointer and native event props and the authored reducedTransparencyFallbackColor
 * are never rewritten. */
const appleMaterialSupport = Platform.OS === 'ios' && Platform.isTV !== true;
export const BlurView = forwardRef<View, BlurViewProps>(function BlurView(props, ref) {
  // Lite (performance) draws the authored fallback fill instead of a live blur.
  const lite = useSyncExternalStore(subscribeLiteEffects, getLiteEffects, getLiteEffects);
  if (lite) return <View ref={ref} pointerEvents={props.pointerEvents} style={[props.style, {backgroundColor: (props as {reducedTransparencyFallbackColor?: string}).reducedTransparencyFallbackColor}]}>{props.children}</View>;
  const material = appleMaterialSupport ? appleMaterialFor(props.blurAmount, props.blurType) : undefined;
  // The union of the iOS and Android prop types is why the mapped object is cast
  // back to the exported props type: the material names are members of the iOS
  // blurType union, and the Android branch must keep its original blurType.
  const mapped = material ? ({...props, blurType: material} as BlurViewProps) : props;
  return <NativeBlurView {...mapped} ref={ref}/>;
});

export {glassRecipe, phoneTabBarRecipe} from './glass';
