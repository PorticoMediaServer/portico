import React, {useEffect, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import Animated, {cancelAnimation, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {textLines, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';

/** tv-player .flab / motion4.5: paint only; the original native control keeps focus. An end-aligned label (the player's
 * last control) gets the label's full width of room to its left: with no left bound it took the control's width and cut
 * "Playback options" to "P…". */
export function TVControlLabel({focused, label, detail, primary = false, align = 'center', children}: {
  focused: boolean; label: string; detail?: string; primary?: boolean; align?: 'center' | 'end'; children: ReactNode;
}) {
  const theme = useTheme(), motion = useMotion(), opacity = useSharedValue(0);
  useEffect(() => {
    opacity.value = withTiming(focused ? 1 : 0, motion.timing('quick'));
    return () => cancelAnimation(opacity);
  }, [focused, motion, opacity]);
  const fade = useAnimatedStyle(() => ({opacity: opacity.value}));
  if (!theme.isTV) return <>{children}</>;
  return <View style={{position: 'relative', flexShrink: 0}}>
    {children}
    <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
      style={[{position: 'absolute', bottom: '100%', marginBottom: primary ? 12 : 16,
        left: align === 'end' ? -440 : -220, right: align === 'end' ? 0 : -220, alignItems: align === 'end' ? 'flex-end' : 'center'}, fade]}>
      <View style={{maxWidth: 440, flexDirection: 'row', alignItems: 'baseline', gap: 12, paddingVertical: 7, paddingHorizontal: 16,
        borderRadius: 10, borderWidth: 1, borderColor: 'rgba(237,245,250,.12)', backgroundColor: 'rgba(7,16,24,.9)'}}>
        <Text {...textLines(1)} style={[theme.type.tvControlLabel, {color: theme.colors.text, flexShrink: 1}]}>{label}</Text>
        {!!detail && <Text {...textLines(1)} style={[theme.type.tvControlDetail, {color: theme.colors.textSecondary, flexShrink: 1}]}>{detail}</Text>}
      </View>
    </Animated.View>
  </View>;
}
