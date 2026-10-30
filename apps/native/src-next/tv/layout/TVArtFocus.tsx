import React, {useEffect} from 'react';
import {View} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {focusSpec} from '@portico/design';
import {focusScale} from '../../kit/controls/control-model';
import {tvFocusGlow} from '../../kit/controls/Focusable';
import {useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';

/** Paint for a circular TV art card whose native Focusable owns the whole card.
 * The card grows together, but its pale ring follows only the artwork face. */
export function TVArtFocus({focused, width, faceSize, face, children}: {
  focused: boolean; width: number; faceSize: number; face: React.ReactNode; children?: React.ReactNode;
}) {
  const {colors} = useTheme();
  const motion = useMotion();
  const scale = useSharedValue(1), halo = useSharedValue(0);
  useEffect(() => {
    scale.value = withTiming(focused && motion.transforms ? focusScale('card', true) : 1, motion.timing('quick'));
    halo.value = withTiming(focused ? 1 : 0, motion.timing('quick'));
  }, [focused, motion, scale, halo]);
  const card = useAnimatedStyle(() => ({transform: [{scale: scale.value}]}));
  const ring = useAnimatedStyle(() => ({opacity: halo.value}));
  const gap = focusSpec.offsetOuter.tv, outer = gap + focusSpec.ringWidth.tv;
  return <Animated.View style={[{width, alignItems: 'center'}, card]}>
    <View style={{width: faceSize, height: faceSize}}>
      {face}
      <Animated.View pointerEvents="none" accessible={false} style={[{position: 'absolute', inset: -gap, borderRadius: faceSize / 2 + gap, borderWidth: gap, borderColor: colors.ink}, ring]}/>
      <Animated.View pointerEvents="none" accessible={false} style={[{position: 'absolute', inset: -outer, borderRadius: faceSize / 2 + outer,
        borderWidth: focusSpec.ringWidth.tv, borderColor: colors.focus, boxShadow: tvFocusGlow(colors.accentStrong)}, ring]}/>
    </View>
    {children}
  </Animated.View>;
}
