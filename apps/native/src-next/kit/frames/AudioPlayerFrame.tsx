import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import type {ArtworkSource} from '../artwork/types';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {PlayerControl} from '../player/parts';
import {ArtBox} from './parts';

/** One control in a fixed slot. A control the item doesn't support keeps its slot with `disabled`, dimmed. */
export type AudioControl = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean; testID?: string}>;
/** A bottom-row slot shows an icon or a short value ("1.25×") over its label. */
export type AudioOption = Omit<AudioControl, 'icon'> & Readonly<{icon?: IconName; value?: string}>;

export type AudioPlayerFrameProps = Readonly<{
  /** Close (the down arrow) and, opposite it, More. Between them, where this is playing from. */
  close: AudioControl; more: AudioControl; context?: string;
  /** The cover. With none, the placeholder with `icon` (music or book) fills the same box. */
  artwork?: ArtworkSource | null; icon: IconName;
  title: string; byline?: string;
  /** A third line under the byline (an audiobook's chapter). */
  detail?: string;
  /** The kit's scrubber for this player; the frame gives it a fixed box. */
  scrubber: ReactNode;
  /** Five fixed slots; the middle one is Play or Pause. */
  transport: readonly [AudioControl, AudioControl, AudioControl, AudioControl, AudioControl];
  /** "Next: ..." or the chapter position. Its line is kept, blank, when there is nothing to say. */
  nextLine?: string;
  /** Three or four fixed slots. */
  options: readonly AudioOption[];
  /** Wide forms and TV: the queue, the one flexible region there. It scrolls. */
  upNext?: ReactNode;
  loading?: boolean;
  testID?: string;
}>;

/** Frame 8, now playing for music and audiobooks (rule 12). Every region is placed from the frame's edges: on a
 * phone the bottom row, the transport, the scrubber and the title slot are stacked up from the bottom edge and the
 * artwork box hangs from the top, so the only thing that changes with a missing cover, a long title or a missing
 * control is the space between the artwork and the title. Wider forms place the columns from the top and let the
 * Up next list scroll. Loading fills the same boxes. */
export function AudioPlayerFrame({close, more, context, artwork, icon, title, byline, detail, scrubber, transport, nextLine, options, upNext, loading = false, testID}: AudioPlayerFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), spec = frameSpec.audio, {form} = frame;
  const wide = form === 'expanded' || form === 'tv', inset = spec.inset[form], column = wide ? spec.artwork[form] : Math.min(spec.artwork[form], frame.width - 2 * inset);
  const control = (item: AudioControl, primary: boolean) => <PlayerControl key={item.label} {...item} primary={primary} disabled={item.disabled || loading}/>;
  const top = <View style={{height: spec.topBar[form], flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    {form !== 'tv' && <Button variant="icon" size="small" icon={close.icon} accessibilityLabel={close.label} onPress={close.onPress} testID={close.testID}/>}
    <Text numberOfLines={1} style={[theme.type.rowHelp, {flex: 1, textAlign: form === 'tv' ? 'left' : 'center', color: theme.colors.textSecondary}]}>{loading ? '' : context}</Text>
    {form !== 'tv' && <Button variant="icon" size="small" icon={more.icon} accessibilityLabel={more.label} disabled={more.disabled || loading} onPress={more.onPress} testID={more.testID}/>}
  </View>;
  const art = <View style={{width: column, height: column}}>{loading ? <SkeletonBlock width={column} height={column} radius={theme.radii.artwork} shine/> : <ArtBox kind="square" source={artwork} icon={icon} width={column} alt={title}/>}</View>;
  // Two title lines and a byline are kept; the words sit on the slot's bottom edge, so a short title leaves the room above it.
  const words = <View style={{height: spec.titleSlot[form], justifyContent: 'flex-end', gap: theme.space.tight}}>
    {loading ? <><SkeletonBlock width="64%" height={theme.type.title.fontSize}/><SkeletonBlock width="40%" height={theme.type.intro.fontSize}/></> : <>
      <Text accessibilityRole="header" numberOfLines={2} style={[theme.type.title, {color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      <Text numberOfLines={1} style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{byline ?? ' '}</Text>
      {detail !== undefined && <Text numberOfLines={1} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </>}
  </View>;
  const seek = <View style={{height: spec.scrubber[form], justifyContent: 'center', gap: frameSpec.player.timesGap[form]}}>{scrubber}</View>;
  const buttons = <View style={{height: spec.transport[form], flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>{transport.map((item, index) => control(item, index === 2))}</View>;
  const next = <View style={{height: spec.nextLine[form], justifyContent: 'center'}}><Text numberOfLines={1} style={[theme.type.rowHelp, {textAlign: wide ? 'left' : 'center', color: theme.colors.textTertiary}]}>{loading ? '' : nextLine}</Text></View>;
  const bottom = <View style={{height: spec.bottomRow[form], flexDirection: 'row', alignItems: 'center'}}>{options.map(item => <Focusable key={item.label} testID={item.testID} accessibilityRole="button" accessibilityLabel={item.label} accessibilityState={{disabled: !!item.disabled, selected: item.active}}
    disabled={item.disabled || loading} onPress={item.onPress} focusRadius={theme.baseRadii.control} style={{flex: 1, height: '100%', alignItems: 'center', justifyContent: 'center', gap: theme.space.tight, opacity: item.disabled || loading ? theme.spec.button.disabledOpacity : 1}}>
    {item.icon ? <Icon name={item.icon} size={theme.spec.transport.control.icon[theme.platform]} color={item.active ? theme.colors.accentStrong : theme.colors.text}/> : <Text numberOfLines={1} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{item.value}</Text>}
    <Text numberOfLines={1} style={[theme.type.micro, {color: theme.colors.textTertiary}]}>{item.label}</Text>
  </Focusable>)}</View>;
  if (!wide) return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, frameSpec.sheet.footerClear), alignItems: 'center'}}>
    <View style={{flex: 1, width: column + 2 * inset, paddingHorizontal: inset}}>
      {top}
      {/* The flexible region: the artwork hangs from the top of this space and whatever is left stays empty under it. */}
      <View style={{flex: 1, paddingTop: spec.gap[form], overflow: 'hidden'}}>{art}</View>
      {words}
      <View style={{marginTop: spec.gap[form]}}>{seek}</View>
      {buttons}{next}{bottom}
    </View>
  </View>;
  return <View testID={testID} style={{flex: 1, flexDirection: 'row', gap: spec.gap[form], paddingTop: (form === 'tv' ? 0 : insets.top) + frame.titleTop + frame.headerGap, paddingLeft: inset, paddingRight: frame.rightGutter, paddingBottom: frame.bottomClear}}>
    <View style={{width: column}}>{top}<View style={{marginTop: spec.gap[form] / 2}}>{art}</View></View>
    <View style={{width: spec.controlsWidth[form], paddingTop: spec.topBar[form] + spec.gap[form] / 2}}>{words}<View style={{marginTop: spec.gap[form] / 2}}>{seek}</View>{buttons}{next}{bottom}</View>
    {upNext !== undefined && <ScrollView style={{flex: 1, minWidth: spec.upNextWidth[form], marginTop: spec.topBar[form] + spec.gap[form] / 2}}>{upNext}</ScrollView>}
  </View>;
}
