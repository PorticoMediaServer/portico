import React, {useId, useRef} from 'react';
import {Pressable, ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Icon} from '../Icon';
import {Button, Focusable} from '../controls';
import {BottomSheet, SidePanel} from '../overlays';
import type {FocusRef} from '../overlays/types';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

const t = defaultI18n.t;

/** One step of the signal path (music-plus 3b/6): what the part did, its value, and a note; `changed` is a step that changed
 * the sound (drawn bright), the rest are untouched (green). */
export type SignalStep = Readonly<{id: string; label: string; value?: string; note?: string; changed: boolean}>;
/** The path as the line and the sheet draw it: the output's pill, the source badge and state for the line, the steps. */
export type SignalPathView = Readonly<{healthy: boolean; pill: string; source?: string; state?: string; accessibleName?: string;
  steps: readonly SignalStep[]}>;

/** music-plus (MC-8) 3/5: the one line under the title. TV takes the caption size and a chevron; a phone rests on a faint chip
 * because touch has no hover. */
export function SignalLine({path, onPress, focusRef}: {path: SignalPathView; onPress?: () => void; focusRef?: React.Ref<View>}) {
  const theme = useTheme(), tv = theme.isTV;
  const colour = path.healthy ? theme.colors.healthy : theme.colors.textTertiary;
  const body = (focused = false) => <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 10 : 7, minWidth: 0}}>
    <Icon name={tv ? 'i-wave' : 'i-pulse'} size={tv ? 26 : 15} color={colour}/>
    {path.source ? <Text ellipsizeMode="clip" style={[theme.type.captionStrong, {color: tv ? theme.colors.textSecondary : theme.colors.text, flexShrink: 0}]}>{path.source}</Text> : null}
    {path.state ? <Text style={[theme.type.caption, {color: tv ? focused ? theme.colors.text : theme.colors.textTertiary : theme.colors.textSecondary, flexShrink: 1}]}>{path.state}</Text> : null}
    {tv && <Icon name="i-right" size={20} color={theme.colors.textTertiary}/>}
  </View>;
  const label = path.accessibleName ?? [path.source, path.state].filter(Boolean).join('. ');
  if (!onPress) return <View accessible accessibilityLabel={label} style={{maxWidth: tv ? 556 : 342}}>{body()}</View>;
  if (tv) return <Focusable ref={focusRef} focusId="music-signal-line" testID="music-signal-line" accessibilityRole="button" accessibilityLabel={label} focusRadius={12}
    onPress={onPress} style={({focused}) => ({maxWidth: 556, height: 52, paddingHorizontal: 14, borderRadius: 12, justifyContent: 'center',
      backgroundColor: focused ? alpha(theme.colors.text, .16) : 'transparent'})}>{({focused}) => body(focused)}</Focusable>;
  return <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} testID="music-signal-line"
    style={{maxWidth: 342, paddingHorizontal: 10, paddingVertical: 5, borderRadius: 14, backgroundColor: alpha(theme.colors.text, .07)}}>{body()}</Pressable>;
}

/** The output's pill: green while the path is untouched. */
function SignalPill({path}: {path: SignalPathView}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, alignSelf: 'flex-start', paddingHorizontal: theme.space.controlGap + 2,
    paddingVertical: theme.isTV ? theme.space.xs : 5, borderRadius: theme.isTV ? 10 : 12,
    backgroundColor: path.healthy ? alpha(theme.colors.healthy, .16) : alpha(theme.colors.text, .1)}}>
    {theme.isTV && <View style={{width: 10, height: 10, borderRadius: 5, backgroundColor: path.healthy ? theme.colors.healthy : theme.colors.textTertiary}}/>}
    <Text style={[theme.type.captionStrong, {color: path.healthy ? theme.colors.healthy : theme.colors.textSecondary}]}>{path.pill}</Text>
  </View>;
}

function Step({step, last}: {step: SignalStep; last: boolean}) {
  const theme = useTheme(), tv = theme.isTV;
  return <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: tv ? 0 : 10, paddingVertical: tv ? 12 : 7}}>
    {tv ? <View style={{width: 30, alignSelf: 'stretch'}}>
      {!last && <View style={{position: 'absolute', left: 11, top: 16, bottom: -34, width: 2, backgroundColor: alpha(theme.colors.text, .14)}}/>}
      <View style={{position: 'absolute', top: 6, left: 2, width: 20, height: 20, borderRadius: 10, padding: 4,
        backgroundColor: step.changed ? alpha(theme.colors.text, .14) : alpha(theme.colors.healthy, .16)}}>
        <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: step.changed ? theme.colors.text : theme.colors.healthy}}/>
      </View>
    </View> : <View style={{width: 9, height: 9, borderRadius: 5, marginTop: 5, backgroundColor: step.changed ? theme.colors.text : theme.colors.healthy}}/>}
    <Text style={[theme.type.caption, {width: tv ? 186 : 108, color: tv ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{step.label}</Text>
    <View style={{flex: 1, minWidth: 0}}>
      {step.value ? <Text style={[step.changed ? theme.type.captionStrong : theme.type.caption, {color: step.changed || !tv ? theme.colors.text : theme.colors.textSecondary}]}>{step.value}</Text> : null}
      {step.note ? <Text style={[theme.type.rowHelp, {marginTop: 2, color: theme.colors.textTertiary}]}>{step.note}</Text> : null}
    </View>
  </View>;
}

/** music-plus 3b: the signal path on a phone, a bottom sheet over the player with its grabber: "Signal path", the output's pill
 * and Done, then the steps. Swipe down, tap outside or Done closes it. */
export function SignalSheet({path, open, onClose}: {path: SignalPathView; open: boolean; onClose: () => void}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <BottomSheet open={open} onOpenChange={next => {if (!next) onClose();}} title={t('player.signal.title')} accessibilityLabel={t('player.signal.title')}
    testID="music-signal-sheet" fitContent scrollable={false}
    headerContent={<View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 2, paddingHorizontal: theme.space.md - 4, paddingBottom: theme.space.sm}}>
      <Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{t('player.signal.title')}</Text>
      <SignalPill path={path}/>
      <View style={{flex: 1}}/>
      <Button size="small" testID="music-signal-done" onPress={onClose}>{t('action.done')}</Button>
    </View>}>
    <View style={{paddingHorizontal: theme.space.md - 4, paddingBottom: insets.bottom + theme.space.sm}}>
      {path.steps.map((step, index) => <Step key={step.id} step={step} last={index === path.steps.length - 1}/>)}
    </View>
  </BottomSheet>;
}

/** listening-notices8: the TV side sheet. Only Done takes focus; the overlay owns Back, the held boundary and the return to the
 * real signal line. */
export function TVSignalSheet({path, onClose, contextTitle, returnFocusRef}: {path: SignalPathView; onClose: () => void; contextTitle?: string; returnFocusRef?: FocusRef}) {
  const theme = useTheme(), done = useRef<View>(null), gradient = useId();
  return <>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0}}><Svg width="100%" height="100%"><Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0">
      <Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.3}/><Stop offset="0.55" stopColor={theme.colors.ink} stopOpacity={.62}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.75}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${gradient})`}/></Svg></View>
    <SidePanel open onOpenChange={open => {if (!open) onClose();}} title={t('player.signal.title')} logicalCanvas={{width: 640, height: 1080}} topInset={0} showClose={false}
      initialFocusRef={done} returnFocusRef={returnFocusRef} testID="music-signal-sheet" accessibilityLabel={t('player.signal.title')}
      headerContext={contextTitle ? <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16, marginBottom: -4}}>
        <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{contextTitle}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('player.queueNowPlaying')}</Text>
      </View> : undefined}
      style={{boxShadow: [{offsetX: -40, offsetY: 0, blurRadius: 80, spreadDistance: -20, color: alpha(theme.colors.shadow, .75)}]}}
      footer={<View style={{gap: 22}}><Button ref={done} focusId="done" testID="music-signal-done" preferredFocus onPress={onClose}>{t('action.done')}</Button>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><View style={{paddingHorizontal: 10, height: 32, borderRadius: 7, borderWidth: 1.5,
          borderColor: alpha(theme.colors.text, .3), justifyContent: 'center'}}>
          <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('action.back')}</Text>
        </View><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.states.close')}</Text></View>
      </View>}>
      <View style={{marginTop: 6}}><SignalPill path={path}/></View>
      <ScrollView style={{marginTop: 30}} showsVerticalScrollIndicator={false}>
        {path.steps.map((step, index) => <Step key={step.id} step={step} last={index === path.steps.length - 1}/>)}
      </ScrollView>
    </SidePanel>
  </>;
}
