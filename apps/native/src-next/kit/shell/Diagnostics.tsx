import React, {useId, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Path, Stop, Circle} from 'react-native-svg';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Focusable} from '../controls/Focusable';
import {ProfileFace, type ProfileFaceProps} from '../artwork/Profiles';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {focusTestID} from '../focus/registry';
import {alpha, shellType} from './visual';
import {FactParts, type AccountAction} from './AccountShell';
import {serverRoleKey, type ServerRole} from './authority';

/** Curves stay within the supplied measured sample range. Missing samples break the trace. */
export function Sparkline({samples, width, height = 46, min, max, endDot = false}: {samples: readonly (number | null)[]; width: number; height?: number; min: number; max: number; endDot?: boolean}) {
  const {colors} = useTheme();
  const id = useId();
  if (!Number.isFinite(min) || !Number.isFinite(max) || max <= min || width <= 0 || height <= 0) return null;
  const segments: {x: number; y: number}[][] = [];
  samples.forEach((sample, index) => {
    if (sample === null || !Number.isFinite(sample)) {if (segments[segments.length - 1]?.length) segments.push([]); return;}
    if (!segments.length) segments.push([]);
    segments[segments.length - 1].push({x: samples.length > 1 ? index / (samples.length - 1) * width : 0, y: height - Math.max(0, Math.min(1, (sample - min) / (max - min))) * height});
  });
  return <Svg accessible={false} width={width} height={height}>
    <Defs><LinearGradient id={id} x1="0%" y1="0%" x2="0%" y2="100%"><Stop offset="0" stopColor={colors.accent} stopOpacity=".22"/><Stop offset="1" stopColor={colors.accent} stopOpacity="0"/></LinearGradient></Defs>
    {segments.filter(points => points.length > 1).map((points, index) => {
      const line = points.map((point, at) => at === 0 ? `M${point.x},${point.y}` : `C${(points[at - 1].x + point.x) / 2},${points[at - 1].y} ${(points[at - 1].x + point.x) / 2},${point.y} ${point.x},${point.y}`).join(' ');
      return <React.Fragment key={index}><Path d={`${line} L${points[points.length - 1].x},${height} L${points[0].x},${height} Z`} fill={`url(#${id})`}/><Path d={line} stroke={colors.accentStrong} strokeWidth={2} fill="none"/>{endDot && index === segments.filter(segment => segment.length > 1).length - 1 && <Circle cx={points[points.length - 1].x} cy={points[points.length - 1].y} r={3.5} fill={colors.accentStrong}/>}</React.Fragment>;
    })}
  </Svg>;
}
export type StatTile = Readonly<{id: string; label: string; value: string; samples?: readonly (number | null)[]; min?: number; max?: number}>;
export function StatTiles({tiles, columns, width, mode = 'health', onTilePress, testID}: {testID?: string; tiles: readonly StatTile[]; columns?: number; width: number; mode?: 'health' | 'viewing'; onTilePress?: (id: string) => void}) {
  const theme = useTheme();
  const phone = theme.platform === 'phone';
  const actualColumns = Math.max(1, columns ?? (phone ? 2 : mode === 'viewing' ? 4 : 3));
  const tileWidth = (width - (actualColumns - 1) * 2) / actualColumns;
  return <View style={{width, flexDirection: 'row', flexWrap: 'wrap', gap: 2, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.lineSoft}}>{tiles.map(tile => {const content = <>
    <View style={{flexDirection: mode === 'health' && !phone ? 'row' : 'column', justifyContent: 'space-between', alignItems: mode === 'health' && !phone ? 'baseline' : 'flex-start'}}><Text style={[shellType(13.5), {color: mode === 'health' ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{tile.label}</Text><Text style={[shellType(mode === 'health' ? phone ? 24 : 20 : 34, 800, phone ? mode === 'health' ? 26.4 : 37.4 : 'normal', mode === 'health' ? phone ? -.02 : -.01 : -.03), {color: theme.colors.text, marginTop: phone ? 3 : mode === 'viewing' ? 4 : 0}]}>{tile.value}</Text></View>
    {tile.samples && tile.min !== undefined && tile.max !== undefined && <View style={{marginTop: 8}}><Sparkline samples={tile.samples} width={tileWidth - (phone ? 28 : 36)} height={phone ? 40 : 46} min={tile.min} max={tile.max} endDot/></View>}
  </>;
    const style = {width: tileWidth, backgroundColor: theme.colors.raised, paddingHorizontal: phone ? mode === 'health' ? 14 : 16 : mode === 'health' ? 18 : 20, paddingTop: phone ? 14 : mode === 'health' ? 16 : 18, paddingBottom: phone ? mode === 'health' ? 10 : 15 : mode === 'health' ? 10 : 18};
    return onTilePress ? <Focusable testID={controlID(testID, 'stat', tile.id)} key={tile.id} accessibilityRole="button" accessibilityLabel={tile.label} accessibilityValue={{text: tile.value}} onPress={() => onTilePress(tile.id)} focusRadius={14} style={style}>{content}</Focusable> : <View key={tile.id} style={style}>{content}</View>;
  })}</View>;
}
export function BarList({rows}: {rows: readonly {id: string; label: string; value: string; fraction: number; leading?: ReactNode}[]}) {
  const theme = useTheme();
  const phone = theme.platform === 'phone';
  return <View>{rows.map(row => <View key={row.id} style={{minHeight: phone ? 47 : 32, paddingTop: phone ? 9 : 0, paddingBottom: phone ? 3 : 0, flexDirection: phone ? 'column' : 'row', alignItems: phone ? 'stretch' : 'center', gap: phone ? 7 : 12}}>
    <View style={{width: phone ? undefined : 170, flexDirection: 'row', alignItems: 'center', gap: 8}}>{row.leading}<Text numberOfLines={1} style={[shellType(phone ? 14.5 : 14), {color: theme.colors.text, flexShrink: 1, flex: phone ? 1 : undefined}]}>{row.label}</Text>{phone && <Text style={[shellType(14.5, 700), {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{row.value}</Text>}</View>
    <View style={{height: phone ? 6 : 8, flex: phone ? undefined : 1, borderRadius: phone ? 3 : 4, backgroundColor: alpha(theme.colors.text, .06), overflow: 'hidden'}}><View style={{height: phone ? 6 : 8, width: `${Math.max(0, Math.min(1, row.fraction)) * 100}%`, borderRadius: phone ? 3 : 4, backgroundColor: theme.colors.accent}}/></View>
    {!phone && <Text style={[shellType(14, 700), {width: 34, textAlign: 'right', color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{row.value}</Text>}
  </View>)}</View>;
}

export type PersonRowProps = {
  testID?: string;
  id: string;
  name: string;
  role: ServerRole;
  face: Pick<ProfileFaceProps, 'initial' | 'color' | 'tint' | 'source'>;
  facts: readonly FactPart[];
  activity?: {text: string; playing: boolean};
  onOpen?: () => void;
  actions?: readonly AccountAction[];
};
export function PersonRow({id, testID, name, role, face, facts, activity, onOpen, actions = []}: PersonRowProps) {
  const theme = useTheme();
  const content = <><ProfileFace {...face} size={theme.isTV ? 52 : 48} scaleOnFocus={false}/><View style={{flex: 1, minWidth: 0}}><View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}><Text numberOfLines={1} style={[shellType(theme.isTV ? 24 : 17, 700), {color: theme.colors.text, flexShrink: 1}]}>{name}</Text><Text style={[shellType(theme.isTV ? 18 : 12, 700), {color: role === 'owner' ? theme.colors.accentStrong : theme.colors.textSecondary, backgroundColor: role === 'owner' ? alpha(theme.colors.accent, .16) : 'transparent', borderWidth: role === 'owner' ? 0 : 1, borderColor: alpha(theme.colors.text, .24), borderRadius: 6, paddingHorizontal: 8, paddingVertical: 2}]}>{defaultI18n.t(serverRoleKey(role))}</Text></View><View style={{marginTop: 4}}><FactParts parts={facts} size={theme.isTV ? 20 : 14}/></View></View>{activity && <View style={{flexDirection: 'row', alignItems: 'center', gap: 7}}>{activity.playing && <View style={{width: 7, height: 7, borderRadius: 4, backgroundColor: theme.colors.accentStrong}}/>}<Text style={[shellType(theme.isTV ? 20 : 13, activity.playing ? 700 : 600), {color: theme.colors.textTertiary}]}>{activity.text}</Text></View>}</>;
  const style = {flexDirection: 'row' as const, gap: 16, alignItems: 'center' as const, paddingHorizontal: 20, paddingVertical: 18, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft};
  return <View>{theme.isTV || onOpen ? <Focusable testID={controlID(testID, 'entity', id)} accessibilityRole={onOpen && !theme.isTV ? 'button' : 'text'} accessibilityLabel={name} onPress={theme.isTV ? undefined : onOpen} focusKind="row" focusRadius={18} style={style}>{content}</Focusable> : <View style={style}>{content}</View>}{!theme.isTV && actions.length > 0 && <View style={{flexDirection: 'row', gap: 12, paddingHorizontal: 20, paddingBottom: 12}}>{actions.map(action => <Focusable key={action.id} testID={controlID(testID, 'entity', id, 'action', action.id)} accessibilityRole="button" accessibilityLabel={action.label} onPress={action.onPress}><Text style={[theme.type.captionStrong, {color: action.destructive ? theme.colors.record : theme.colors.accentStrong}]}>{action.label}</Text></Focusable>)}</View>}</View>;
}
export type DeviceRowProps = {
  testID?: string;
  id: string;
  name: string;
  icon: IconName;
  facts: readonly FactPart[];
  state?: string;
  waiting?: boolean;
  blocked?: boolean;
  onOpen?: () => void;
  actions?: readonly AccountAction[];
  handoff?: string;
};
export function DeviceRow({id, testID, name, icon, facts, state, waiting = false, blocked = false, onOpen, actions = [], handoff}: DeviceRowProps) {
  const theme = useTheme();
  const content = <><View style={{width: 44, height: 44, borderRadius: 11, backgroundColor: waiting ? alpha(theme.colors.amber, .14) : theme.colors.raisedHigh, alignItems: 'center', justifyContent: 'center'}}><Icon name={icon} size={24} color={waiting ? theme.colors.amber : theme.colors.textSecondary}/></View><View style={{flex: 1, minWidth: 0}}><Text style={[shellType(theme.isTV ? 24 : 15.5, 700), {color: theme.colors.text}]}>{name}</Text><View style={{marginTop: 3}}><FactParts parts={facts} size={theme.isTV ? 20 : 13.5} gap={8}/></View>{theme.isTV && handoff && <Text style={[shellType(20, 400, 29), {color: theme.colors.textTertiary, marginTop: 4}]}>{handoff}</Text>}</View>{state && <Text style={[shellType(theme.isTV ? 21 : 12.5, 700), {color: blocked ? theme.colors.danger : waiting ? theme.colors.amber : theme.colors.textTertiary}]}>{state}</Text>}</>;
  const style = {flexDirection: 'row' as const, gap: 16, alignItems: 'center' as const, paddingHorizontal: 20, paddingVertical: 16, borderTopWidth: 1, borderTopColor: waiting ? alpha(theme.colors.amber, .22) : theme.colors.lineSoft, backgroundColor: waiting ? alpha(theme.colors.amber, .1) : 'transparent'};
  return <View>{theme.isTV || onOpen ? <Focusable testID={controlID(testID, 'entity', id)} accessibilityRole={onOpen && !theme.isTV ? 'button' : 'text'} accessibilityLabel={name} onPress={theme.isTV ? undefined : onOpen} focusKind="row" focusRadius={18} style={style}>{content}</Focusable> : <View style={style}>{content}</View>}{!theme.isTV && actions.length > 0 && <View style={{flexDirection: 'row', gap: 10, paddingHorizontal: 20, paddingBottom: 16}}>{actions.map(action => <Focusable key={action.id} testID={controlID(testID, 'entity', id, 'action', action.id)} accessibilityRole="button" accessibilityLabel={action.label} onPress={action.onPress} style={{padding: 10, borderRadius: 10, backgroundColor: alpha(theme.colors.text, .1)}}><Text style={[shellType(14, 700), {color: action.destructive ? theme.colors.record : theme.colors.text}]}>{action.label}</Text></Focusable>)}</View>}</View>;
}
export function WaitingNotice({message, action}: {message: ReactNode; action?: {label: string; onPress: () => void}}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, padding: 14, borderRadius: 14, backgroundColor: alpha(theme.colors.amber, .1), borderWidth: 1, borderColor: alpha(theme.colors.amber, .22)}}><Icon name="i-warn" size={theme.isTV ? 30 : 22} color={theme.colors.amber}/><Text style={[shellType(theme.isTV ? 22 : 14, 400, theme.isTV ? 31.9 : 20.3), {color: theme.colors.textSecondary, flex: 1}]}>{message}</Text>{action && !theme.isTV && <Focusable accessibilityRole="button" accessibilityLabel={action.label} onPress={action.onPress}><Text style={[shellType(14, 700), {color: theme.colors.amber}]}>{action.label}</Text></Focusable>}</View>;
}

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
