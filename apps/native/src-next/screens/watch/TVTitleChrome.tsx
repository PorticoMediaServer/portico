import React, {Children, useEffect, useId, type ReactNode} from 'react';
import {Text, View, type StyleProp, type ViewStyle} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withDelay, withTiming} from 'react-native-reanimated';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {AmbientArt, type AmbientArtSource, type ArtworkSource} from '../../kit/artwork';
import {nativeType, useTheme} from '../../kit/theme';
import {useMotion} from '../../kit/useMotion';

function useBrowseOpacity(browsing: boolean, inverted = false, slow = false) {
  const motion = useMotion(), opacity = useSharedValue(inverted ? Number(!browsing) : Number(browsing));
  useEffect(() => {opacity.value = withTiming(inverted ? Number(!browsing) : Number(browsing), motion.timing(slow ? 'slow' : 'standard'));}, [browsing, inverted, motion, opacity, slow]);
  return useAnimatedStyle(() => ({opacity: opacity.value}));
}

/** The title owns the backdrop even while focus browses its rows. */
export function TVTitleBackdrop({source, foreground, browsing}: {source?: AmbientArtSource; foreground?: ArtworkSource; browsing: boolean}) {
  const gradient = useId(), shade = useBrowseOpacity(browsing, false, true);
  return <View pointerEvents="none" style={{position: 'absolute', inset: 0}}>
    <AmbientArt source={source} foreground={foreground} treatment="title" style={{position: 'absolute', inset: 0}}/>
    <Animated.View style={[{position: 'absolute', inset: 0}, shade]}>
      <Svg width="100%" height="100%" accessible={false}>
        <Defs><LinearGradient id={gradient} x1="0" y1="0" x2="0" y2="100%">
          <Stop offset="0" stopColor="#071018" stopOpacity={.28}/><Stop offset="0.2" stopColor="#071018" stopOpacity={.7}/>
          <Stop offset="0.4" stopColor="#071018" stopOpacity={.92}/><Stop offset="0.62" stopColor="#071018" stopOpacity={.97}/><Stop offset="0.86" stopColor="#071018"/>
        </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${gradient})`}/>
      </Svg>
    </Animated.View>
  </View>;
}

export function TVTitleCompactHeader({title, browsing, children}: {title: string; browsing: boolean; children: ReactNode}) {
  const theme = useTheme(), fade = useBrowseOpacity(browsing);
  return <Animated.View pointerEvents="none" accessibilityElementsHidden={!browsing} importantForAccessibility={browsing ? 'auto' : 'no-hide-descendants'}
    testID="watch-title-compact" style={[{position: 'absolute', left: 200, right: 96, top: 66, zIndex: 4}, fade]}>
    <Text numberOfLines={1} accessibilityRole="header" style={[nativeType({size: 62, weight: 800, lineHeight: 62, tracking: -.035}, theme.textScale),
      {color: theme.colors.text, textShadowOffset: {width: 0, height: 2}, textShadowRadius: 30, textShadowColor: '#07101899'}]}>{title}</Text>
    <View style={{marginTop: 42}}>{children}</View>
  </Animated.View>;
}

/** motion.html 4.4: entering a title page, its lockup, details, buttons and tabs rise 24 px and
 * fade in 60 ms apart (standard 180, out; at most three steps). A backdrop both pages share
 * doesn't move, so only the copy arrives. Reduced motion: the same fade, no rise or stagger. */
function TVTitleArrival({index, children}: {index: number; children: ReactNode}) {
  const motion = useMotion(), arrived = useSharedValue(0);
  useEffect(() => {
    arrived.value = withDelay(motion.transforms ? Math.min(index, 3) * 60 : 0, withTiming(1, motion.timing('standard', 'out')));
  }, [arrived, index, motion]);
  const style = useAnimatedStyle(() => ({opacity: arrived.value, transform: motion.transforms ? [{translateY: (1 - arrived.value) * 24}] : []}));
  return <Animated.View pointerEvents="box-none" style={style}>{children}</Animated.View>;
}

export function TVTitleHeroCopy({browsing, style, children}: {browsing: boolean; style: StyleProp<ViewStyle>; children: ReactNode}) {
  const fade = useBrowseOpacity(browsing, true);
  return <Animated.View style={[style, fade]}>{Children.toArray(children).filter(Boolean).map((child, index) =>
    <TVTitleArrival key={React.isValidElement(child) && child.key != null ? child.key : index} index={index}>{child}</TVTitleArrival>)}</Animated.View>;
}
