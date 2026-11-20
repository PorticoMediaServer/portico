import React, {useMemo, type ReactNode} from 'react';
import {Text, View, type ViewProps} from 'react-native';
import Animated, {cancelAnimation, useAnimatedStyle, useSharedValue, withTiming, type LayoutAnimationFunction} from 'react-native-reanimated';
import {Button} from '../controls';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';

export function TVHomeSpotlight({hero, preview}: {hero: ReactNode; preview?: ReactNode}) {
  const {spec} = useTheme();
  return <View style={{height: preview ? spec.hero.tvHome.preview : spec.hero.tvHome.spotlight}}>
    <View pointerEvents="box-none" style={{position: 'absolute', inset: 0}}>{hero}</View>
    {preview && <View pointerEvents="none" style={{position: 'absolute', inset: 0, height: spec.hero.tvHome.artwork}}>{preview}</View>}
  </View>;
}

/** Only the spotlight's height change shifts the row. New content retains its natural position. */
export function TVHomeRow({onLayout, children}: {onLayout: ViewProps['onLayout']; children: ReactNode}) {
  const {spec} = useTheme(), delta = spec.hero.tvHome.spotlight - spec.hero.tvHome.preview;
  const motion = useMotion(), y = useSharedValue(0), transforms = motion.transforms;
  const shift = useMemo(() => motion.timing('slow', 'inOut'), [motion]);
  const layout = useMemo<LayoutAnimationFunction>(() => values => {
    'worklet';
    const moved = values.currentOriginY + y.value - values.targetOriginY;
    cancelAnimation(y);
    y.value = transforms && Math.abs(Math.abs(moved) - delta) < 1 ? moved : 0;
    y.value = withTiming(0, shift);
    return {initialValues: {originX: values.targetOriginX, originY: values.targetOriginY, width: values.targetWidth, height: values.targetHeight}, animations: {}};
  }, [delta, shift, transforms, y]);
  const position = useAnimatedStyle(() => ({transform: [{translateY: y.value}]}));
  return <Animated.View collapsable={false} layout={layout} onLayout={onLayout} style={position}>{children}</Animated.View>;
}

export function TVHomeCustomize({focus, label, description, onPress, disabled}: {
  focus: FocusRegionProps; label: string; description: string; onPress: () => void; disabled: boolean;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <FocusRegion {...focus} order={['customize']} enabled={!disabled}
    style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.lg, rowGap: theme.space.controlGap,
      paddingLeft: frame.gutter, paddingRight: frame.rightGutter, paddingVertical: theme.space.md}}>
    <Button focusId="customize" disabled={disabled} variant="glass" icon="i-sliders" onPress={onPress}>{label}</Button>
    <Text style={[theme.type.intro, {color: theme.colors.textTertiary, flexShrink: 1}]}>{description}</Text>
  </FocusRegion>;
}
