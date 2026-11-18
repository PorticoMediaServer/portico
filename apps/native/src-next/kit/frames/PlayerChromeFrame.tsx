import React, {type ReactNode} from 'react';
import {StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {frameSpec} from '@portico/design';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

/** The time read-outs; the frame places them for the form (under the scrubber, either side of it, or as one clock). */
export type PlayerReadouts = Readonly<{position: string; /** With a minus sign: the time left. */ remaining?: string; duration?: string; /** "Ends 4:48 PM". */ endsAt?: string; /** Said in place of the time left. */ live?: string}>;
export type PlayerChromeFrameProps = Readonly<{
  /** The video surface, or the frozen frame when playback failed. */
  picture: ReactNode;
  /** Whether the controls show; the player decides when they hide. */
  chromeVisible: boolean;
  /** Close (a `PlayerControl onPicture`), top left. Not drawn on TV, where the remote's Back leaves. */
  close: ReactNode;
  /** `PlayerTitle`, beside Close. Not drawn on an upright phone, where the watch page under the picture has the title. */
  title?: ReactNode;
  /** The buttons opposite Close (`PlayerControl onPicture`): Play on, picture in picture, settings. */
  tools?: ReactNode;
  /** The scrubber (`PlayerScrubBar`, inside the screen's own timeline). */
  scrubber: ReactNode;
  times: PlayerReadouts;
  /** Previous, back, Play or Pause, forward, next (`PlayerControl`s). In the middle of the bottom bar on a tablet and
   * TV; in the middle of the picture on a phone. */
  transport: ReactNode;
  /** The bar's left end (Episodes) and right end (the options: `PlayerPill`s, full screen). */
  leading?: ReactNode; trailing?: ReactNode;
  /** `PlayerPrompt`: Skip intro, Up next. The prompt corner, bottom right above the bar; it stays when the chrome hides. */
  prompts?: ReactNode;
  /** An upright phone only: the watch page under the picture (the title page's own content). */
  body?: ReactNode;
  testID?: string;
}>;

/** The top and bottom scrims over the picture, from the spec's stops. */
function Shade() {
  const theme = useTheme(), {height} = useWindowDimensions(), spec = frameSpec.player.scrim;
  const stops = (list: readonly (readonly number[])[]) => list.map(([opacity, at]) => <Stop key={at} offset={Math.min(1, at / height)} stopColor={theme.colors.livePlayerScrim} stopOpacity={opacity}/>);
  return <Svg pointerEvents="none" accessible={false} width="100%" height="100%" style={StyleSheet.absoluteFill}>
    <Defs><LinearGradient id="player-shade-top" x1="0" y1="0" x2="0" y2="1">{stops(spec.top)}</LinearGradient><LinearGradient id="player-shade-bottom" x1="0" y1="1" x2="0" y2="0">{stops(spec.bottom)}</LinearGradient></Defs>
    <Rect width="100%" height="100%" fill="url(#player-shade-top)"/><Rect width="100%" height="100%" fill="url(#player-shade-bottom)"/>
  </Svg>;
}

/** Frame 7, the video player's chrome, in its three drawn forms. Tablet and TV (tablet-play, tv-player): the picture
 * fills the window; top bar, then a bar of scrubber, read-outs and a row of left end, transport, right end. A phone on
 * its side (phone-player 1 and 2): the same, with the transport in the middle of the picture and the read-outs either
 * side of the scrubber. An upright phone (phone-player 4): the picture is a 16:9 strip under the status bar with the
 * chrome inside it, the scrubber on its lower edge, and the watch page below. No backdrop under the picture; every
 * region is fixed; the parts that fill it are `kit/player`'s. */
export function PlayerChromeFrame({picture, chromeVisible, close, title, tools, scrubber, times, transport, leading, trailing, prompts, body, testID}: PlayerChromeFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), spec = frameSpec.player, {form} = frame;
  const phone = form === 'phone', upright = phone && window.height > window.width, sideways = phone && !upright, tv = form === 'tv';
  const edge = upright ? spec.portrait.edge : Math.max(spec.edge[form], insets.left, insets.right);
  const shown = {opacity: chromeVisible ? 1 : 0} as const, touch = chromeVisible ? 'box-none' : 'none';
  const time = [theme.type.playerTime, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums' as const]}], strong = {color: theme.colors.text, fontFamily: theme.type.button.fontFamily};
  const position = <Text style={[time, strong]}>{times.position}</Text>;
  const remaining = <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
    {!sideways && times.endsAt !== undefined && <Text style={time}>{times.endsAt}</Text>}
    {times.live !== undefined && <View style={{width: theme.space.xs, height: theme.space.xs, borderRadius: theme.space.xs / 2, backgroundColor: theme.colors.record}}/>}
    <Text style={[time, strong]}>{times.live ?? times.remaining}</Text>
  </View>;
  const row = {flexDirection: 'row', alignItems: 'center'} as const, end = [row, {flex: 1, gap: spec.endGap[form]}];
  const centre = <View pointerEvents={touch} style={[StyleSheet.absoluteFill, row, {justifyContent: 'center', gap: upright ? undefined : theme.space.xl}, upright && {justifyContent: 'space-evenly'}, shown]}>{transport}</View>;
  const top = <View pointerEvents={touch} style={[row, {position: 'absolute', left: 0, right: 0, top: 0, alignItems: 'flex-start', gap: upright ? 0 : spec.titleGap[form],
    paddingTop: upright ? spec.portrait.top : Math.max(spec.top[form], insets.top), paddingHorizontal: edge}, shown]}>
    {!tv && close}<View style={{flex: 1, minWidth: 0, alignSelf: 'center'}}>{!upright && title}</View><View style={[row, {gap: upright ? 0 : theme.space.controlGap}]}>{tools}</View>
  </View>;
  const stage = <View style={upright ? {aspectRatio: spec.portrait.aspect, overflow: 'hidden'} : {flex: 1, overflow: 'hidden'}}>
    <View style={StyleSheet.absoluteFill}>{picture}</View>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, shown]}>{upright ? <View style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.colors.dim, spec.portrait.shade)}]}/> : <Shade/>}</View>
    {top}
    {phone && centre}
    {/* The prompt corner rides on the bar's box, which keeps its size while the chrome is hidden, so a prompt never moves. */}
    <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, bottom: 0}}>
      <View pointerEvents="box-none" style={{alignItems: 'flex-end', paddingHorizontal: edge, paddingBottom: upright ? theme.space.xs : theme.space.sm}}>{prompts}</View>
      {upright ? <View pointerEvents={touch} style={shown}>
        <View style={[row, {justifyContent: 'space-between', paddingLeft: spec.portrait.timeInset, paddingRight: edge, paddingBottom: spec.portrait.bottom, minHeight: theme.targets.touch}]}>
          {times.live !== undefined ? remaining : <Text style={time}><Text style={strong}>{times.position}</Text>{times.duration !== undefined ? ` / ${times.duration}` : ''}</Text>}<View style={[row, {gap: spec.endGap[form]}]}>{trailing}</View>
        </View>
        {scrubber}
      </View> : <View pointerEvents={touch} style={[{paddingHorizontal: edge, paddingBottom: Math.max(spec.bottom[form], insets.bottom)}, shown]}>
        {sideways ? <View style={[row, {gap: theme.space.controlGap}]}>{position}<View style={{flex: 1}}>{scrubber}</View>{remaining}</View>
          : <View style={{gap: spec.timesGap[form]}}>{scrubber}<View style={[row, {justifyContent: 'space-between'}]}>{position}{remaining}</View></View>}
        {/* Three fixed places: the ends take equal room, so the transport stays centred whatever each end holds. */}
        <View style={[row, {marginTop: spec.controlsGap[form], minHeight: theme.spec.transport.primary.size[theme.platform]}, sideways && {minHeight: spec.pill.height[form]}]}>
          <View style={end}>{leading}</View>
          {!phone && <View style={[row, {gap: theme.spec.transport.gap[theme.platform]}]}>{transport}</View>}
          <View style={[end, {justifyContent: 'flex-end'}]}>{trailing}</View>
        </View>
      </View>}
    </View>
  </View>;
  return <View testID={testID} style={{flex: 1, backgroundColor: upright ? theme.colors.ink : theme.colors.shadow, paddingTop: upright ? insets.top : 0}}>
    {stage}
    {upright && <View style={{flex: 1}}>{body}</View>}
  </View>;
}
