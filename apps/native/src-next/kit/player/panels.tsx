import React, {Children, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {BackCloseHints} from '../controls/KeyHints';
import {useFrameMetrics, usePlayerUpright} from '../frames/metrics';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

const spec = frameSpec.player.panel;
type PanelAction = Readonly<{label: string; onPress: () => void}>;

/** One of the player's own panels (Playback options, Subtitles, Audio, Quality, Speed, Episodes): a header and a
 * list. `PlayerChromeFrame` places it (its `panel`): under the picture as a sheet in the upright form (phone-player
 * 3b), beside the picture on a tablet (tablet-play 2), over the picture's right side on a phone on its side and a TV
 * (phone-player 3, tv-player 4). The panel fills the box it is given, so its top never moves with what it holds.
 * `back` is for a list opened from Playback options; its title is then that list's name. A TV closes with the
 * remote's Back, so it draws no close button. */
export function PlayerPanel({title, note, back, close, footer, children, testID}: {title: string; /** Under the title: what a change here lasts for. */ note?: string; back?: PanelAction; close: PanelAction; /** Pinned under the list (the Episodes note). */ footer?: string; children: ReactNode; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), upright = usePlayerUpright(), tv = form === 'tv', pad = spec.padding[form], sheet = upright;
  // The Android line: a sheet's footer clears the navigation bar.
  const insets = useSafeAreaInsets();
  return <View testID={testID} accessibilityViewIsModal style={[{flex: 1, backgroundColor: theme.colors.raised}, sheet && {borderTopLeftRadius: frameSpec.sheet.fullRadius, borderTopRightRadius: frameSpec.sheet.fullRadius}]}>
    {sheet && <View style={{alignSelf: 'center', marginTop: spec.grab.top, width: spec.grab.width, height: spec.grab.height, borderRadius: spec.grab.height / 2, backgroundColor: alpha(theme.colors.text, frameSpec.player.tint.buffered)}}/>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, paddingHorizontal: pad, paddingTop: sheet ? theme.space.xs : pad, minHeight: theme.targets.touch + (sheet ? theme.space.xs : pad)}}>
      {back && <Button variant="text" size="small" icon="i-left" iconOnly iconColor={theme.colors.text} accessibilityLabel={back.label} onPress={back.onPress} testID={testID ? `${testID}-back` : undefined}/>}
      <Text accessibilityRole="header" numberOfLines={1} style={[tv ? theme.type.pageTitle : theme.type.rowHeading, {flex: 1, color: theme.colors.text}]}>{title}</Text>
      {!tv && (sheet ? <Button variant="text" size="small" onPress={close.onPress} testID={testID ? `${testID}-close` : undefined}>{close.label}</Button>
        : <Button variant="icon" size="small" icon="i-x" accessibilityLabel={close.label} onPress={close.onPress} testID={testID ? `${testID}-close` : undefined}/>)}
    </View>
    {note !== undefined && <View style={{flexDirection: 'row', gap: theme.space.xs, paddingHorizontal: pad, paddingTop: theme.space.tight}}>
      <Icon name="i-clock" size={frameSpec.player.pillIcon} color={theme.colors.accentStrong}/><Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textSecondary}]}>{note}</Text>
    </View>}
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: pad, paddingTop: theme.space.sm, paddingBottom: pad, gap: theme.space.sm}}>{children}</ScrollView>
    {footer !== undefined && <View style={{flexDirection: 'row', gap: theme.space.xs, padding: pad, paddingBottom: pad + (sheet ? insets.bottom : 0), borderTopWidth: theme.space.hairline, borderTopColor: theme.colors.line}}>
      <Icon name="i-info" size={frameSpec.player.pillIcon} color={theme.colors.accentStrong}/><Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textSecondary}]}>{footer}</Text>
    </View>}
    {/* R11: a TV panel closes with the remote's Back, so its foot says so (player-tv 4). */}
    {tv && <View style={{paddingHorizontal: pad, paddingBottom: pad}}><BackCloseHints/></View>}
  </View>;
}

/** One list inside a panel: an optional label, its rows on one raised card with a hairline between them, and an
 * optional line under the card saying what is happening now ("Converting to 1080p for this connection."). */
export function PlayerPanelGroup({label, note, children}: {label?: string; note?: string; children: ReactNode}) {
  const theme = useTheme(), rows = Children.toArray(children);
  return <View style={{gap: theme.space.xs}}>
    {label !== undefined && <Text accessibilityRole="header" style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily}]}>{label}</Text>}
    <View style={{borderRadius: frameSpec.card.radius.phone, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh}}>
      {rows.map((row, index) => <View key={index} style={index > 0 && {borderTopWidth: theme.space.hairline, borderTopColor: theme.colors.line}}>{row}</View>)}
    </View>
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{note}</Text>}
  </View>;
}

/** One row of a panel. A choice is marked by its tick alone when `selected`. An entry of the options list shows its
 * `icon` on a small tile, its current `value` (with `qualifier` before it, dimmer) and a chevron. A choice the title
 * cannot offer stays in its place, dimmed, with `unavailable`. `trailing` is for a row's own control (Add, a switch). */
export function PlayerPanelRow({label, detail, icon, value, qualifier, selected = false, unavailable, trailing, onPress, testID}: {label: string; detail?: string; icon?: IconName; value?: string; qualifier?: string; selected?: boolean; /** Why it cannot be chosen, said at the row's end. */ unavailable?: string; trailing?: ReactNode; onPress?: () => void; testID: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), tile = spec.iconTile[form];
  const end = [theme.type.rowHelp, {color: theme.colors.textSecondary}];
  return <Focusable testID={testID} focusId={testID} focusKind="row" accessibilityRole="button" accessibilityLabel={[label, detail, qualifier, value, unavailable].filter(Boolean).join(', ')} accessibilityState={{selected, disabled: unavailable !== undefined}}
    disabled={unavailable !== undefined || !onPress} onPress={onPress}
    style={() => ({minHeight: spec.row[form], paddingVertical: theme.space.xs, paddingHorizontal: frameSpec.card.padding.phone, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap,
      opacity: unavailable !== undefined ? theme.spec.button.disabledOpacity : 1})}>
    {/* R1 "current": on TV the chosen option is an accent tick before its label, and stays when focus is elsewhere
        (player-tv 4); focus is the kit ring. Elsewhere the tick stays at the row's end. */}
    {theme.isTV && !icon && value === undefined && <View style={{width: frameSpec.player.promptIcon}}>{selected && <Icon name="i-check" size={frameSpec.player.promptIcon} color={theme.colors.accentStrong}/>}</View>}
    {icon && <View style={{width: tile, height: tile, borderRadius: theme.baseRadii.control, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedSoft}}><Icon name={icon} size={tile * frameSpec.stateRow.iconShare} color={theme.colors.textSecondary}/></View>}
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
      {detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </View>
    {trailing}
    {unavailable !== undefined ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{unavailable}</Text>
      : value !== undefined ? <><Text style={end}>{qualifier !== undefined && <Text style={{color: theme.colors.text}}>{qualifier}  </Text>}{value}</Text><Icon name="i-right" size={frameSpec.player.pillIcon} color={theme.colors.textTertiary}/></>
      : selected && !theme.isTV && <Icon name="i-check" size={frameSpec.player.promptIcon} color={theme.colors.accentStrong}/>}
  </Focusable>;
}

/** Live TV: channel down or up, the size of a transport control, with the channel it goes to under (down) or over
 * (up) the arrow (tv-player 11, phone-player 1c). A plain glyph on a phone and tablet, on a disc on TV. */
export function PlayerChannelStep({direction, number, label, onPress, disabled, testID}: {direction: 'down' | 'up'; number: string; /** For assistive technology: the direction and the channel it goes to. */ label: string; onPress: () => void; disabled?: boolean; testID: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), size = theme.spec.transport.control.size[theme.column];
  const channel = <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{number}</Text>;
  return <Focusable testID={testID} focusId={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} focusRadius={size / 2}
    style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center', backgroundColor: form === 'tv' ? alpha(theme.colors.text, frameSpec.player.tint.pill) : 'transparent', opacity: disabled ? theme.spec.button.disabledOpacity : 1}}>
    {direction === 'up' && <Icon name="i-chup" size={frameSpec.player.promptIcon} color={theme.colors.text}/>}{channel}{direction === 'down' && <Icon name="i-chdown" size={frameSpec.player.promptIcon} color={theme.colors.text}/>}
  </Focusable>;
}
