import React, {type ReactNode} from 'react';
import {StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {PlayerBeside, PlayerCompact} from '../player/parts';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {frameSpec} from '@portico/design';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {StartFocusScope} from '../focus';
import {useFrameMetrics, usePlayerUpright} from './metrics';

/** The time read-outs; the frame places them for the form (under the scrubber, either side of it, or as one clock). */
export type PlayerReadouts = Readonly<{position: string; /** With a minus sign: the time left. */ remaining?: string; duration?: string; /** "Ends 4:48 PM". */ endsAt?: string; /** Said in place of the time left. */ live?: string; /** Live TV, after the position: "2 min behind live". */ behind?: string;
  /** TV: the time of day, large at the top right with `endsAt` under it (tv-player 1). */ clock?: string}>;
export type PlayerChromeFrameProps = Readonly<{
  /** The video surface, or the frozen frame when playback failed. */
  picture: ReactNode;
  /** Whether the controls show; the player decides when they hide. */
  chromeVisible: boolean;
  /** Close (a `PlayerControl onPicture`), top left. Not drawn on TV, where the remote's Back leaves. */
  close: ReactNode;
  /** `PlayerTitle`, beside Close. Not drawn in the upright form, where the watch page under the picture has the title. */
  title?: ReactNode;
  /** The buttons opposite Close (`PlayerControl onPicture`; Live TV's Record and More). A TV with `times.clock` shows the clock there instead. */
  tools?: ReactNode;
  /** The scrubber (`PlayerScrubBar`, inside the screen's own timeline). */
  scrubber: ReactNode;
  times: PlayerReadouts;
  /** Previous, back, Play or Pause, forward, next (`PlayerControl`s). In the middle of the bottom bar on a tablet and
   * TV; in the middle of the picture on a phone and in the upright form. */
  transport: ReactNode;
  /** The bar's left end (Episodes) and right end (the options: `PlayerPill`s, full screen). */
  leading?: ReactNode; trailing?: ReactNode;
  /** `PlayerPrompt`: the prompt corner, bottom right above the bar; it stays when the chrome hides. In the upright form
   * a prompt sits in the strip's bottom row, where it covers nothing. Not drawn while a panel is open. */
  prompts?: ReactNode;
  /** `PlayerUpNext`, over the credits: the picture's bottom right corner, alone. The controls hide while it shows
   * (phone-player 6, tv-player 5). */
  card?: ReactNode;
  /** A `PlayerPanel`, open. The frame places it: under the picture in the upright form, beside the picture on a
   * tablet (the picture and its controls take the room that is left), and over the picture's right side on a phone on
   * its side and a TV, where the controls hide while it is open. */
  panel?: ReactNode;
  /** The panel is wide (`frameSpec.player.panel.wideWidth`: Playback info's table on TV). */
  panelWide?: boolean;
  /** TV: Episodes as a band across the bottom of the picture (`PlayerEpisodeBand`, tv-player 1 frame 3). The controls
   * hide while it shows; the bottom shade stays under it. */
  band?: ReactNode;
  /** The scrubber is held: the transport makes way for the preview (in the middle of the picture on a phone; the whole controls row on a tablet and TV, tv-player 2). */
  scrubbing?: boolean;
  /** TV, while scrubbing: `KeyHints`, in the controls row's place (tv-player 2). */
  hints?: ReactNode;
  /** The upright form only: the watch page under the picture (the title page's own content). */
  body?: ReactNode;
  testID?: string;
  /** TV: the control the page starts on (R1), by its `focusId` or `testID`. Never in the rail. */
  startFocus?: string;
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
 * side of the scrubber. Upright (phone-player 4, tablet-play 3): the picture is a 16:9 strip under the status bar with
 * the chrome inside it, the scrubber on its lower edge, and the watch page below. No backdrop under the picture; every
 * region is fixed; the parts that fill it are `kit/player`'s. */
export function PlayerChromeFrame({startFocus, ...props}: PlayerChromeFrameProps) {
  return <StartFocusScope id={startFocus}><PlayerChromeFrameBody {...props}/></StartFocusScope>;
}
function PlayerChromeFrameBody({picture, chromeVisible, close, title, tools, scrubber, times, transport, leading, trailing, prompts, card, panel, panelWide = false, band, scrubbing = false, hints, body, testID}: Omit<PlayerChromeFrameProps, 'startFocus'>) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), spec = frameSpec.player, {form} = frame;
  const phone = form === 'phone', tv = form === 'tv', upright = usePlayerUpright(), sideways = phone && !upright, beside = !!panel && !upright && !phone && !tv;
  const edge = upright ? spec.portrait.edge : Math.max(spec.edge[form], insets.left, insets.right);
  // The controls make way for a panel wherever it lies over the picture (phone-player 3, tv-player 4).
  const banded = tv && !!band, visible = chromeVisible && !card && !(panel && !beside) && !banded;
  const shown = {opacity: visible ? 1 : 0} as const, touch = visible ? 'box-none' : 'none';
  const time = [theme.type.playerTime, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums' as const]}], strong = {color: theme.colors.text, fontFamily: theme.type.button.fontFamily};
  const clock = tv && times.clock !== undefined ? <View style={{alignItems: 'flex-end'}}>
    <Text style={[theme.type.playerTitle, {color: theme.colors.text}]}>{times.clock}</Text>
    {times.endsAt !== undefined && <Text style={[theme.type.playerSubtitle, {color: theme.colors.textSecondary}]}>{times.endsAt}</Text>}
  </View> : undefined;
  const position = <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}><Text style={[time, strong]}>{times.position}</Text>{times.behind !== undefined && <Text numberOfLines={1} style={time}>{times.behind}</Text>}</View>;
  const remaining = <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
    {!sideways && clock === undefined && times.endsAt !== undefined && <Text style={time}>{times.endsAt}</Text>}
    {times.live !== undefined && <View style={{width: theme.space.xs, height: theme.space.xs, borderRadius: theme.space.xs / 2, backgroundColor: theme.colors.record}}/>}
    {(times.live ?? times.remaining) !== undefined && <Text style={[time, strong]}>{times.live ?? times.remaining}</Text>}
  </View>;
  const row = {flexDirection: 'row', alignItems: 'center'} as const;
  // Buttons keep their own height in a row, so the row is centred by a column around it, not by the row's own alignment.
  const centre = <View pointerEvents={touch} style={[StyleSheet.absoluteFill, {justifyContent: 'center'}, shown, scrubbing && {opacity: 0}]}>
    <View style={[row, {justifyContent: 'center', gap: upright ? spec.transport.upright[phone ? 'phone' : 'tablet'].gap : spec.transport.gap[form]}]}>{transport}</View>
  </View>;
  const top = <View pointerEvents={touch} style={[row, {position: 'absolute', left: 0, right: 0, top: 0, alignItems: 'flex-start', gap: upright ? 0 : spec.titleGap[form],
    paddingTop: upright ? spec.portrait.top : Math.max(spec.top[form], insets.top), paddingHorizontal: edge}, tv && panel ? {opacity: chromeVisible ? spec.panelDim : 0} : shown]}>
    {!tv && close}<View style={{flex: 1, minWidth: 0, alignSelf: 'center'}}>{!upright && title}</View>{clock ?? <View style={[row, {gap: upright ? 0 : theme.space.controlGap}]}>{tools}</View>}
  </View>;
  const stage = <View style={upright ? {aspectRatio: spec.portrait.aspect, overflow: 'hidden'} : {flex: 1, overflow: 'hidden'}}>
    <View style={StyleSheet.absoluteFill}>{picture}</View>
    <View pointerEvents="none" style={[StyleSheet.absoluteFill, banded ? undefined : shown]}>{upright ? <View style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.colors.dim, spec.portrait.shade)}]}/> : <Shade/>}</View>
    {tv && !!panel && <View pointerEvents="none" style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.colors.dim, spec.panelDim)}]}/>}
    {top}
    {(phone || upright) && centre}
    {!!card && !panel && <View pointerEvents="box-none" style={{position: 'absolute', right: edge, bottom: upright ? spec.portrait.bottom : Math.max(spec.bottom[form], insets.bottom), maxWidth: '100%'}}>{card}</View>}
    {/* The prompt corner rides on the bar's box, which keeps its size while the chrome is hidden, so a prompt never moves. */}
    <View pointerEvents="box-none" style={{position: 'absolute', left: 0, right: 0, bottom: 0}}>
      {!upright && !panel && !card && !banded && <View pointerEvents="box-none" style={{alignItems: 'flex-end', paddingHorizontal: edge, paddingBottom: theme.space.sm}}>{prompts}</View>}
      {upright ? <View pointerEvents="box-none">
        <View pointerEvents="box-none" style={[row, {gap: theme.space.xs, paddingLeft: spec.portrait.timeInset, paddingRight: edge, paddingBottom: spec.portrait.bottom, minHeight: theme.targets.touch}]}>
          {/* The read-out gives way first, so the options at the row's end always keep the gutter. */}
          <View style={[shown, {flexShrink: 1, minWidth: 0, overflow: 'hidden'}]}>{times.behind !== undefined ? position : times.live !== undefined ? remaining : <Text style={time}><Text style={strong}>{times.position}</Text>{times.duration !== undefined ? ` / ${times.duration}` : ''}</Text>}</View>
          {/* A prompt's own place in the strip, between the read-out and the options: it covers neither them nor the transport. */}
          <View pointerEvents="box-none" style={{flex: 1, minWidth: 0, alignItems: 'flex-end'}}>{!panel && !card && prompts}</View>
          <View pointerEvents={touch} style={[row, {gap: spec.endGap[form]}, shown]}>{trailing}</View>
        </View>
        <View pointerEvents={touch} style={shown}>{scrubber}</View>
      </View> : <View pointerEvents={touch} style={[{paddingHorizontal: edge, paddingBottom: Math.max(spec.bottom[form], insets.bottom)}, shown]}>
        {sideways ? <View style={[row, {gap: theme.space.controlGap}]}>{position}<View style={{flex: 1}}>{scrubber}</View>{remaining}</View>
          : <View style={{gap: spec.timesGap[form]}}>{scrubber}<View style={[row, {justifyContent: 'space-between'}, tv && scrubbing && {opacity: 0}]}>{position}{remaining}</View></View>}
        {/* Three fixed places. The transport is centred on the bar itself, not between the ends, so it stays in the middle whatever each end holds. */}
        <View>
          <View style={[row, scrubbing && {opacity: 0}, {justifyContent: 'space-between', gap: spec.endGap[form], marginTop: spec.controlsGap[form], minHeight: spec.transport.primary[form]}, sideways && {minHeight: spec.pill.height[form]}]}>
            {!phone && <View pointerEvents="box-none" style={[StyleSheet.absoluteFill, row, {justifyContent: 'center', gap: spec.transport.gap[form]}]}>{transport}</View>}
            <View style={[row, {flexShrink: 0, gap: spec.endGap[form]}]}>{leading}</View>
            <View style={[row, {flexShrink: 1, minWidth: 0, overflow: 'hidden', justifyContent: 'flex-end', gap: spec.endGap[form]}]}>{trailing}</View>
          </View>
          {tv && scrubbing && hints !== undefined && <View pointerEvents="none" style={[StyleSheet.absoluteFill, {justifyContent: 'center', marginTop: spec.controlsGap[form]}]}>{hints}</View>}
        </View>
      </View>}
    </View>
    {/* Over the hidden bar, so the band's tiles are what the remote reaches. */}
    {banded && <View style={{position: 'absolute', left: 0, right: 0, bottom: Math.max(spec.bottom[form], insets.bottom)}}>{band}</View>}
  </View>;
  const panelWidth = spec.panel[panelWide ? 'wideWidth' : 'width'][form];
  const over = !!panel && !upright && !beside && <View style={{position: 'absolute', top: 0, right: 0, bottom: 0, width: panelWidth}}>{panel}</View>;
  // The frame draws no backdrop: the player's layer is black (R10). Beside a panel the bar's spare parts make way (SC-10).
  return <View testID={testID} style={{flex: 1, flexDirection: beside ? 'row' : 'column', paddingTop: upright ? insets.top : 0}}>
    {upright ? <PlayerCompact.Provider value>{stage}</PlayerCompact.Provider> : <PlayerBeside.Provider value={beside}>{stage}</PlayerBeside.Provider>}
    {upright && <View style={{flex: 1}}>{body}{!!panel && <View style={StyleSheet.absoluteFill}>{panel}</View>}</View>}
    {beside && <View style={{width: panelWidth}}>{panel}</View>}
    {over}
  </View>;
}
