import React, {type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import type {ArtworkSource} from '../artwork/types';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';
import {PlayerControl} from '../player/parts';
import {ArtBox} from './parts';

/** One control in a fixed slot. A control the item doesn't support keeps its slot with `disabled`, dimmed. */
export type AudioControl = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean; /** Back or forward: the seconds it skips, drawn in the arrow. */ skip?: number; testID?: string}>;
/** A bottom-row slot. Music's are a glyph over its label, or `inline` with the glyph beside it (the device). An
 * audiobook's are tiles: a short `value` ("1.25×", "28 min") or a glyph over the label, outlined in the accent when `active`. */
export type AudioOption = Omit<AudioControl, 'icon'> & Readonly<{icon?: IconName; value?: string; inline?: boolean}>;

export type AudioPlayerFrameProps = Readonly<{
  /** Close (the down arrow) and, opposite it, More. Between them, where this is playing from. */
  close: AudioControl; more: AudioControl; context?: string;
  /** What it is playing from, said strongly after `context` ("Playing from album" / "After Hours"): under it on a phone, beside it elsewhere. */
  source?: string;
  /** The cover. With none, the placeholder with `icon` (music or book) fills the same box. */
  artwork?: ArtworkSource | null; icon: IconName;
  title: string; byline?: string;
  /** The heart at the end of the title row. Not drawn on TV. */
  favourite?: AudioControl;
  /** An audiobook: the chapter line over the scrubber ("Chapter 8" ... "of 19"). */
  chapter?: Readonly<{label: string; of: string}>;
  /** The kit's scrubber for this player; the frame gives it a fixed box. */
  scrubber: ReactNode;
  /** Five fixed slots; the middle one is Play or Pause. */
  transport: readonly [AudioControl, AudioControl, AudioControl, AudioControl, AudioControl];
  /** What plays next ("Next" / "Scared To Live"). Its line is kept, blank, when there is nothing to say. */
  next?: Readonly<{label: string; title: string}>;
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
export function AudioPlayerFrame({close, more, context, artwork, icon, title, byline, favourite, source, chapter, scrubber, transport, next: upcoming, options, upNext, loading = false, testID}: AudioPlayerFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), spec = frameSpec.audio, {form} = frame;
  const tv = form === 'tv', phone = form === 'phone', inset = spec.inset[form], panel = !phone && !tv && upNext !== undefined ? spec.upNextWidth[form] : 0;
  // The artwork is the spec's size where the window has room; a short window (a tablet on its side) gets the largest square that leaves every fixed region its place.
  const tiles = options.some(item => item.value !== undefined), chapterLine = frame.rowTwoLine / 2;
  const fixed = (chapter ? chapterLine : 0) + spec.topBar[form] + theme.space.sm + spec.titleSlot[form] + spec.scrubber[form] + spec.transport[form] + spec.nextLine[form] + spec.bottomRow[form] + insets.top + Math.max(insets.bottom, frameSpec.sheet.footerClear) + (phone ? spec.gap[form] : 0);
  const column = tv ? spec.artwork[form] : Math.max(0, Math.min(spec.artwork[form], frame.width - panel - 2 * inset, window.height - fixed));
  // Shuffle and repeat are plain glyphs on TV; previous and next keep their discs (music-plus).
  const control = (item: AudioControl, index: number) => <PlayerControl key={item.label} {...item} primary={index === 2} plain={index === 0 || index === 4} disabled={item.disabled || loading}/>;
  const glyph = (item: AudioControl) => <Button variant="text" size="small" iconOnly icon={item.icon} iconColor={item.active ? theme.colors.accentStrong : theme.colors.text} accessibilityLabel={item.label} disabled={item.disabled || loading} onPress={item.onPress} testID={item.testID}/>;
  const top = <View style={{height: spec.topBar[form], flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    {!tv && glyph(close)}
    <View style={{flex: 1, minWidth: 0, alignItems: phone ? 'center' : 'flex-start', flexDirection: phone ? 'column' : 'row', gap: phone ? 0 : theme.space.tight}}>
      <Text numberOfLines={1} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{loading ? '' : context}</Text>
      {source !== undefined && !loading && <Text numberOfLines={1} style={[phone ? theme.type.rowLabel : theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{source}</Text>}
    </View>
    {!tv && glyph(more)}
  </View>;
  const art = <View style={{width: column, height: column}}>{loading ? <SkeletonBlock width={column} height={column} radius={theme.radii.artwork} shine/> : <ArtBox kind="square" source={artwork} icon={icon} width={column} alt={title}/>}</View>;
  // Two title lines and a byline are kept, from the slot's top edge, so a short title leaves its room under it.
  // On TV the words sit on the slot's bottom edge, against the scrubber; elsewhere they hang from its top, under the artwork.
  const words = <View style={{height: spec.titleSlot[form], flexDirection: 'row', alignItems: tv ? 'flex-end' : 'flex-start', gap: theme.space.xs}}>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>{loading ? <><SkeletonBlock width="64%" height={theme.type.title.fontSize}/><SkeletonBlock width="40%" height={theme.type.intro.fontSize}/></> : <>
      <Text accessibilityRole="header" numberOfLines={2} style={[tv ? theme.type.pageTitle : theme.type.title, {color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      <Text numberOfLines={1} style={[theme.type.intro, {color: theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily}]}>{byline ?? ' '}</Text>
    </>}</View>
    {!tv && favourite && !loading && glyph(favourite)}
  </View>;
  const seek = <>
    {chapter && <View style={{height: chapterLine, flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
      <Icon name="i-list" size={frameSpec.player.pillIcon} color={theme.colors.textSecondary}/><Text numberOfLines={1} style={[theme.type.rowLabel, {flex: 1, color: theme.colors.textSecondary}]}>{loading ? '' : chapter.label}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{loading ? '' : chapter.of}</Text></View>}
    <View style={{height: spec.scrubber[form], justifyContent: 'center', gap: frameSpec.player.timesGap[form]}}>{scrubber}</View>
  </>;
  const buttons = <View style={{height: spec.transport[form], flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>{transport.map(control)}</View>;
  const next = <View style={{height: spec.nextLine[form], justifyContent: 'center'}}>{upcoming && !loading && <Text numberOfLines={1} style={[theme.type.rowHelp, {textAlign: tv ? 'left' : 'center', color: theme.colors.textTertiary}]}>
    {upcoming.label}  <Text style={{color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}}>{upcoming.title}</Text></Text>}</View>;
  const optionLabel = [theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}];
  const option = (item: AudioOption) => {
    const state = {disabled: !!item.disabled, selected: item.active}, off = item.disabled || loading, tint = item.active ? theme.colors.accentStrong : theme.colors.text;
    // TV (music-plus): labelled buttons with the button's own corners.
    if (tv) return <Button key={item.label} testID={item.testID} variant="glass" size="compact" icon={item.icon} disabled={off} onPress={item.onPress}>{item.value !== undefined ? `${item.value}  ${item.label}` : item.label}</Button>;
    const glyph = item.icon && <Icon name={item.icon} size={tiles || item.inline ? frameSpec.player.promptIcon : theme.spec.transport.control.icon[theme.platform]} color={tint}/>;
    return <Focusable key={item.label} testID={item.testID} accessibilityRole="button" accessibilityLabel={item.value !== undefined ? `${item.label}, ${item.value}` : item.label} accessibilityState={state} disabled={off} onPress={item.onPress} focusRadius={theme.baseRadii.control}
      style={[{flex: 1, height: '100%', alignItems: 'center', justifyContent: 'center', gap: theme.space.tight, opacity: off ? theme.spec.button.disabledOpacity : 1}, item.inline && !tiles && {flexDirection: 'row', gap: theme.space.xs},
        tiles && {borderRadius: frameSpec.card.radius.phone, backgroundColor: alpha(theme.colors.text, frameSpec.player.tint.pill), borderWidth: theme.space.hairline, borderColor: item.active ? theme.colors.accentStrong : 'transparent'}]}>
      {item.value !== undefined ? <Text numberOfLines={1} style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.pageTitle.fontFamily}]}>{item.value}</Text> : glyph}
      <Text numberOfLines={1} style={optionLabel}>{item.label}</Text>
    </Focusable>;
  };
  const bottom = <View style={[{flexDirection: 'row', alignItems: 'center', gap: tiles || tv ? theme.space.xs : 0}, tv ? {minHeight: spec.bottomRow[form], flexWrap: 'wrap'} : {height: spec.bottomRow[form]}]}>{options.map(option)}</View>;
  // The artwork, the title, the scrubber and the transport read as one group (phone-app 14, tablet-play 9 and 10). What is left of
  // the height is below the group on a phone (the next line and the bottom row sit on the bottom edge) and around it on a tablet.
  const stack = <View style={{flex: phone ? 1 : undefined, flexGrow: 1, width: Math.max(column, spec.artwork.phone) + 2 * inset, paddingHorizontal: inset, alignSelf: 'center', justifyContent: phone ? 'flex-start' : 'center'}}>
    {phone && top}
    <View style={{marginTop: phone ? spec.gap[form] : 0, alignItems: 'center'}}>{art}</View>
    <View style={{marginTop: theme.space.sm}}>{words}</View>
    {seek}{buttons}
    {phone && <View style={{flex: 1}}/>}
    {next}{bottom}
  </View>;
  if (phone) return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, frameSpec.sheet.footerClear)}}>{stack}</View>;
  if (!tv) return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, frameSpec.sheet.footerClear)}}>
    <View style={{paddingHorizontal: frame.gutter}}>{top}</View>
    <View style={{flex: 1, flexDirection: 'row'}}>
      {stack}
      {upNext !== undefined && <ScrollView style={{width: panel, flexGrow: 0}} contentContainerStyle={{paddingRight: frame.gutter, paddingBottom: frame.bottomClear}}>{upNext}</ScrollView>}
    </View>
  </View>;
  // TV (music-plus "np-tv"): the artwork, the title and controls beside it and Up next are one row, centred in the screen's height.
  const row = spec.topBar[form] + spec.gap[form] / 2 + column;
  return <View testID={testID} style={{flex: 1, justifyContent: 'center', paddingLeft: inset, paddingRight: frame.rightGutter}}>
    <View style={{height: row, flexDirection: 'row', gap: spec.gap[form]}}>
      <View style={{width: column}}>{top}<View style={{marginTop: spec.gap[form] / 2}}>{art}</View></View>
      <View style={{width: spec.controlsWidth[form], paddingTop: spec.topBar[form] + spec.gap[form] / 2, justifyContent: 'center'}}>{words}<View style={{marginTop: spec.gap[form] / 2}}>{seek}</View>{buttons}{next}{bottom}</View>
      {upNext !== undefined && <ScrollView style={{flex: 1, minWidth: spec.upNextWidth[form], marginTop: spec.topBar[form] + spec.gap[form] / 2}}>{upNext}</ScrollView>}
    </View>
  </View>;
}
