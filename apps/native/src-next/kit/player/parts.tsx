import React, {createContext, useContext, useState, type ReactNode} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {useFrameMetrics} from '../frames/metrics';

/** The players' own parts: what a player needs beyond the stock buttons, rows and cards (phone-player, tablet-play
 * and tv-player mock-ups). The frames (`kit/frames` `PlayerChromeFrame`, `AudioPlayerFrame`) place them; a player
 * screen supplies their values and handlers. The web has the same parts with the same names in `web/src/kit/player`. */

/** The title block at the top of the video player: the title, then the episode or the year under it. Live TV adds
 * the channel's mark before it, the red Live mark after the channel, and a third line (the airing's times and what
 * follows it). */
export function PlayerTitle({title, lead, subtitle, mark, live, detail}: {title: string; /** Said first and brighter on the line under the title ("S1 E2"). */ lead?: string; subtitle?: string; /** `ChannelMark`. */ mark?: ReactNode; live?: string; detail?: string}) {
  const theme = useTheme(), second = [theme.type.playerSubtitle, {color: theme.colors.textSecondary}];
  return <View style={{flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
    {mark}
    <View style={{flex: 1, minWidth: 0}}>
      <Text accessibilityRole="header" numberOfLines={1} style={[theme.type.playerTitle, {color: theme.colors.text}]}>{title}</Text>
      {(lead !== undefined || subtitle !== undefined || live !== undefined) && <Text numberOfLines={1} style={second}>
        {lead !== undefined && <Text style={{color: theme.colors.text, fontFamily: theme.type.button.fontFamily}}>{lead}  </Text>}{subtitle}
        {live !== undefined && <Text style={{color: theme.colors.record, fontFamily: theme.type.button.fontFamily}}>{subtitle !== undefined ? '  ' : ''}{'\u25CF'} {live}</Text>}</Text>}
      {detail !== undefined && <Text numberOfLines={1} style={second}>{detail}</Text>}
    </View>
  </View>;
}

/** Set by `PlayerChromeFrame` around the upright form's strip, where the controls are the small ones (phone-player 4, tablet-play 3). */
export const PlayerCompact = createContext(false);

export type PlayerControlProps = Readonly<{
  icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean;
  /** Play or Pause: the large white one. */
  primary?: boolean;
  /** A top-bar button over the picture: on a glass disc on a tablet, a plain white glyph on a phone and in the upright strip. */
  onPicture?: boolean;
  /** TV draws its controls on discs; `plain` is for the ones it draws as bare glyphs (shuffle and repeat). */
  plain?: boolean;
  /** Back or forward: the seconds it skips, drawn inside the arrow. */
  skip?: number;
  /** Buffering: the spinner takes the icon's place and the button keeps its size. */
  busy?: boolean;
  testID?: string;
}>;
/** One transport or tool button: the kit Button at its round `transport` size. Play or Pause is the white disc. The
 * others are plain white glyphs on a phone and a tablet, and sit on a disc only where the mock-ups draw one: a TV's
 * controls and a tablet's top bar. A control the title doesn't support stays in its place with `disabled`. */
export function PlayerControl({icon, label, onPress, disabled, active, primary = false, onPicture = false, plain = false, skip, busy = false, testID}: PlayerControlProps) {
  const theme = useTheme(), {form} = useFrameMetrics(), compact = useContext(PlayerCompact), small = frameSpec.player.portrait;
  const disc = form === 'tv' ? !plain : onPicture && form !== 'phone' && !compact;
  // The small controls are the phone's; an upright tablet's strip is large enough for the tablet's own (tablet-play 3).
  const little = compact && form === 'phone', size = little ? primary ? small.primary : small.control : undefined;
  const button = <Button testID={testID} variant={primary ? 'primary' : disc ? 'glass' : 'text'} size="transport" iconOnly icon={icon} loading={busy}
    iconSize={little ? primary ? small.primaryIcon : small.controlIcon : undefined} iconColor={primary ? undefined : active ? theme.colors.accentStrong : theme.colors.text}
    style={size === undefined ? undefined : {height: size, borderRadius: size / 2}} align="center"
    accessibilityLabel={label} accessibilityState={{selected: active}} disabled={disabled} onPress={onPress} preferredFocus={primary}/>;
  if (skip === undefined) return button;
  return <View>{button}<View pointerEvents="none" style={[StyleSheet.absoluteFill, {alignItems: 'center', justifyContent: 'center', opacity: disabled ? theme.spec.button.disabledOpacity : 1}]}>
    <Text style={[theme.type.playerSkip, {color: theme.colors.text, includeFontPadding: false}]}>{skip}</Text></View></View>;
}

/** The scrubber as it is drawn: the track, what has buffered, what has played, the knob, and the marked stretches
 * (intro, credits). A player screen's own timeline owns the gestures and keys and draws through this. */
export function PlayerScrubBar({value, buffered = [], marks = [], preview, filmstrip, labels, tone = 'accent', knob: withKnob = true, liveEdge, from = 0}: {value: number; /** TV, while scrubbing: `PlayerFilmstrip`, which the bar centres over itself, and the chapters' names, which it sets under itself where each starts (`end` for one that runs to the end). */ filmstrip?: ReactNode; labels?: readonly Readonly<{at: number; text: string; end?: boolean}>[]; /** Live TV: where live is, 0 to 1. A red tick marks it and the bar after it, not yet aired, is dotted. */ liveEdge?: number; /** Live TV: where the kept window starts; what has played is drawn from there. */ from?: number; buffered?: readonly Readonly<{start: number; end: number}>[]; marks?: readonly Readonly<{start: number; end: number}>[]; /** `PlayerScrubPreview`, while scrubbing. */ preview?: ReactNode;
  /** What has played: the accent on video, white on the audio player. */ tone?: 'accent' | 'white'; /** TV's audio player draws a bar with no knob. */ knob?: boolean}) {
  const theme = useTheme(), {form} = useFrameMetrics(), compact = useContext(PlayerCompact), knob = frameSpec.player.knob[form], track = compact ? frameSpec.player.portrait.scrubber : theme.spec.card.progress.height[theme.platform];
  const part = (start: number, end: number): {left: `${number}%`; width: `${number}%`} => ({left: `${Math.max(0, start) * 100}%`, width: `${Math.max(0, Math.min(1, end) - Math.max(0, start)) * 100}%`});
  // The bar's box is the knob's height in every state, so nothing moves when the knob is hidden; in the upright strip the box is the track itself and the knob rides over the picture's edge.
  const [width, setWidth] = useState(0), [strip, setStrip] = useState(0);
  return <View onLayout={event => setWidth(event.nativeEvent.layout.width)} style={{height: compact ? track : knob, justifyContent: 'center'}}>
    <View style={{height: track, borderRadius: track / 2, backgroundColor: alpha(theme.colors.text, tint.track)}}>
      {buffered.map((stretch, index) => <View key={index} style={[styles.part, part(stretch.start, stretch.end), {borderRadius: track / 2, backgroundColor: alpha(theme.colors.text, tint.buffered)}]}/>)}
      <View style={[styles.part, part(from, value), {borderRadius: track / 2, backgroundColor: tone === 'white' ? theme.colors.text : theme.colors.accentStrong}]}/>
      {marks.map((mark, index) => <View key={index} style={[styles.part, part(mark.start, mark.end), {backgroundColor: alpha(theme.colors.white, tint.mark)}]}/>)}
    </View>
    {liveEdge !== undefined && <>
      {/* Not yet aired: the track broken into dots (a dotted border is not drawn the same on every platform, so the gaps are drawn). */}
      <View pointerEvents="none" style={[styles.part, part(liveEdge, 1), {top: undefined, bottom: undefined, height: track, flexDirection: 'row', overflow: 'hidden', backgroundColor: theme.colors.shadow}]}>
        {Array.from({length: 80}, (_, index) => <View key={index} style={{width: track, height: track, marginRight: track, borderRadius: track / 2, backgroundColor: alpha(theme.colors.text, tint.buffered)}}/>)}</View>
      <View pointerEvents="none" style={{position: 'absolute', left: `${liveEdge * 100}%`, width: theme.space.hairline * 2, top: 0, bottom: 0, backgroundColor: theme.colors.record}}/>
    </>}
    {withKnob && <View pointerEvents="none" style={{position: 'absolute', top: (compact ? track : knob) / 2 - knob / 2, left: `${Math.max(0, Math.min(1, value)) * 100}%`, marginLeft: -knob / 2, width: knob, height: knob, borderRadius: knob / 2, backgroundColor: theme.colors.white}}/>}
    {preview}
    {/* The landing picture sits over the knob (tv-player 2); the strip's ends run off the screen's edge when the knob is near one. */}
    {filmstrip !== undefined && <View pointerEvents="none" onLayout={event => setStrip(event.nativeEvent.layout.width)} style={{position: 'absolute', bottom: knob + film.lift, left: value * width - strip / 2, opacity: strip > 0 && width > 0 ? 1 : 0}}>{filmstrip}</View>}
    {labels?.map(label => <Text key={label.text} numberOfLines={1} style={[theme.type.playerTime, {position: 'absolute', top: knob + film.labelGap, color: theme.colors.textSecondary}, label.end ? {right: 0} : {left: `${label.at * 100}%`}]}>{label.text}</Text>)}
  </View>;
}

/** TV scrubbing (tv-player 2): the pictures either side of where the viewer would land, the landing one large in the
 * middle, each with its time. A picture that is not there leaves its tile dark. */
export function PlayerFilmstrip({frames}: {frames: readonly Readonly<{time: string; picture?: ReactNode}>[]}) {
  const theme = useTheme(), middle = (frames.length - 1) / 2;
  return <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: film.gap}}>{frames.map((frame, index) => {
    const large = index === middle, box = large ? film.large : film.small;
    return <View key={index} style={{...box, borderRadius: film.radius, overflow: 'hidden', backgroundColor: theme.colors.raisedSoft, opacity: large ? 1 : film.dim, borderWidth: theme.space.hairline * 1.5, borderColor: alpha(theme.colors.text, large ? tint.buffered : tint.track)}}>
      {frame.picture}
      <View style={{position: 'absolute', bottom: large ? theme.space.sm : theme.space.controlGap, alignSelf: 'center', paddingHorizontal: theme.space.controlGap, paddingVertical: theme.space.hairline * 3, borderRadius: theme.baseRadii.control, backgroundColor: alpha(theme.colors.ink, tint.previewRing)}}>
        <Text style={[large ? theme.type.playerPrompt : theme.type.playerTime, {color: theme.colors.text, fontFamily: theme.type.pageTitle.fontFamily, fontVariant: ['tabular-nums']}]}>{frame.time}</Text>
      </View>
    </View>;
  })}</View>;
}

/** The frame at the finger or the focused time while scrubbing: the picture there and its time under it. */
export function PlayerScrubPreview({at, time, children}: {/** 0 to 1 along the scrubber. */ at: number; time: string; children: ReactNode}) {
  const theme = useTheme(), {form} = useFrameMetrics(), spec = frameSpec.player.preview, width = spec.width[form];
  return <View pointerEvents="none" style={{position: 'absolute', bottom: spec.lift[form], left: `${at * 100}%`, marginLeft: -width / 2, width, height: spec.height[form], borderRadius: spec.radius[form], overflow: 'hidden', backgroundColor: theme.colors.shadow, borderWidth: theme.spec.focus.ringWidth[theme.platform], borderColor: alpha(theme.colors.text, tint.previewRing)}}>
    {children}
    <Text style={[theme.type.playerPreviewTime, styles.previewTime, {color: theme.colors.text, backgroundColor: alpha(theme.colors.dim, tint.previewBand), paddingVertical: theme.space.tight}]}>{time}</Text>
  </View>;
}

/** The time read-outs under a scrubber: where it is, how long is left, and between them an audiobook's time left in the book. (The video frame places its own.) */
export function PlayerTimes({position, remaining, middle}: {position: string; remaining?: string; middle?: string}) {
  const theme = useTheme(), style = [theme.type.playerTime, {color: theme.colors.text, fontFamily: theme.type.button.fontFamily, fontVariant: ['tabular-nums' as const]}];
  return <View style={{flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', gap: theme.space.xs}}><Text style={style}>{position}</Text>
    {middle !== undefined && <Text numberOfLines={1} style={[theme.type.playerTime, {flexShrink: 1, color: theme.colors.textSecondary}]}>{middle}</Text>}<Text style={style}>{remaining}</Text></View>;
}

/** An options entry point that says what it is and what it is set to: "Subtitles English", "Audio English", and
 * quality as "Auto 1080p". With `name` the name is the strong word and the value follows dimmer; without it the value
 * is the strong word and `qualifier` goes before it. One an item doesn't have keeps its place with `disabled`. */
export function PlayerPill({name, value, qualifier, icon, tone, label, onPress, disabled, testID}: {name?: string; value: string; /** Before the value, dimmer ("Auto"). */ qualifier?: string; icon?: IconName; /** `record` colours the icon red (Go to live). */ tone?: 'record'; /** For assistive technology. */ label: string; onPress: () => void; disabled?: boolean; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), spec = frameSpec.player.pill, height = spec.height[form];
  const strong = [theme.type.playerPill, {color: theme.colors.text}], dim = [theme.type.playerPill, {color: theme.colors.textTertiary}];
  // TV (tv-player 1): a named entry is a two-line tile, its name over its value.
  if (form === 'tv' && name !== undefined) return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} focusRadius={frameSpec.player.tile.radius}
    style={{height: theme.spec.transport.control.size[theme.platform], paddingHorizontal: frameSpec.player.tile.paddingX, borderRadius: frameSpec.player.tile.radius, justifyContent: 'center', backgroundColor: alpha(theme.colors.text, tint.pill), opacity: disabled ? theme.spec.button.disabledOpacity : 1}}>
    <Text numberOfLines={1} style={[theme.type.playerTime, {color: theme.colors.text, fontFamily: theme.type.button.fontFamily}]}>{name}</Text>
    <Text numberOfLines={1} style={[theme.type.playerTime, {color: theme.colors.textSecondary}]}>{qualifier !== undefined ? `${qualifier} ` : ''}{value}</Text>
  </Focusable>;
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} focusRadius={height / 2}
    style={{height, paddingHorizontal: spec.paddingX[form], borderRadius: height / 2, flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, backgroundColor: alpha(theme.colors.text, tint.pill), opacity: disabled ? theme.spec.button.disabledOpacity : 1}}>
    {icon && <Icon name={icon} size={frameSpec.player.pillIcon} color={tone === 'record' ? theme.colors.record : theme.colors.textSecondary}/>}
    {name !== undefined ? <><Text numberOfLines={1} style={strong}>{name}</Text><Text numberOfLines={1} style={dim}>{value}</Text></> : <>{qualifier !== undefined && <Text numberOfLines={1} style={dim}>{qualifier}</Text>}<Text numberOfLines={1} style={strong}>{value}</Text></>}
  </Focusable>;
}

/** Skip intro, Up next: the white prompt in the player's prompt corner, with its dismiss beside it on a tablet. */
export function PlayerPrompt({label, icon = 'i-next', onPress, dismiss, testID}: {label: string; icon?: IconName; onPress: () => void; dismiss?: Readonly<{label: string; onPress: () => void}>; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), compact = useContext(PlayerCompact), spec = frameSpec.player.prompt, small = frameSpec.player.portrait, height = compact ? small.promptHeight : spec.height[form], round = compact ? 0 : spec.dismiss[form];
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: spec.gap}}>
    {dismiss && round > 0 && <Button variant="icon" icon="i-x" accessibilityLabel={dismiss.label} onPress={dismiss.onPress} style={{height: round}}/>}
    <Button testID={testID} variant="primary" trailingIcon={icon} iconSize={compact ? frameSpec.player.pillIcon : undefined} onPress={onPress} textStyle={compact ? theme.type.playerPill : theme.type.playerPrompt}
      style={{height, paddingHorizontal: compact ? small.promptPaddingX : spec.paddingX[form], borderRadius: compact ? height / 2 : spec.radius[form]}}>{label}</Button>
  </View>;
}

const tint = frameSpec.player.tint, film = frameSpec.player.filmstrip;
const styles = StyleSheet.create({
  part: {position: 'absolute', top: 0, bottom: 0},
  previewTime: {position: 'absolute', left: 0, right: 0, bottom: 0, textAlign: 'center'},
});
