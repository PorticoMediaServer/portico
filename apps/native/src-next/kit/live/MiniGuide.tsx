import React, {type Ref} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {channelTint} from '../../../../../packages/client-core/src/presentation/kit-model/index';
import {Button, Focusable} from '../controls';
import {Artwork} from '../artwork/Artwork';
import type {ArtworkSource} from '../artwork/types';
import {Icon} from '../Icon';
import {nativeType, useTheme} from '../theme';

/** The short guide's drawn parts (tv-player 11, tablet-play 8, phone-player 1e): its heading, one row per channel and
 * the way to the full guide. The screens (`kit/live/TVMiniGuide`, `HandheldMiniGuide`) read the guide, choose
 * the rows and own the opening; these only draw what they are given. TV is the larger form. A row's ids are
 * `tv-mini-guide-<key>` on TV and `watch-mini-guide-<key>` on a phone and tablet. */
const prefix = (tv: boolean) => tv ? 'tv-mini-guide' : 'watch-mini-guide';

/** "Guide", with Close on a phone and tablet and the remote's hint on TV. `docked`: the tablet's pane beside the picture. */
export function MiniGuideHead({close, docked = false, upright = false}: Readonly<{
  close?: Readonly<{label: string; onPress: () => void; ref?: Ref<View>}>; docked?: boolean; upright?: boolean;
}>) {
  const theme = useTheme(), t = defaultI18n.t;
  if (theme.isTV) return <View style={{flexDirection: 'row', alignItems: 'baseline', justifyContent: 'space-between', paddingHorizontal: 12, paddingTop: 4, paddingBottom: 12}}>
    <Text style={[nativeType({size: 30, weight: 800, tracking: -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{t('next.controls.guide')}</Text>
    <Text style={[nativeType({size: 19, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.live.miniGuideHint')}</Text>
  </View>;
  return <View style={{flexDirection: 'row', alignItems: 'center', paddingTop: docked ? 24 : 8, paddingLeft: docked ? 24 : 18, paddingRight: docked ? 16 : 6, paddingBottom: docked ? 10 : 0}}>
    <Text accessibilityRole="header" style={[nativeType({size: docked ? 24 : upright ? 19 : 18, weight: 800, tracking: docked ? -.025 : -.02, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text, flex: 1}]}>{t('next.controls.guide')}</Text>
    {close && <Button ref={close.ref} testID="watch-mini-guide-close" focusId="watch-mini-guide-close" variant="text" iconOnly icon="i-close"
      accessibilityLabel={close.label} onPress={close.onPress} style={{width: 44, height: 44, borderRadius: 22}}/>}
  </View>;
}

export type MiniGuideRowProps = Readonly<{
  /** The channel's key: kind, source and id (cross-source channels can share an id). */
  id: string;
  channel: Readonly<{name: string; number?: string; mark?: Readonly<{text: string; seed: string}>; tuneAvailable: boolean}>;
  logo?: ArtworkSource;
  /** What is on now; "No information" when absent. */
  title?: string;
  /** How much of what is on has played, 0 to 1. */
  progress?: number;
  /** What is next, and its clock time when the guide's zone names one. */
  next?: Readonly<{title: string; at?: string}>;
  /** The channel playing now: "Watching", and the row is raised. */
  watching: boolean;
  disabled: boolean; busy?: boolean;
  /** The tablet's pane beside the picture: larger rows, edge to edge. */
  docked?: boolean;
  onPress: () => void;
  /** TV: the row focus starts on. */
  preferredFocus?: boolean; ref?: Ref<View>;
}>;

/** One channel: its mark (or logo, or number), name and number, "Watching", what is on, its bar, and what is next. */
export function MiniGuideRow({id, channel, logo, title, progress, next, watching, disabled, busy, docked = false, onPress, preferredFocus, ref}: MiniGuideRowProps) {
  const theme = useTheme(), t = defaultI18n.t, tv = theme.isTV, name = `${prefix(tv)}-${id}`;
  const programme = title ?? t('guide.noInformation'), nextAt = next?.at ? t('next.live.nextAt', {time: next.at}) : undefined;
  const type = (size: number, weight: 400 | 600 | 700 | 800) => nativeType({size, weight, tracking: 0, lineHeight: 'normal'}, theme.textScale);
  if (tv) {
    const mark = channel.mark ? <View style={{width: 60, height: 60, borderRadius: 60 * 8 / 34, marginRight: 20, overflow: 'hidden', backgroundColor: channelTint(channel.mark.seed), alignItems: 'center', justifyContent: 'center'}}>
      <Text numberOfLines={1} adjustsFontSizeToFit allowFontScaling={false} style={[nativeType({size: channel.mark.text.length > 2 ? 60 * 0.32 : 60 * 0.38, weight: 800, tracking: -.01, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{channel.mark.text}</Text>
      {logo ? <Artwork source={logo} sized={false} visible style={StyleSheet.absoluteFillObject}/> : null}</View>
      : logo ? <Artwork source={logo} width={60} height={60} style={{borderRadius: 14, overflow: 'hidden', marginRight: 20}}/>
      : <View style={{width: 60, height: 60, borderRadius: 14, marginRight: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: '#edf5fa14'}}>
        <Text numberOfLines={1} adjustsFontSizeToFit style={[nativeType({size: 20, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{channel.number}</Text></View>;
    return <Focusable ref={ref} focusId={name} testID={name} focusKind="row" focusRadius={16} preferredFocus={preferredFocus} disabled={disabled} accessibilityRole="button"
      accessibilityLabel={[channel.name, channel.number, watching ? t('next.live.watching') : undefined, programme, next && nextAt, next?.title].filter(Boolean).join(', ')}
      accessibilityState={{selected: watching}} onPress={() => {if (!disabled) onPress();}}
      style={({focused}) => ({flexDirection: 'row', alignItems: 'center', height: 112, paddingHorizontal: 16, borderRadius: 16, marginBottom: 4, opacity: channel.tuneAvailable ? 1 : .55,
        backgroundColor: focused ? 'rgba(237,245,250,.16)' : watching ? 'rgba(237,245,250,.07)' : 'transparent'})}>
      {mark}
      <View style={{flex: 1, minWidth: 0}}>
        <View style={{flexDirection: 'row', alignItems: 'center'}}>
          <Text numberOfLines={1} style={[type(19, 700), {color: theme.colors.textTertiary}]}>{channel.name}</Text>
          <Text style={[type(19, 600), {color: theme.colors.textTertiary, marginLeft: 10}]}>{channel.number}</Text>
          {watching && <Text style={[type(19, 800), {color: theme.colors.accentStrong, marginLeft: 'auto'}]}>{t('next.live.watching')}</Text>}
        </View>
        <Text numberOfLines={1} style={[type(25, 700), {color: title ? theme.colors.text : theme.colors.textTertiary, marginTop: 2}]}>{programme}</Text>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, marginTop: 8}}>
          {progress !== undefined && <View style={{width: 150, height: 5, borderRadius: 3, backgroundColor: '#edf5fa26', overflow: 'hidden'}}>
            <View style={{width: `${Math.round(progress * 100)}%`, height: '100%', backgroundColor: theme.colors.accentStrong}}/></View>}
          {!!next && <Text numberOfLines={1} style={[type(18, 600), {color: theme.colors.textTertiary, flexShrink: 1}]}>
            {nextAt ? <>{nextAt}<Text style={{color: theme.colors.textSecondary}}>{`  ${next.title}`}</Text></> : next.title}</Text>}
        </View>
      </View>
    </Focusable>;
  }
  const size = docked ? 40 : 38;
  return <Focusable testID={name} focusId={name} focusKind="row" focusRadius={12}
    accessibilityRole="button" accessibilityLabel={[channel.name, channel.number, programme].filter(Boolean).join(', ')}
    accessibilityState={{selected: watching, disabled, busy}} disabled={disabled} onPress={() => {if (!disabled) onPress();}}
    style={{minHeight: docked ? 64 : 60, paddingVertical: docked ? 10 : 6, paddingLeft: docked ? 24 : 10, paddingRight: docked ? 20 : 10,
      flexDirection: 'row', alignItems: 'center', gap: docked ? 14 : 12, borderRadius: docked ? 0 : 12, opacity: channel.tuneAvailable ? 1 : .55,
      backgroundColor: watching ? 'rgba(237,245,250,.07)' : 'transparent'}}>
    <View style={{width: size, height: size, borderRadius: 9, overflow: 'hidden', alignItems: 'center', justifyContent: 'center',
      backgroundColor: channel.mark ? channelTint(channel.mark.seed) : '#edf5fa14'}}>
      <Text numberOfLines={1} style={[nativeType({size: docked ? 13 : 12, weight: 800, tracking: -.01, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{channel.mark?.text ?? channel.number}</Text>
      {logo && <Artwork source={logo} sized={false} visible style={StyleSheet.absoluteFillObject}/>}
    </View>
    <View style={{flex: 1, minWidth: 0}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
        <Text numberOfLines={1} style={[type(docked ? 12.5 : 12, docked ? 600 : 700), {color: theme.colors.textTertiary, flexShrink: 1}]}>{channel.name}</Text>
        {!!channel.number && <Text style={[theme.type.caption, {fontSize: 12, color: theme.colors.textTertiary}]}>{channel.number}</Text>}
        {watching && <Text style={[theme.type.captionStrong, {fontSize: 12, color: theme.colors.accentStrong, marginLeft: 'auto'}]}>{t('next.live.watching')}</Text>}
      </View>
      <Text numberOfLines={1} style={[type(docked ? 15 : 14.5, 700), {color: title ? theme.colors.text : theme.colors.textTertiary, marginTop: docked ? 2 : 1}]}>{programme}</Text>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: docked ? 6 : 5}}>
        {progress !== undefined && <View testID={`watch-mini-guide-progress-${id}`} style={{width: 70, height: 3, borderRadius: 2, backgroundColor: '#edf5fa26', overflow: 'hidden'}}>
          <View style={{width: `${progress * 100}%`, height: '100%', backgroundColor: theme.colors.accentStrong}}/>
        </View>}
        {next && <Text numberOfLines={1} style={[type(12, 600), {color: theme.colors.textTertiary, flexShrink: 1}]}>
          {nextAt ?? next.title}{nextAt && !docked && <Text style={{color: theme.colors.textSecondary}}>{`  ${next.title}`}</Text>}
        </Text>}
      </View>
    </View>
  </Focusable>;
}

/** "Open the full guide", after the rows. */
export function MiniGuideAll({disabled, docked = false, onPress, ref}: Readonly<{disabled: boolean; docked?: boolean; onPress: () => void; ref?: Ref<View>}>) {
  const theme = useTheme(), t = defaultI18n.t, tv = theme.isTV, name = `${prefix(tv)}-all`;
  const label = (size: number) => <Text style={[nativeType({size, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('next.live.openFullGuide')}</Text>;
  if (tv) return <Focusable ref={ref} focusId={name} testID={name} focusKind="row" focusRadius={12} disabled={disabled}
    onPress={() => {if (!disabled) onPress();}} style={({focused}) => ({flexDirection: 'row', alignItems: 'center', gap: 6,
      paddingTop: 14, paddingHorizontal: 16, paddingBottom: 4, borderRadius: 12, backgroundColor: focused ? 'rgba(237,245,250,.12)' : 'transparent'})}>
    {label(22)}<Icon name="i-right" size={22} color={theme.colors.textSecondary}/>
  </Focusable>;
  return <Focusable testID={name} focusId={name} accessibilityRole="button" disabled={disabled} onPress={() => {if (!disabled) onPress();}}
    style={{minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 4, paddingHorizontal: docked ? 24 : 10, paddingTop: docked ? 14 : 2, paddingBottom: 12}}>
    {label(docked ? 14.5 : 14)}<Icon name="i-right" size={18} color={theme.colors.textTertiary}/>
  </Focusable>;
}
