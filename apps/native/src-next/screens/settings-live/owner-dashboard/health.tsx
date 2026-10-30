import React, {useState} from 'react';
import {Text, View} from 'react-native';
import Svg, {Path} from 'react-native-svg';
import {Segmented} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {StatTiles} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {formatBytes} from '../../../../../../packages/client-core/src/presentation/content';
import type {LoadTile} from '../../../../../../packages/client-core/src/server-admin/dashboard';
import type {TelemetryReading} from '../../../../../../packages/client-core/src/telemetry';
import {OwnerCalm, OwnerSection} from './parts';
import {OWNER_HEALTH_WINDOWS, ownerHealthDetail, ownerHealthTiles, ownerHealthWindowLabel, type OwnerHealthWindow, type Translate} from './model';

/** Health: the six real load tiles with their own recent history, and one tile opened on a true axis. */
export function OwnerHealthSection({reading, error, window, onWindow, note, onRetry, t}: {reading?: TelemetryReading; error?: unknown; window: OwnerHealthWindow; onWindow: (window: OwnerHealthWindow) => void; note?: string; onRetry: () => void; t: Translate}) {
  const theme = useTheme(), [width, setWidth] = useState(0), [opened, setOpened] = useState<string>();
  const tiles = ownerHealthTiles(reading, t);
  const detail = opened && reading ? ownerHealthDetail(reading, opened as LoadTile['metric'], t) : undefined;
  const selectedTile = tiles.find(tile => tile.id === opened);
  return <OwnerSection title={t('settings.server.health')}>
    <Segmented testID="owner-health-window" accessibilityLabel={t('web.telemetry.period')} value={window} onValueChange={value => onWindow(value as OwnerHealthWindow)} style={{alignSelf: 'stretch', marginBottom: 12}} itemProps={() => ({style: {flex: 1}})}
      items={OWNER_HEALTH_WINDOWS.map(id => ({value: id, label: t(ownerHealthWindowLabel(id))}))}/>
    {error !== undefined && !reading ? <OwnerCalm icon="i-gauge" tone="error" title={t('next.shell.activityError')} action={{label: t('next.shell.tryAgain'), onPress: onRetry}}/> : null}
    {!reading && error === undefined ? <OwnerCalm icon="i-gauge" title={t('next.states.loading')}/> : null}
    {reading && !tiles.length ? <OwnerCalm icon="i-gauge" title={t('server.health.loadNotMeasured')}/> : null}
    {tiles.length ? <View onLayout={event => setWidth(event.nativeEvent.layout.width)}>{width > 0 && <StatTiles testID="owner-health" tiles={tiles} width={width} mode="health" onTilePress={id => setOpened(id)}/>}</View> : null}
    {note && tiles.length ? <Text style={[shellType(13, 400, 18.85), {color: theme.colors.textTertiary, marginTop: 10, marginHorizontal: 4}]}>{note}</Text> : null}
    <BottomSheet open={Boolean(detail && selectedTile)} onOpenChange={open => {if (!open) setOpened(undefined);}} title={detail?.label} testID="owner-health-detail" fitContent>
      {detail ? <HealthChart detail={detail} t={t}/> : null}
    </BottomSheet>
  </OwnerSection>;
}

function HealthChart({detail, t}: {detail: NonNullable<ReturnType<typeof ownerHealthDetail>>; t: Translate}) {
  const theme = useTheme(), [width, setWidth] = useState(0), height = 170;
  const points = detail.points, first = points[0], last = points[points.length - 1];
  const x = (point: {t: number}) => last.t > first.t ? (point.t - first.t) / (last.t - first.t) * width : 0;
  const y = (value: number) => height - Math.max(0, Math.min(1, (value - detail.min) / Math.max(1e-9, detail.max - detail.min))) * height;
  const line = points.map((point, index) => `${index ? 'L' : 'M'}${x(point).toFixed(1)},${y(point.v).toFixed(1)}`).join(' ');
  const percent = detail.unit === 'percent';
  const axis = [detail.max, (detail.max + detail.min) / 2, detail.min];
  const value = (amount: number) => percent ? `${Math.round(amount)}%` : detail.unit === 'rate' ? `${formatBytes(amount)}/s` : String(Math.round(amount));
  return <>
    <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 6}}><Text style={[shellType(34, 800, 'normal', -.03), {color: theme.colors.text}]}>{detail.value}</Text><Text style={[shellType(13, 600), {color: theme.colors.textTertiary}]}>{t('next.dashboard.nowValue')}</Text></View>
    <View onLayout={event => setWidth(event.nativeEvent.layout.width)} style={{height: height + 2, marginTop: 14}}>
      {axis.map((amount, index) => <View key={index} pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, top: index === 0 ? 0 : index === 1 ? height / 2 : height, borderTopWidth: 1, borderTopColor: index === 2 ? theme.colors.line : theme.colors.lineSoft}}>
        <Text style={[shellType(11, 600), {color: theme.colors.textTertiary, position: 'absolute', right: 0, top: -14, backgroundColor: theme.colors.overlaySurface, paddingHorizontal: 3}]}>{value(amount)}</Text>
      </View>)}
      <Svg accessible={false} width={width} height={height} style={{position: 'absolute', left: 0, top: 0}}>
        <Path d={`${line} L${width},${height} L0,${height} Z`} fill={alpha(theme.colors.accent, .22)}/>
        <Path d={line} stroke={theme.colors.accentStrong} strokeWidth={2} fill="none" strokeLinejoin="round"/>
      </Svg>
      <View pointerEvents="none" style={{position: 'absolute', left: Math.max(0, Math.min(width - 7, x(last) - 3.5)), top: Math.max(0, Math.min(height - 7, y(last.v) - 3.5)), width: 7, height: 7, borderRadius: 4, backgroundColor: theme.colors.accentStrong, borderWidth: 2, borderColor: theme.colors.overlaySurface}}/>
    </View>
    <View style={{flexDirection: 'row', justifyContent: 'space-between', marginTop: 6}}>
      <Text style={[shellType(11.5, 600), {color: theme.colors.textTertiary}]}>{when(first.t)}</Text>
      <Text style={[shellType(11.5, 600), {color: theme.colors.textTertiary}]}>{when(points[Math.floor(points.length / 2)].t)}</Text>
      <Text style={[shellType(11.5, 600), {color: theme.colors.textTertiary}]}>{t('next.dashboard.axis.Now')}</Text>
    </View>
    <View style={{flexDirection: 'row', gap: 2, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.lineSoft, marginTop: 18}}>
      {[{id: 'lowest', label: t('next.dashboard.lowest'), amount: detail.lowest}, {id: 'average', label: t('next.dashboard.averageValue'), amount: detail.average}, {id: 'highest', label: t('next.dashboard.highest'), amount: detail.highest}].map(cell => <View key={cell.id} style={{flex: 1, backgroundColor: theme.colors.raised, paddingHorizontal: 12, paddingVertical: 12}}>
        <Text style={[shellType(12.5, 600), {color: theme.colors.textTertiary}]}>{cell.label}</Text>
        <Text style={[shellType(20, 800, 'normal', -.02), {color: theme.colors.text, marginTop: 2}]}>{value(cell.amount)}</Text>
        {cell.id === 'highest' ? <Text numberOfLines={1} style={[shellType(11.5, 600), {color: theme.colors.textTertiary, marginTop: 1}]}>{t('next.dashboard.highestAt', {time: detail.highestAt})}</Text> : null}
      </View>)}
    </View>
  </>;
}
