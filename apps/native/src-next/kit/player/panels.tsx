import React, {Children, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {BackCloseHints} from '../controls/KeyHints';
import {ListEdgeFade, useListEdges} from '../layout/ListEdgeFade';
import {useFrameMetrics, usePlayerUpright} from '../frames/metrics';
import {Icon, type IconName} from '../Icon';
import {TVRow} from '../layout/TVRow';
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
export function PlayerPanel({title, note, back, close, footer, children, testID}: {title: string; /** Under the title: what a change here lasts for. */ note?: string; back?: PanelAction; close: PanelAction; /** Pinned under the list (the Episodes note; TV's Back hint, R11). */ footer?: ReactNode; children: ReactNode; testID?: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), upright = usePlayerUpright(), insets = useSafeAreaInsets(), edges = useListEdges(theme.space.xl), tv = form === 'tv', pad = spec.padding[form], sheet = upright;
  return <View testID={testID} accessibilityViewIsModal style={[{flex: 1, backgroundColor: theme.colors.raised}, sheet && {borderTopLeftRadius: frameSpec.sheet.fullRadius, borderTopRightRadius: frameSpec.sheet.fullRadius}]}>
    {sheet && <View style={{alignSelf: 'center', marginTop: spec.grab.top, width: spec.grab.width, height: spec.grab.height, borderRadius: spec.grab.height / 2, backgroundColor: alpha(theme.colors.text, frameSpec.player.tint.buffered)}}/>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, paddingHorizontal: pad, paddingTop: sheet ? theme.space.xs : pad, minHeight: theme.targets.touch + (sheet ? theme.space.xs : pad)}}>
      {back && <Button variant="text" size="small" icon="i-left" iconOnly iconColor={theme.colors.text} accessibilityLabel={back.label} onPress={back.onPress} testID={testID ? `${testID}-back` : undefined}/>}
      <Text accessibilityRole="header" numberOfLines={1} style={[tv ? theme.type.pageTitle : theme.type.rowHeading, {flex: 1, color: theme.colors.text}]}>{title}</Text>
      {!tv && (sheet ? <Button variant="text" size="small" onPress={close.onPress} testID={testID ? `${testID}-close` : undefined}>{close.label}</Button>
        : <Button variant="icon" size="small" icon="i-x" accessibilityLabel={close.label} onPress={close.onPress} testID={testID ? `${testID}-close` : undefined}/>)}
    </View>
    {note !== undefined && <View style={{flexDirection: 'row', gap: theme.space.xs, paddingHorizontal: pad, paddingTop: theme.space.tight}}>
      {/* TV says it in words alone under the title (player-tv 4); touch and pointer forms lead with the clock. */}
      {!tv && <Icon name="i-clock" size={frameSpec.player.pillIcon} color={theme.colors.accentStrong}/>}<Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textSecondary}]}>{note}</Text>
    </View>}
    {/* A list that runs past the panel fades into it at that edge instead of being cut at the foot (player-tv 4). */}
    <View style={{flex: 1}}>
      <ScrollView style={{flex: 1}} {...edges.scroll} contentContainerStyle={{paddingHorizontal: pad, paddingTop: theme.space.sm, paddingBottom: pad, gap: theme.space.sm}}>{children}</ScrollView>
      {edges.edges.top && <ListEdgeFade edge="top" length={theme.space.xl} opacity={1} color={theme.colors.raised}/>}
      {edges.edges.bottom && <ListEdgeFade edge="bottom" length={theme.space.xl} opacity={1} color={theme.colors.raised}/>}
    </View>
    {/* The footer clears the home indicator and Android's navigation bar. */}
    {footer !== undefined && <View style={{flexDirection: 'row', gap: theme.space.xs, padding: pad, paddingBottom: Math.max(pad, sheet ? insets.bottom + theme.space.xs : 0), borderTopWidth: theme.space.hairline, borderTopColor: theme.colors.line}}>
      {typeof footer === 'string' ? <><Icon name="i-info" size={frameSpec.player.pillIcon} color={theme.colors.accentStrong}/><Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textSecondary}]}>{footer}</Text></> : footer}
    </View>}
    {/* R11: a TV panel closes with the remote's Back, so its foot says so (player-tv 4). */}
    {tv && <View style={{paddingHorizontal: pad, paddingBottom: pad}}><BackCloseHints/></View>}
  </View>;
}

/** One list inside a panel: an optional label, its rows on one raised card with a hairline between them, and
 * optional lines under the card saying what is happening now ("Home network, up to 1080p" / "Playing the original
 * file"). With `heading` the list opens in place under its heading (owner ruling 4: TV, and a tablet's options,
 * tablet-play 2b): the heading row names the list and its current value in the accent, and its rows follow while open. */
export function PlayerPanelGroup({label, note, heading, children}: {label?: string; note?: string | readonly string[];
  heading?: Readonly<{label: string; value?: string; open: boolean; onPress: () => void; testID: string}>; children: ReactNode}) {
  const theme = useTheme(), {form} = useFrameMetrics(), rows = Children.toArray(children), notes = note === undefined ? [] : typeof note === 'string' ? [note] : note;
  const head = heading && <Focusable testID={heading.testID} focusId={heading.testID} focusKind="row" accessibilityRole="button" accessibilityState={{expanded: heading.open}} accessibilityLabel={[heading.label, heading.value].filter(Boolean).join(', ')} onPress={heading.onPress}
    style={{minHeight: spec.row[form], paddingHorizontal: frameSpec.card.padding.phone, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
    <Text style={[theme.type.rowLabel, {flex: 1, color: theme.colors.text}]}>{heading.label}</Text>
    {heading.value !== undefined && <Text style={[theme.type.rowLabel, {color: theme.colors.accentStrong}]}>{heading.value}</Text>}
    <Icon name={heading.open ? 'i-chup' : 'i-chdown'} size={frameSpec.player.pillIcon} color={theme.colors.textTertiary}/>
  </Focusable>;
  const list = heading ? heading.open ? rows : [] : rows;
  return <View style={{gap: theme.space.xs}}>
    {label !== undefined && <Text accessibilityRole="header" style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily}]}>{label}</Text>}
    {/* Not clipped on TV: a focused row's ring and glow spread past the card (player-tv 4), and Android cuts any drawing at a
        clipping parent's edge; the rows round their own focus. Elsewhere the card clips its rows' fills to its corners. */}
    <View style={{borderRadius: frameSpec.card.radius.phone, overflow: theme.isTV ? undefined : 'hidden', backgroundColor: theme.colors.raisedHigh}}>
      {head}
      {list.map((row, index) => <View key={index} style={(index > 0 || head) && {borderTopWidth: theme.space.hairline, borderTopColor: theme.colors.line}}>{row}</View>)}
    </View>
    {(heading ? heading.open : true) && notes.map((line, index) => <Text key={index} style={[theme.type.rowHelp, {color: index === 0 ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{line}</Text>)}
  </View>;
}

/** One row of a panel. A choice is marked by its tick alone when `selected`. An entry of the options list shows its
 * `icon` on a small tile, its current `value` (with `qualifier` before it, dimmer) and a chevron. A choice the title
 * cannot offer stays in its place, dimmed, with `unavailable`. `trailing` is for a row's own control (Add, a switch). */
export function PlayerPanelRow({label, detail, icon, value, qualifier, selected = false, unavailable, trailing, onPress, testID}: {label: string; detail?: string; icon?: IconName; value?: string; qualifier?: string; selected?: boolean; /** Why it cannot be chosen, said at the row's end. */ unavailable?: string; trailing?: ReactNode; onPress?: () => void; testID: string}) {
  const theme = useTheme(), {form} = useFrameMetrics(), tile = spec.iconTile[form], tv = form === 'tv';
  // A value is near-white at the row's size (phone-player 3); on TV the current choice's tick comes before its label (tv-player 4).
  const end = [theme.type.rowLabel, {color: theme.colors.textSecondary}];
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
    {unavailable !== undefined ? <Text style={[theme.type.rowLabel, {color: theme.colors.textTertiary}]}>{unavailable}</Text>
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

/** TV's Episodes (tv-player 1, frame 3): a band across the bottom of the picture, not a column. "Episodes" and the
 * count, the season at the right, then the season's tiles (the kit `EpisodeTile`) in a row that scrolls past the
 * player's edge. `PlayerChromeFrame` places it (its `band`) and hides the controls while it shows. */
export function PlayerEpisodeBand({title, count, season, children}: Readonly<{title: string; count?: string; season?: ReactNode; children: ReactNode}>) {
  const theme = useTheme(), edge = frameSpec.player.edge.tv, focus = theme.spec.focus;
  // The room a focused tile grows into and rings, as the kit Row keeps it (taken back by the margins, so the band's box is its tiles).
  const tile = theme.spec.episode.width.tv * 9 / 16, room = Math.ceil(tile * (focus.scale.card.tv - 1) / 2 + (focus.offsetOuter.tv + focus.ringWidth.tv) * focus.scale.card.tv);
  return <View style={{gap: theme.space.md}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: edge}}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textTertiary}]}>{count}</Text>
      {season}
    </View>
    <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} style={{marginVertical: -room}} contentContainerStyle={{gap: theme.space.md, paddingHorizontal: edge, paddingVertical: room}}>{children}</ScrollView></TVRow>
  </View>;
}
