import React, {type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
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
export type AudioControl = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean; /** Back or forward: the seconds it skips, drawn in the arrow. */ skip?: number; testID?: string}>;
/** A bottom-row slot shows an icon or a short value ("1.25×") over its label. */
export type AudioOption = Omit<AudioControl, 'icon'> & Readonly<{icon?: IconName; value?: string}>;

export type AudioPlayerFrameProps = Readonly<{
  /** Close (the down arrow) and, opposite it, More. Between them, where this is playing from. */
  close: AudioControl; more: AudioControl; context?: string;
  /** The cover. With none, the placeholder with `icon` (music or book) fills the same box. */
  artwork?: ArtworkSource | null; icon: IconName;
  title: string; byline?: string;
  /** The heart at the end of the title row. Not drawn on TV. */
  favourite?: AudioControl;
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

/** Frame 8, now playing for music and audiobooks (rule 12). Every region has a fixed box, so a missing cover, a long
 * title or a missing control moves nothing. A phone (phone-app 14) is one column: the bar, the artwork, the title
 * straight under it, then the scrubber, transport, next line and bottom row stacked up from the bottom edge; the
 * room that is left sits between the title and the scrubber. A tablet (tablet-play 9 and 10) keeps that column,
 * larger, in the room beside an Up next panel on the right, under a bar that spans the window. TV (music-plus) puts
 * the title and controls beside the artwork and Up next after them. Loading fills the same boxes. */
export function AudioPlayerFrame({close, more, context, artwork, icon, title, byline, favourite, detail, scrubber, transport, nextLine, options, upNext, loading = false, testID}: AudioPlayerFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), spec = frameSpec.audio, {form} = frame;
  const tv = form === 'tv', phone = form === 'phone', inset = spec.inset[form], panel = !phone && !tv && upNext !== undefined ? spec.upNextWidth[form] : 0;
  // The artwork is the spec's size where the window has room; a short window (a tablet on its side) gets the largest square that leaves every fixed region its place.
  const fixed = spec.topBar[form] + theme.space.sm + spec.titleSlot[form] + spec.scrubber[form] + spec.transport[form] + spec.nextLine[form] + spec.bottomRow[form] + insets.top + Math.max(insets.bottom, frameSpec.sheet.footerClear) + (phone ? spec.gap[form] : 0);
  const column = tv ? spec.artwork[form] : Math.max(0, Math.min(spec.artwork[form], frame.width - panel - 2 * inset, window.height - fixed));
  // Shuffle and repeat are plain glyphs on TV; previous and next keep their discs (music-plus).
  const control = (item: AudioControl, index: number) => <PlayerControl key={item.label} {...item} primary={index === 2} plain={index === 0 || index === 4} disabled={item.disabled || loading}/>;
  const glyph = (item: AudioControl) => <Button variant="text" size="small" iconOnly icon={item.icon} iconColor={item.active ? theme.colors.accentStrong : theme.colors.text} accessibilityLabel={item.label} disabled={item.disabled || loading} onPress={item.onPress} testID={item.testID}/>;
  const top = <View style={{height: spec.topBar[form], flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    {!tv && glyph(close)}
    <Text numberOfLines={1} style={[theme.type.rowHelp, {flex: 1, textAlign: phone ? 'center' : 'left', color: theme.colors.textSecondary}]}>{loading ? '' : context}</Text>
    {!tv && glyph(more)}
  </View>;
  const art = <View style={{width: column, height: column}}>{loading ? <SkeletonBlock width={column} height={column} radius={theme.radii.artwork} shine/> : <ArtBox kind="square" source={artwork} icon={icon} width={column} alt={title}/>}</View>;
  // Two title lines and a byline are kept, from the slot's top edge, so a short title leaves its room under it.
  const words = <View style={{height: spec.titleSlot[form], flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.xs}}>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>{loading ? <><SkeletonBlock width="64%" height={theme.type.title.fontSize}/><SkeletonBlock width="40%" height={theme.type.intro.fontSize}/></> : <>
      <Text accessibilityRole="header" numberOfLines={2} style={[tv ? theme.type.pageTitle : theme.type.title, {color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      <Text numberOfLines={1} style={[theme.type.intro, {color: theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily}]}>{byline ?? ' '}</Text>
      {detail !== undefined && <Text numberOfLines={1} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </>}</View>
    {!tv && favourite && !loading && glyph(favourite)}
  </View>;
  const seek = <View style={{height: spec.scrubber[form], justifyContent: 'center', gap: frameSpec.player.timesGap[form]}}>{scrubber}</View>;
  const buttons = <View style={{height: spec.transport[form], flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>{transport.map(control)}</View>;
  const next = <View style={{height: spec.nextLine[form], justifyContent: 'center'}}><Text numberOfLines={1} style={[theme.type.rowHelp, {textAlign: tv ? 'left' : 'center', color: theme.colors.textTertiary}]}>{loading ? '' : nextLine}</Text></View>;
  const bottom = <View style={{height: spec.bottomRow[form], flexDirection: 'row', alignItems: 'center'}}>{options.map(item => <Focusable key={item.label} testID={item.testID} accessibilityRole="button" accessibilityLabel={item.label} accessibilityState={{disabled: !!item.disabled, selected: item.active}}
    disabled={item.disabled || loading} onPress={item.onPress} focusRadius={theme.baseRadii.control} style={{flex: 1, height: '100%', alignItems: 'center', justifyContent: 'center', gap: theme.space.tight, opacity: item.disabled || loading ? theme.spec.button.disabledOpacity : 1}}>
    {item.icon ? <Icon name={item.icon} size={theme.spec.transport.control.icon[theme.platform]} color={item.active ? theme.colors.accentStrong : theme.colors.text}/> : <Text numberOfLines={1} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{item.value}</Text>}
    <Text numberOfLines={1} style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{item.label}</Text>
  </Focusable>)}</View>;
  const stack = <View style={{flex: 1, width: Math.max(column, spec.artwork.phone) + 2 * inset, paddingHorizontal: inset, alignSelf: 'center'}}>
    {phone && top}
    <View style={{marginTop: phone ? spec.gap[form] : 0, alignItems: 'center'}}>{art}</View>
    <View style={{marginTop: theme.space.sm}}>{words}</View>
    {/* The flexible region: what is left of the column's height stays empty here. */}
    <View style={{flex: 1}}/>
    {seek}{buttons}{next}{bottom}
  </View>;
  if (phone) return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, frameSpec.sheet.footerClear)}}>{stack}</View>;
  if (!tv) return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, frameSpec.sheet.footerClear)}}>
    <View style={{paddingHorizontal: frame.gutter}}>{top}</View>
    <View style={{flex: 1, flexDirection: 'row'}}>
      {stack}
      {upNext !== undefined && <ScrollView style={{width: panel, flexGrow: 0}} contentContainerStyle={{paddingRight: frame.gutter, paddingBottom: frame.bottomClear}}>{upNext}</ScrollView>}
    </View>
  </View>;
  return <View testID={testID} style={{flex: 1, flexDirection: 'row', gap: spec.gap[form], paddingTop: frame.titleTop + frame.headerGap, paddingLeft: inset, paddingRight: frame.rightGutter, paddingBottom: frame.bottomClear}}>
    <View style={{width: column}}>{top}<View style={{marginTop: spec.gap[form] / 2}}>{art}</View></View>
    <View style={{width: spec.controlsWidth[form], paddingTop: spec.topBar[form] + spec.gap[form] / 2}}>{words}<View style={{marginTop: spec.gap[form] / 2}}>{seek}</View>{buttons}{next}{bottom}</View>
    {upNext !== undefined && <ScrollView style={{flex: 1, minWidth: spec.upNextWidth[form], marginTop: spec.topBar[form] + spec.gap[form] / 2}}>{upNext}</ScrollView>}
  </View>;
}
