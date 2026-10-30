import React, {useEffect} from 'react';
import {type StyleProp, type ViewStyle} from 'react-native';
import Animated, {cancelAnimation, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {useOverlayPaintMode} from '../overlays/OverlayPaintContext';

export type SkeletonBlockProps = Readonly<{
  width?: number | `${number}%`;
  height?: number;
  radius?: number;
  /** A frozen light band from the reference, never an ambient animation. */
  shine?: boolean;
  accent?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}>;

export function SkeletonBlock({width, height = 14, radius, shine = false, accent = false, style, testID}: SkeletonBlockProps) {
  const theme = useTheme();
  const motion = useMotion(), paint = useOverlayPaintMode();
  const opacity = useSharedValue(0);
  useEffect(() => {
    if (!paint.interactive) {cancelAnimation(opacity); return;}
    opacity.value = withTiming(1, motion.timing('quick'));
    return () => cancelAnimation(opacity);
  }, [opacity, motion, paint.interactive]);
  const arrival = useAnimatedStyle(() => ({opacity: opacity.value}));
  const gradientId = React.useId().replace(/:/g, '');
  return <Animated.View testID={testID} accessible={false} focusable={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
    style={[{width, height, borderRadius: radius ?? theme.radii.artwork, overflow: 'hidden', backgroundColor: accent ? `${theme.colors.accent}2E` : theme.colors.raised}, style, arrival]}>
    {shine && !motion.reduce && <Svg pointerEvents="none" width="100%" height="100%" style={{position: 'absolute', inset: 0}}>
      <Defs><LinearGradient id={gradientId} x1="0%" y1="0%" x2="100%" y2="18%"><Stop offset="0%" stopColor={theme.colors.raised} /><Stop offset="35%" stopColor={theme.colors.raised} /><Stop offset="50%" stopColor={theme.colors.raisedBright} stopOpacity={.45} /><Stop offset="65%" stopColor={theme.colors.raised} /><Stop offset="100%" stopColor={theme.colors.raised} /></LinearGradient></Defs>
      <Rect width="100%" height="100%" fill={`url(#${gradientId})`} />
    </Svg>}
  </Animated.View>;
}
