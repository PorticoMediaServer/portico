import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import Svg, {Circle} from 'react-native-svg';
import Animated, {cancelAnimation, Easing, useAnimatedStyle, useSharedValue, withRepeat, withTiming} from 'react-native-reanimated';
import {playbackWaitingDelayMs} from '../../../../../packages/client-core/src/presentation/playback-waiting';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {colorAlpha} from '../controls/control-model';

/** states.html f23, as the web player draws it (centred at 44% of the picture): the turning ring and its one plain line over the picture, once a wait
 * has lasted three seconds. "Getting ready" while the server converts the stream (definition 07, rule 3), the
 * connection line otherwise. It observes the playback service's waiting facts and owns no retry, clock or transport;
 * it never takes a touch or focus. */
export function PlaybackWaiting({waiting, preparing = false}: {waiting: boolean; preparing?: boolean}) {
  const theme = useTheme(), motion = useMotion(), turn = useSharedValue(0), [shown, setShown] = useState(false);
  useEffect(() => {
    setShown(false);
    if (!waiting) return;
    const timer = setTimeout(() => setShown(true), playbackWaitingDelayMs);
    return () => clearTimeout(timer);
  }, [waiting]);
  const turning = shown && !motion.reduce;
  useEffect(() => {
    if (!turning) {cancelAnimation(turn); turn.value = 0; return;}
    turn.value = withRepeat(withTiming(1, {duration: 900, easing: Easing.linear}), -1, false);
    return () => cancelAnimation(turn);
  }, [turning, turn]);
  const ring = useAnimatedStyle(() => ({transform: [{rotate: `${-90 + turn.value * 360}deg`}]}));
  if (!waiting || !shown) return null;
  const size = theme.isTV ? 96 : 64, line = defaultI18n.t(preparing ? 'player.gettingReady' : 'playback.message.network_stall');
  return <View testID="playback-waiting" pointerEvents="none" accessibilityRole="text" accessibilityLiveRegion="polite" accessibilityLabel={line}
    style={{position: 'absolute', left: 24, right: 24, top: 0, bottom: '12%', alignItems: 'center', justifyContent: 'center', gap: theme.isTV ? 24 : 16}}>
    <Animated.View style={ring}>
      <Svg width={size} height={size} viewBox="0 0 24 24" accessible={false}>
        <Circle cx={12} cy={12} r={9} fill="none" stroke={theme.colors.text} strokeOpacity={0.16} strokeWidth={1.6}/>
        <Circle cx={12} cy={12} r={9} fill="none" stroke={theme.colors.text} strokeWidth={1.6} strokeLinecap="round" strokeDasharray="14.55 42"/>
      </Svg>
    </Animated.View>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.textSecondary, textAlign: 'center', paddingHorizontal: 14, paddingVertical: 7, borderRadius: 10,
      backgroundColor: colorAlpha(theme.colors.ink, .62), overflow: 'hidden', ...(theme.isTV ? {fontSize: 24 * theme.textScale, lineHeight: 32 * theme.textScale} : {})}]}>{line}</Text>
  </View>;
}
