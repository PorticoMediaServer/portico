import React, {type ReactNode} from 'react';
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

/** The title block at the top of the video player: the title, then the episode or the year under it. */
export function PlayerTitle({title, lead, subtitle}: {title: string; /** Said first and brighter on the line under the title ("S1 E2"). */ lead?: string; subtitle?: string}) {
  const theme = useTheme();
  return <View style={{flex: 1, minWidth: 0}}>
    <Text accessibilityRole="header" numberOfLines={1} style={[theme.type.playerTitle, {color: theme.colors.text}]}>{title}</Text>
    {(lead !== undefined || subtitle !== undefined) && <Text numberOfLines={1} style={[theme.type.playerSubtitle, {color: theme.colors.textSecondary}]}>
      {lead !== undefined && <Text style={{color: theme.colors.text, fontFamily: theme.type.button.fontFamily}}>{lead}  </Text>}{subtitle}</Text>}
  </View>;
}

export type PlayerControlProps = Readonly<{
  icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean;
  /** Play or Pause: the large white one. */
  primary?: boolean;
  /** Buffering: the spinner takes the icon's place and the button keeps its size. */
  busy?: boolean;
  testID?: string;
}>;
/** One round transport or tool button: the kit Button at its `transport` size. A control the title doesn't support
 * stays in its place with `disabled`. */
export function PlayerControl({icon, label, onPress, disabled, active, primary = false, busy = false, testID}: PlayerControlProps) {
  const theme = useTheme();
  return <Button testID={testID} variant={primary ? 'primary' : 'icon'} size="transport" iconOnly icon={icon} loading={busy} iconColor={!primary && active ? theme.colors.accentStrong : undefined}
    accessibilityLabel={label} accessibilityState={{selected: active}} disabled={disabled} onPress={onPress} preferredFocus={primary}/>;
}

/** The scrubber as it is drawn: the track, what has buffered, what has played, the knob, and the marked stretches
 * (intro, credits). A player screen's own timeline owns the gestures and keys and draws through this. */
export function PlayerScrubBar({value, buffered = [], marks = [], preview}: {value: number; buffered?: readonly Readonly<{start: number; end: number}>[]; marks?: readonly Readonly<{start: number; end: number}>[]; /** `PlayerScrubPreview`, while scrubbing. */ preview?: ReactNode}) {
  const theme = useTheme(), spec = theme.spec.transport, knob = spec.control.icon[theme.platform] / 2, track = theme.spec.card.progress.height[theme.platform];
  const part = (start: number, end: number): {left: `${number}%`; width: `${number}%`} => ({left: `${Math.max(0, start) * 100}%`, width: `${Math.max(0, Math.min(1, end) - Math.max(0, start)) * 100}%`});
  return <View style={{height: knob * 2, justifyContent: 'center'}}>
    <View style={{height: track, borderRadius: track / 2, backgroundColor: alpha(theme.colors.text, tint.track)}}>
      {buffered.map((stretch, index) => <View key={index} style={[styles.part, part(stretch.start, stretch.end), {borderRadius: track / 2, backgroundColor: alpha(theme.colors.text, tint.buffered)}]}/>)}
      <View style={[styles.part, part(0, value), {borderRadius: track / 2, backgroundColor: theme.colors.accentStrong}]}/>
      {marks.map((mark, index) => <View key={index} style={[styles.part, part(mark.start, mark.end), {backgroundColor: alpha(theme.colors.white, tint.mark)}]}/>)}
    </View>
    <View pointerEvents="none" style={{position: 'absolute', left: `${Math.max(0, Math.min(1, value)) * 100}%`, marginLeft: -knob, width: knob * 2, height: knob * 2, borderRadius: knob, backgroundColor: theme.colors.white}}/>
    {preview}
  </View>;
}

/** The frame at the finger or the focused time while scrubbing: the picture there and its time under it. */
export function PlayerScrubPreview({at, time, children}: {/** 0 to 1 along the scrubber. */ at: number; time: string; children: ReactNode}) {
  const theme = useTheme(), {form} = useFrameMetrics(), spec = frameSpec.player.preview, width = spec.width[form];
  return <View pointerEvents="none" style={{position: 'absolute', bottom: spec.lift[form], left: `${at * 100}%`, marginLeft: -width / 2, width, height: spec.height[form], borderRadius: spec.radius[form], overflow: 'hidden', backgroundColor: theme.colors.shadow, borderWidth: theme.spec.focus.ringWidth[theme.platform], borderColor: alpha(theme.colors.text, tint.previewRing)}}>
    {children}
    <Text style={[theme.type.playerPreviewTime, styles.previewTime, {color: theme.colors.text, backgroundColor: alpha(theme.colors.dim, tint.previewBand), paddingVertical: theme.space.tight}]}>{time}</Text>
  </View>;
}

/** The time read-outs under the scrubber: where it is, and on the right when it ends and how long is left. A live
 * programme says Live in place of the time left. */
export function PlayerTimes({position, remaining, endsAt, live}: {position: string; remaining?: string; endsAt?: string; live?: string}) {
  const theme = useTheme(), strong = {color: theme.colors.text, fontFamily: theme.type.button.fontFamily, fontVariant: ['tabular-nums' as const]};
  return <View style={{flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center'}}>
    <Text style={[theme.type.playerTime, strong]}>{position}</Text>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
      {endsAt !== undefined && <Text style={[theme.type.playerTime, {color: theme.colors.textTertiary}]}>{endsAt}</Text>}
      {live !== undefined ? <><View style={{width: theme.space.xs, height: theme.space.xs, borderRadius: theme.space.xs / 2, backgroundColor: theme.colors.record}}/><Text style={[theme.type.playerTime, strong]}>{live}</Text></>
        : <Text style={[theme.type.playerTime, strong]}>{remaining}</Text>}
    </View>
  </View>;
}

/** An options entry point that shows its current value: quality ("Auto 1080p"), speed ("1×"). */
export function PlayerPill({value, qualifier, icon, label, onPress, disabled, testID}: {value: string; /** Said before the value, dimmer ("Auto"). */ qualifier?: string; icon?: IconName; label: string; onPress: () => void; disabled?: boolean; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), spec = frameSpec.player.pill, height = spec.height[form];
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} focusRadius={height / 2}
    style={{height, paddingHorizontal: spec.paddingX[form], borderRadius: height / 2, flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, backgroundColor: alpha(theme.colors.text, tint.pill), opacity: disabled ? theme.spec.button.disabledOpacity : 1}}>
    {icon && <Icon name={icon} size={theme.space.sm} color={theme.colors.text}/>}
    {qualifier !== undefined && <Text style={[theme.type.playerPill, {color: theme.colors.textTertiary}]}>{qualifier}</Text>}
    <Text style={[theme.type.playerPill, {color: theme.colors.text}]}>{value}</Text>
  </Focusable>;
}

/** Skip intro, Up next: the white prompt in the player's prompt corner, with its dismiss beside it on a tablet. */
export function PlayerPrompt({label, icon = 'i-next', onPress, dismiss, testID}: {label: string; icon?: IconName; onPress: () => void; dismiss?: Readonly<{label: string; onPress: () => void}>; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), spec = frameSpec.player.prompt, height = spec.height[form], round = spec.dismiss[form];
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: spec.gap}}>
    {dismiss && round > 0 && <Button variant="glass" iconOnly icon="i-x" accessibilityLabel={dismiss.label} onPress={dismiss.onPress} style={{width: round, height: round}}/>}
    <Button testID={testID} variant="primary" trailingIcon={icon} onPress={onPress} textStyle={theme.type.playerPrompt} style={{height, paddingHorizontal: spec.paddingX[form], borderRadius: spec.radius[form]}}>{label}</Button>
  </View>;
}

const tint = frameSpec.player.tint;
const styles = StyleSheet.create({
  part: {position: 'absolute', top: 0, bottom: 0},
  previewTime: {position: 'absolute', left: 0, right: 0, bottom: 0, textAlign: 'center'},
});
