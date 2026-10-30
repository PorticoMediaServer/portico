import React, {useId, useRef} from 'react';
import {Pressable, ScrollView, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {SignalTranslate} from '../../../../../../packages/client-core/src/music-signal';
import type {MusicSignalInput} from '../../../../../../packages/client-core/src/music-signal';
import {Icon} from '../../../kit/Icon';
import {Button, Focusable} from '../../../kit/controls';
import {SidePanel} from '../../../kit/overlays';
import type {FocusRef} from '../../../kit/overlays/types';
import {useTheme} from '../../../kit/theme';
import {nativeType} from '../../../kit/theme';
import {nativeSignalPath, type NativeSignalPath} from './signal-path-model';

const t = defaultI18n.t;
const translate: SignalTranslate = (key: MessageId, values) => t(key, values as never);

export type MusicSignalView = Readonly<{input: MusicSignalInput; path: NativeSignalPath}>;

/** music-plus (MC-8) 3/5: the one line under the title. TV takes the caption size and a
 * chevron; a phone rests on a faint chip because touch has no hover. */
export function MusicSignalLine({view, onPress, focusRef}: {view: MusicSignalView; onPress?: () => void; focusRef?: React.Ref<View>}) {
  const theme = useTheme(), tv = theme.isTV, path = view.path;
  const colour = path.healthy ? theme.colors.healthy : theme.colors.textTertiary;
  const body = (focused = false) => <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 10 : 7, minWidth: 0}}>
    <Icon name={tv ? 'i-wave' : 'i-pulse'} size={tv ? 26 : 15} color={colour}/>
    {path.source ? <Text numberOfLines={1} ellipsizeMode="clip" style={[nativeType({size: tv ? 22 : 12, weight: tv ? 800 : 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: tv ? theme.colors.textSecondary : theme.colors.text, flexShrink: 0}]}>{path.source}</Text> : null}
    {tv && path.source && path.state ? <View style={{width: 2}}/> : null}
    {path.state ? <Text numberOfLines={1} ellipsizeMode="tail" style={[nativeType({size: tv ? 22 : 12, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: tv ? focused ? theme.colors.text : theme.colors.textTertiary : theme.colors.textSecondary, flexShrink: 1}]}>{path.state}</Text> : null}
    {tv && <Icon name="i-right" size={20} color={theme.colors.textTertiary}/>}
  </View>;
  const label = path.accessibleName ?? [path.source, path.state].filter(Boolean).join('. ');
  if (!onPress) return <View accessible accessibilityLabel={label} style={{maxWidth: tv ? 556 : 342}}>{body()}</View>;
  if (tv) return <Focusable ref={focusRef} focusId="music-signal-line" testID="music-signal-line" accessibilityRole="button" accessibilityLabel={label} focusRadius={12}
    onPress={onPress} style={({focused}) => ({maxWidth: 556, height: 52, paddingHorizontal: 14, borderRadius: 12, justifyContent: 'center', backgroundColor: focused ? 'rgba(237,245,250,.16)' : 'transparent'})}>{({focused}) => body(focused)}</Focusable>;
  return <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} testID="music-signal-line"
    style={{maxWidth: 342, paddingHorizontal: 10, paddingVertical: 5, borderRadius: 14, backgroundColor: 'rgba(237,245,250,.07)'}}>{body()}</Pressable>;
}

function Row({label, value, note, changed}: {label: string; value?: string; note?: string; changed: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.isTV ? 18 : 10, paddingVertical: theme.isTV ? 10 : 7}}>
    <View style={{width: theme.isTV ? 12 : 9, height: theme.isTV ? 12 : 9, borderRadius: 6, marginTop: theme.isTV ? 9 : 5,
      backgroundColor: changed ? theme.colors.text : theme.colors.healthy}}/>
    <Text style={[nativeType({size: theme.isTV ? 22 : 13.5, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {width: theme.isTV ? 250 : 108, color: theme.colors.textSecondary}]}>{label}</Text>
    <View style={{flex: 1, minWidth: 0}}>
      {value ? <Text style={[nativeType({size: theme.isTV ? 22 : 13.5, weight: changed ? 800 : 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{value}</Text> : null}
      {note ? <Text style={[nativeType({size: theme.isTV ? 18 : 12, weight: 500, tracking: 0, lineHeight: 'normal'}, theme.textScale), {marginTop: 2, color: theme.colors.textTertiary}]}>{note}</Text> : null}
    </View>
  </View>;
}

/** music-plus 3b/6: the same rows as the desktop popover, as a phone sheet or a TV side sheet.
 * The pill names the output only; the steps say what each part of the path did. */
export function MusicSignalSheet({view, onClose, contextTitle, returnFocusRef}: {view: MusicSignalView; onClose: () => void; contextTitle?: string; returnFocusRef?: FocusRef}) {
  const theme = useTheme(), tv = theme.isTV, path = view.path;
  if (tv) return <TVMusicSignalSheet view={view} onClose={onClose} contextTitle={contextTitle} returnFocusRef={returnFocusRef}/>;
  return <View style={{flex: 1, backgroundColor: tv ? 'rgba(7,16,24,.96)' : theme.colors.ink, paddingTop: tv ? 64 : 54, paddingHorizontal: tv ? 56 : 20}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
      <Text style={[nativeType({size: tv ? 30 : 20, weight: 800, tracking: -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('player.signal.title')}</Text>
      <View style={{paddingHorizontal: 12, paddingVertical: 5, borderRadius: 12, backgroundColor: path.healthy ? 'rgba(134,207,179,.16)' : 'rgba(237,245,250,.1)'}}>
        <Text style={[nativeType({size: tv ? 19 : 12.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: path.healthy ? theme.colors.healthy : theme.colors.textSecondary}]}>{view.path.pill}</Text>
      </View>
      <View style={{flex: 1}}/>
      <Focusable focusId="music-signal-done" testID="music-signal-done" accessibilityRole="button" accessibilityLabel={t('action.done')}
        hasTVPreferredFocus={tv} onPress={onClose} style={{paddingHorizontal: tv ? 26 : 14, paddingVertical: tv ? 12 : 8, borderRadius: 10, backgroundColor: theme.colors.text}}>
        <Text style={[nativeType({size: tv ? 21 : 13.5, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.ink}]}>{t('action.done')}</Text>
      </Focusable>
    </View>
    <ScrollView style={{marginTop: tv ? 26 : 16}} showsVerticalScrollIndicator={false}>
      {path.steps.map(step => <Row key={step.id} label={step.label} value={step.value} note={step.note} changed={step.changed}/>)}
    </ScrollView>
  </View>;
}

/** listening-notices8: only Done takes focus; the existing overlay owns Back,
 * the held boundary and restoration to the real signal line. */
function TVMusicSignalSheet({view, onClose, contextTitle, returnFocusRef}: {view: MusicSignalView; onClose: () => void; contextTitle?: string; returnFocusRef?: FocusRef}) {
  const theme = useTheme(), done = useRef<View>(null), gradient = useId(), path = view.path;
  return <>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0}}><Svg width="100%" height="100%"><Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0">
      <Stop offset="0" stopColor="#071018" stopOpacity={.3}/><Stop offset="0.55" stopColor="#071018" stopOpacity={.62}/><Stop offset="1" stopColor="#071018" stopOpacity={.75}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${gradient})`}/></Svg></View>
    <SidePanel open onOpenChange={open => {if (!open) onClose();}} title={t('player.signal.title')} logicalCanvas={{width: 640, height: 1080}} topInset={0} showClose={false}
      initialFocusRef={done} returnFocusRef={returnFocusRef} testID="music-signal-sheet" accessibilityLabel={t('player.signal.title')}
      headerContext={contextTitle ? <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16, marginBottom: -4}}>
        <Text style={[nativeType({size: 22, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{contextTitle}</Text>
        <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('player.queueNowPlaying')}</Text>
      </View> : undefined}
      style={{boxShadow: [{offsetX: -40, offsetY: 0, blurRadius: 80, spreadDistance: -20, color: 'rgba(0,0,0,.75)'}]}}
      footer={<View style={{gap: 22}}><Button ref={done} focusId="done" testID="music-signal-done" hasTVPreferredFocus onPress={onClose} style={{alignSelf: 'flex-start'}}>{t('action.done')}</Button>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><View style={{paddingHorizontal: 10, height: 32, borderRadius: 7, borderWidth: 1.5, borderColor: 'rgba(237,245,250,.3)', justifyContent: 'center'}}>
          <Text style={[nativeType({size: 16, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('action.back')}</Text>
        </View><Text style={[nativeType({size: 18, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.states.close')}</Text></View>
      </View>}>
      <View style={{height: 38, paddingHorizontal: 16, borderRadius: 10, gap: 10, flexDirection: 'row', alignItems: 'center', alignSelf: 'flex-start', marginTop: 6,
        backgroundColor: path.healthy ? 'rgba(134,207,179,.14)' : 'rgba(237,245,250,.1)'}}>
        <View style={{width: 10, height: 10, borderRadius: 5, backgroundColor: path.healthy ? theme.colors.healthy : theme.colors.textTertiary}}/>
        <Text style={[nativeType({size: 20, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: path.healthy ? theme.colors.healthy : theme.colors.textSecondary}]}>{path.pill}</Text>
      </View>
      <ScrollView style={{marginTop: 30}} showsVerticalScrollIndicator={false}>{path.steps.map((step, index) => <View key={step.id} style={{flexDirection: 'row', alignItems: 'flex-start', paddingVertical: 12}}>
        <View style={{width: 30, alignSelf: 'stretch'}}>
          {index < path.steps.length - 1 && <View style={{position: 'absolute', left: 11, top: 16, bottom: -34, width: 2, backgroundColor: 'rgba(237,245,250,.14)'}}/>}
          <View style={{position: 'absolute', top: 6, left: 2, width: 20, height: 20, borderRadius: 10, padding: 4, backgroundColor: step.changed ? 'rgba(237,245,250,.14)' : 'rgba(134,207,179,.16)'}}>
            <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: step.changed ? theme.colors.text : theme.colors.healthy}}/>
          </View>
        </View>
        <Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {width: 186, paddingTop: 1, color: theme.colors.textTertiary}]}>{step.label}</Text>
        <View style={{flex: 1, minWidth: 0}}>{step.value && <Text style={[nativeType({size: 22, weight: step.changed ? 700 : 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: step.changed ? theme.colors.text : theme.colors.textSecondary}]}>{step.value}</Text>}
          {step.note && <Text style={[nativeType({size: 20, weight: 500, tracking: 0, lineHeight: 29}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 4}]}>{step.note}</Text>}
        </View>
      </View>)}</ScrollView>
    </SidePanel>
  </>;
}

/** The projection the host renders: one call, no store. */
export function musicSignalView(input: MusicSignalInput): MusicSignalView {
  return Object.freeze({input, path: nativeSignalPath(input, translate)});
}
