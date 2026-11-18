import React, {type ReactNode} from 'react';
import {StyleSheet, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {BarScrim} from '../layout/TopBar';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

export type PlayerChromeFrameProps = Readonly<{
  /** The video surface, or the frozen frame when playback failed. */
  picture: ReactNode;
  /** Whether the controls show; the player decides when they hide. */
  chromeVisible: boolean;
  /** Close, the title, and the round buttons opposite it. */
  top: ReactNode;
  /** The player's scrubber, the full width of the bar. */
  scrubber: ReactNode;
  /** Previous, back, Play or Pause, forward, next. In the middle of the picture on a phone; in the middle of the
   * bottom bar on a tablet and TV. */
  transport: ReactNode;
  /** The bar's left end (Episodes) and right end (volume, subtitles, quality, speed, full screen). */
  leading?: ReactNode; trailing?: ReactNode;
  /** Skip intro, Up next: the prompt corner, bottom right above the bar. Stays when the chrome hides. */
  prompts?: ReactNode;
  testID?: string;
}>;

/** Frame 7, the video player's chrome (phone-player, desktop-player and tv-player mock-ups): the one frame with no
 * backdrop, because the picture is the page (black around it). Every region is fixed: the top bar on its scrim, the
 * scrubber, the transport, the bar's two ends, the prompt corner. There is no flexible region: options open as
 * panels over the picture. */
export function PlayerChromeFrame({picture, chromeVisible, top, scrubber, transport, leading, trailing, prompts, testID}: PlayerChromeFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), phone = frame.form === 'phone';
  const side = frame.form === 'tv' ? theme.layout.gutter.tvPlayer : Math.max(frame.gutter, insets.left, insets.right);
  const shown = {opacity: chromeVisible ? 1 : 0} as const, touch = chromeVisible ? 'box-none' : 'none';
  const end = {flex: 1, flexDirection: 'row', alignItems: 'center', gap: theme.space.xs} as const;
  return <View testID={testID} style={{flex: 1, backgroundColor: theme.colors.shadow}}>
    <View style={StyleSheet.absoluteFill}>{picture}</View>
    <View pointerEvents={touch} style={[{position: 'absolute', left: 0, right: 0, top: 0, paddingTop: insets.top + theme.space.xs, paddingHorizontal: side, paddingBottom: theme.space.lg}, shown]}><BarScrim/>{top}</View>
    {phone && <View pointerEvents={touch} style={[StyleSheet.absoluteFill, {alignItems: 'center', justifyContent: 'center'}, shown]}>{transport}</View>}
    {/* The prompt corner rides on the bottom bar's box, which keeps its size while the chrome is hidden, so a prompt never moves. */}
    <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, bottom: 0}}>
      <View pointerEvents="box-none" style={{alignItems: 'flex-end', paddingHorizontal: side}}>{prompts}</View>
      <View pointerEvents={touch} style={[{paddingTop: theme.space.lg, paddingHorizontal: side, paddingBottom: Math.max(insets.bottom, theme.space.sm), gap: theme.space.xs}, shown]}>
        <View pointerEvents="none" style={[StyleSheet.absoluteFill, {transform: [{scaleY: -1}]}]}><BarScrim/></View>
        {scrubber}
        {/* Three fixed places: the ends take equal room, so the transport stays centred whatever each end holds. */}
        <View style={{flexDirection: 'row', alignItems: 'center'}}>
          <View style={end}>{leading}</View>
          {!phone && transport}
          <View style={[end, {justifyContent: 'flex-end'}]}>{trailing}</View>
        </View>
      </View>
    </View>
  </View>;
}
