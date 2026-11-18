import React, {useState, type ReactNode} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {frameSpec} from '@portico/design';
import {MediaArt, type MediaArtKind} from '../artwork/Artwork';
import type {ArtworkSource} from '../artwork/types';
import {Button} from '../controls/Button';
import {Icon, type IconName} from '../Icon';
import {SettingsGroup} from '../shell/SettingsControls';
import {alpha} from '../shell/visual';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

/** Rule 12: what an artwork box shows when there is no picture. A diagonal raised fill, a 1 px inner line and the
 * kind's icon at a third of the short side. It fills its parent, so the box is always full. */
export function ArtPlaceholder({icon}: {icon: IconName}) {
  const theme = useTheme(), [short, setShort] = useState(0);
  return <View pointerEvents="none" style={StyleSheet.absoluteFill} onLayout={event => setShort(Math.min(event.nativeEvent.layout.width, event.nativeEvent.layout.height))}>
    <Svg accessible={false} width="100%" height="100%" style={StyleSheet.absoluteFill}>
      <Defs><LinearGradient id="frame-art-placeholder" x1="0" y1="0" x2="1" y2="1"><Stop offset="0" stopColor={theme.colors.raisedHigh}/><Stop offset="1" stopColor={theme.colors.raised}/></LinearGradient></Defs>
      <Rect width="100%" height="100%" fill="url(#frame-art-placeholder)"/>
    </Svg>
    <View style={[StyleSheet.absoluteFill, {alignItems: 'center', justifyContent: 'center', borderWidth: 1, borderColor: alpha(theme.colors.text, frameSpec.placeholder.lineAlpha), borderRadius: theme.radii.artwork}]}>
      {short > 0 && <Icon name={icon} size={short * frameSpec.placeholder.iconShare} color={theme.colors.textTertiary}/>}
    </View>
  </View>;
}

/** An artwork slot that keeps its box: the picture when there is one (arriving with the kit's fade), the placeholder
 * under it otherwise. `icon` is the kind's icon (music, book, film, tv). */
export function ArtBox({kind, source, icon, width, alt}: {kind: MediaArtKind; source?: ArtworkSource | null; icon: IconName; width: number; alt?: string}) {
  return <MediaArt kind={kind} source={source} alt={alt} width={width} fallback={<ArtPlaceholder icon={icon}/>}/>;
}

/** A block that is not a row of cards (a title page's episode list or details): its heading at the gutter, then its
 * content. A row of cards is the kit `Row`, which draws its own heading; never put a `Row` inside a `Section`. */
export function Section({title, subtitle, trailing, children}: {title: string; /** A quieter line beside the heading: "Movies, TV Shows and Anime". */ subtitle?: string; trailing?: ReactNode; children: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, marginLeft: frame.gutter, marginRight: frame.rightGutter, marginBottom: frame.headingGap}}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {flex: 1, color: theme.colors.text}]}>{title}{subtitle !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{'   '}{subtitle}</Text>}</Text>{trailing}
    </View>
    {children}
  </View>;
}

/** Rule 9, stale data: the section keeps what it last had and says so beside its label. */
export type SectionStale = Readonly<{label: string; retryLabel: string; onRetry: () => void}>;

/** Rule 7: one group of a settings or owner page. Its label, then one raised card of rows. In-section loading,
 * empty and error are a `StateRow` as the card's only child. */
export function SectionCard({label, help, stale, children}: {label?: string; /** The section's help paragraph under its label (desktop-settings f1). */ help?: string; stale?: SectionStale; children: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View>
    {label !== undefined && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, marginHorizontal: frame.gutter, marginBottom: frame.headingGap, minHeight: stale ? theme.targets.pointer : undefined}}>
      <Text accessibilityRole="header" style={[theme.type.groupLabel, {color: theme.colors.textSecondary, flexShrink: 1}]}>{label}</Text>
      {stale && <><Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.amber}]}>{stale.label}</Text>
        <Button variant="glass" size="xs" onPress={stale.onRetry}>{stale.retryLabel}</Button></>}
    </View>}
    {help !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginHorizontal: frame.gutter, marginBottom: frame.headingGap, maxWidth: frameSpec.settings.paragraphMax}]}>{help}</Text>}
    <SettingsGroup style={{marginHorizontal: frame.cardInset, borderRadius: frame.cardRadius}}>{children}</SettingsGroup>
  </View>;
}

export type StateRowProps =
  | Readonly<{kind: 'loading'}>
  | Readonly<{kind: 'empty' | 'error'; icon: IconName; title: string; reason?: string; action?: Readonly<{label: string; onPress: () => void}>}>;

/** Rule 9: loading, empty or error inside one section, as one row of the section's own card. It has a list row's
 * shape and height (tile, one bold line, one line of reason, one extra-small button), so nothing jumps when the rows
 * arrive, and the rest of the page carries on. Never a centred block and never a large title. */
export function StateRow(props: StateRowProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  const spec = frameSpec.stateRow, tile = spec.tile[frame.form];
  const row = {minHeight: frame.rowTwoLine, paddingHorizontal: frame.cardPadding, paddingVertical: theme.space.xs, flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap} as const;
  // The row sits inside a section's raised card, so its placeholders take the step above it, without the shine (its stripe
  // repaints the card's colour, so on a card only the stripe showed).
  const on = {backgroundColor: theme.colors.raisedHigh};
  if (props.kind === 'loading') return <View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={row}>
    <SkeletonBlock width={tile} height={tile} radius={theme.baseRadii.control} style={on}/>
    <View style={{flex: 1, gap: theme.space.xs}}><SkeletonBlock width="46%" height={theme.type.rowHelp.fontSize} style={on}/><SkeletonBlock width="72%" height={theme.type.rowHelp.fontSize} style={on}/></View>
  </View>;
  const error = props.kind === 'error', tint = error ? theme.colors.record : theme.colors.textSecondary;
  return <View style={row}>
    <View style={{width: tile, height: tile, borderRadius: theme.baseRadii.control, alignItems: 'center', justifyContent: 'center', backgroundColor: error ? alpha(theme.colors.record, spec.errorTint) : theme.colors.raisedBright}}>
      <Icon name={props.icon} size={tile * spec.iconShare} color={tint}/>
    </View>
    <View style={{flex: 1, minWidth: 0}}>
      <Text numberOfLines={2} style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{props.title}</Text>
      {props.reason !== undefined && <Text numberOfLines={2} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{props.reason}</Text>}
    </View>
    {props.action && <Button variant="glass" size="xs" onPress={props.action.onPress}>{props.action.label}</Button>}
  </View>;
}
