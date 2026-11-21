import React, {type ComponentType} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {Button, RichSelect} from '../controls';
import {useTheme} from '../theme';

type Choice = Readonly<{label: string; value: string; disabled: boolean; onValueChange: (value: string) => void;
  options: readonly Readonly<{value: string; label: string; description?: string; disabled?: boolean}>[]}>;

/** The player a phone keeps for its downloads while it can't reach the server (states 4a, "Your downloads still play"):
 * once something is playing, the picture at 16:9, Play or Pause beside where it is, then the audio and subtitle choices the download carries and the
 * subtitle timing. Everything it runs comes from the page. */
export function OfflinePlayerPanel({Surface, toggle, audio, subtitles, subtitleError, timing, error}: {Surface: ComponentType;
  toggle?: Readonly<{label: string; disabled: boolean; onPress: () => void; position: string}>; audio?: Choice; subtitles?: Choice;
  subtitleError?: string; timing?: Readonly<{label: string; value: string; steps: readonly Readonly<{id: string; label: string; accessibilityLabel: string; disabled: boolean; onPress: () => void}>[]}>;
  error?: string}) {
  const theme = useTheme(), {width} = useWindowDimensions();
  const note = (text: string) => <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{text}</Text>;
  return <View testID="remembered-player" style={{gap: toggle ? theme.space.sm : 0}}>
    {/* The surface stays mounted (opening needs it) but takes no room until something is playing. */}
    <View style={{height: toggle ? Math.round((width - 32) * 9 / 16) : 0, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}><Surface/></View>
    {toggle && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
      <Button variant="glass" disabled={toggle.disabled} onPress={toggle.onPress}>{toggle.label}</Button>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{toggle.position}</Text>
    </View>}
    {audio && <RichSelect testID="remembered-player-audio" label={audio.label} value={audio.value} disabled={audio.disabled} options={[...audio.options]} onValueChange={audio.onValueChange}/>}
    {subtitles && <RichSelect testID="remembered-player-subtitles" label={subtitles.label} value={subtitles.value} disabled={subtitles.disabled} options={[...subtitles.options]} onValueChange={subtitles.onValueChange}/>}
    {subtitleError && note(subtitleError)}
    {timing && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{timing.label}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{timing.value}</Text>
      {timing.steps.map(step => <Button key={step.id} variant="glass" disabled={step.disabled} accessibilityLabel={step.accessibilityLabel} onPress={step.onPress}>{step.label}</Button>)}
    </View>}
    {error && note(error)}
  </View>;
}
