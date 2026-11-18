import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {ArtworkSource} from '../artwork/types';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {ArtBox} from '../frames/parts';
import {useFrameMetrics} from '../frames/metrics';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

const spec = frameSpec.player.panel;
type PanelAction = Readonly<{label: string; onPress: () => void}>;

/** One of the player's own panels (Playback options, Subtitles, Audio, Quality, Speed, Episodes): a header and a
 * list. It places itself over the player: a column on the picture's right edge from tablet width (tablet-play 2,
 * tv-player 4), a sheet from the bottom on a phone (phone-player 3). `back` is for a panel opened from the options
 * list. A TV closes with the remote's Back, so it draws no close button. */
export function PlayerPanel({title, note, back, close, footer, children, testID}: {title: string; /** Under the title: what a change here lasts for. */ note?: string; back?: PanelAction; close: PanelAction; /** Pinned under the list. */ footer?: ReactNode; children: ReactNode; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), phone = form === 'phone', tv = form === 'tv', pad = spec.padding[form];
  return <View testID={testID} accessibilityViewIsModal style={[{position: 'absolute', right: 0, bottom: 0, backgroundColor: theme.colors.raised},
    phone ? {left: 0, maxHeight: `${spec.sheetShare * 100}%`, borderTopLeftRadius: frameSpec.sheet.fullRadius, borderTopRightRadius: frameSpec.sheet.fullRadius} : {top: 0, width: spec.width[form]}]}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, paddingHorizontal: pad, paddingTop: pad, minHeight: theme.targets.pointer + pad}}>
      {back && <Button variant="text" iconOnly icon="i-left" accessibilityLabel={back.label} onPress={back.onPress}/>}
      <Text accessibilityRole="header" style={[tv ? theme.type.pageTitle : theme.type.barTitle, {flex: 1, color: theme.colors.text}]}>{title}</Text>
      {!tv && (phone ? <Button variant="text" size="small" onPress={close.onPress}>{close.label}</Button> : <Button variant="glass" iconOnly icon="i-x" accessibilityLabel={close.label} onPress={close.onPress}/>)}
    </View>
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, paddingHorizontal: pad, paddingTop: theme.space.xs}]}>{note}</Text>}
    <ScrollView style={{flexShrink: 1}} contentContainerStyle={{paddingHorizontal: pad, paddingTop: theme.space.sm, paddingBottom: pad, gap: theme.space.sm}}>{children}</ScrollView>
    {footer}
  </View>;
}

/** One list inside a panel: its label with the current choice beside it, then its rows on one raised card. */
export function PlayerPanelGroup({label, value, children}: {label?: string; value?: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.xs}}>
    {label !== undefined && <View style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.space.xs}}>
      <Text accessibilityRole="header" style={[theme.type.groupLabel, {flex: 1, color: theme.colors.text}]}>{label}</Text>
      {value !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{value}</Text>}
    </View>}
    <View style={{borderRadius: frameSpec.card.radius.phone, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh}}>{children}</View>
  </View>;
}

/** One row of a panel. A choice shows its tick when `selected`; an entry of the options list shows its `icon`, its
 * current `value` and a chevron. A choice the title cannot offer stays in its place, dimmed, with `unavailable`. */
export function PlayerPanelRow({label, detail, icon, value, selected = false, unavailable, onPress, testID}: {label: string; detail?: string; icon?: IconName; value?: string; selected?: boolean; /** Why it cannot be chosen, said at the row's end. */ unavailable?: string; onPress: () => void; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics();
  return <Focusable testID={testID} focusKind="row" accessibilityRole="button" accessibilityLabel={[label, detail, value, unavailable].filter(Boolean).join(', ')} accessibilityState={{selected, disabled: unavailable !== undefined}}
    disabled={unavailable !== undefined} onPress={onPress}
    style={({focused}) => ({minHeight: spec.row[form], paddingVertical: theme.space.xs, paddingHorizontal: frameSpec.card.padding.phone, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap,
      backgroundColor: focused || selected ? alpha(theme.colors.text, frameSpec.player.tint.pill) : 'transparent', opacity: unavailable !== undefined ? theme.spec.button.disabledOpacity : 1})}>
    {icon && <Icon name={icon} size={frameSpec.player.promptIcon} color={theme.colors.textSecondary}/>}
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
      {detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </View>
    {unavailable !== undefined ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{unavailable}</Text>
      : value !== undefined ? <><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{value}</Text><Icon name="i-right" size={frameSpec.player.pillIcon} color={theme.colors.textTertiary}/></>
      : selected && <Icon name="i-check" size={frameSpec.player.promptIcon} color={theme.colors.accentStrong}/>}
  </Focusable>;
}

/** One episode in the Episodes panel: its picture (or the placeholder), its title on up to two lines, one line of
 * facts and two of synopsis. The one that is playing is tinted. */
export function PlayerEpisodeRow({still, title, facts, overview, playing = false, onPress, testID}: {still?: ArtworkSource | null; title: string; facts: string; overview?: string; playing?: boolean; onPress: () => void; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics();
  return <Focusable testID={testID} focusKind="row" accessibilityRole="button" accessibilityLabel={[title, facts].join(', ')} accessibilityState={{selected: playing}} onPress={onPress}
    style={({focused}) => ({flexDirection: 'row', gap: theme.space.controlGap, padding: theme.space.xs, borderRadius: frameSpec.card.radius.phone, backgroundColor: focused || playing ? alpha(theme.colors.text, frameSpec.player.tint.pill) : 'transparent'})}>
    <ArtBox kind="wide" icon="i-tv" source={still} width={spec.still[form]}/>
    <View style={{flex: 1, minWidth: 0}}>
      <Text numberOfLines={2} style={[theme.type.rowLabel, {color: playing ? theme.colors.accentStrong : theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{title}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{facts}</Text>
      {overview !== undefined && <Text numberOfLines={2} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{overview}</Text>}
    </View>
  </Focusable>;
}

/** Live TV: channel down or up, the size of a transport control, with the channel it goes to under (down) or over
 * (up) the arrow (tv-player 11). */
export function PlayerChannelStep({direction, number, label, onPress, disabled, testID}: {direction: 'down' | 'up'; number: string; /** For assistive technology: the direction and the channel it goes to. */ label: string; onPress: () => void; disabled?: boolean; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), size = theme.spec.transport.control.size[theme.platform];
  const channel = <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{number}</Text>;
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} focusRadius={size / 2}
    style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, frameSpec.player.tint.pill), opacity: disabled ? theme.spec.button.disabledOpacity : 1}}>
    {direction === 'up' && <Icon name="i-chup" size={frameSpec.player.pillIcon} color={theme.colors.text}/>}{channel}{direction === 'down' && <Icon name="i-chdown" size={frameSpec.player.pillIcon} color={theme.colors.text}/>}
  </Focusable>;
}
