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
  /** Back, the title and Play on. */
  top: ReactNode;
  /** The centre transport (phone) or nothing where the bottom bar carries it. */
  transport?: ReactNode;
  /** The scrubber and the options under it. */
  bottom: ReactNode;
  /** Skip intro, Up next: the prompt corner, bottom right above the bar. Stays when the chrome hides. */
  prompts?: ReactNode;
  testID?: string;
}>;

/** Frame 7, the video player's chrome: the one frame with no backdrop, because the picture is the page (black
 * around it). Every region is fixed: the top bar and bottom bar on their scrims at the edges, the transport in the
 * middle, prompts in their corner. There is no flexible region: options open as panels over the picture. */
export function PlayerChromeFrame({picture, chromeVisible, top, transport, bottom, prompts, testID}: PlayerChromeFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets();
  const side = frame.form === 'tv' ? theme.layout.gutter.tvPlayer : Math.max(frame.gutter, insets.left, insets.right);
  const shown = {opacity: chromeVisible ? 1 : 0} as const, touch = chromeVisible ? 'box-none' : 'none';
  return <View testID={testID} style={{flex: 1, backgroundColor: theme.colors.shadow}}>
    <View style={StyleSheet.absoluteFill}>{picture}</View>
    <View pointerEvents={touch} style={[{position: 'absolute', left: 0, right: 0, top: 0, paddingTop: insets.top, paddingHorizontal: side, paddingBottom: theme.space.lg}, shown]}><BarScrim/>{top}</View>
    {transport && <View pointerEvents={touch} style={[StyleSheet.absoluteFill, {alignItems: 'center', justifyContent: 'center'}, shown]}>{transport}</View>}
    {/* The prompt corner rides on the bottom bar's box, which keeps its size while the chrome is hidden, so a prompt never moves. */}
    <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, bottom: 0}}>
      <View pointerEvents="box-none" style={{alignItems: 'flex-end', paddingHorizontal: side}}>{prompts}</View>
      <View pointerEvents={touch} style={[{paddingTop: theme.space.lg, paddingHorizontal: side, paddingBottom: Math.max(insets.bottom, theme.space.sm)}, shown]}>
        <View pointerEvents="none" style={[StyleSheet.absoluteFill, {transform: [{scaleY: -1}]}]}><BarScrim/></View>{bottom}
      </View>
    </View>
  </View>;
}
