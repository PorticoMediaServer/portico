import React from 'react';
import {Text, View} from 'react-native';
import {ProfileFace} from '../../../kit/artwork';
import {Segmented} from '../../../kit/controls';
import {BarList} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import {shellType} from '../../../kit/shell/visual';
import {OwnerCalm, OwnerSection, OwnerStatisticTiles} from './parts';
import {OWNER_VIEWING_PERIODS, ownerBarRows, ownerStatistics, ownerViewingPeriodLabel, type OwnerViewing, type OwnerViewingPeriod, type Translate} from './model';

const faceColors = ['mint', 'violet', 'gold', 'blue', 'slate', 'rose', 'sky'] as const;

/** Viewing statistics: the range's four totals, then Most played and Most active from Play history. */
export function OwnerStatisticsSection({summary, error, period, onPeriod, onRetry, t}: {summary?: OwnerViewing; error?: unknown; period: OwnerViewingPeriod; onPeriod: (period: OwnerViewingPeriod) => void; onRetry: () => void; t: Translate}) {
  const theme = useTheme();
  const figures = ownerStatistics(summary, t);
  const played = ownerBarRows(summary?.mostPlayed ?? [], 'played');
  const active = ownerBarRows(summary?.mostActive ?? [], 'active');
  return <OwnerSection title={t('settings.server.statistics')}>
    <Segmented testID="owner-statistics-period" accessibilityLabel={t('web.telemetry.period')} value={period} onValueChange={value => onPeriod(value as OwnerViewingPeriod)} fill style={{marginBottom: 12}}
      items={OWNER_VIEWING_PERIODS.map(id => ({value: id, label: t(ownerViewingPeriodLabel(id))}))}/>
    {error !== undefined && !summary ? <OwnerCalm icon="i-warn" tone="error" title={t('next.shell.activityError')} action={{label: t('next.shell.tryAgain'), onPress: onRetry}}/> : null}
    {!summary && error === undefined ? <OwnerCalm icon="i-clock" title={t('next.states.loading')}/> : null}
    {summary && !summary.totals.plays ? <OwnerCalm icon="i-clock" title={t('web.telemetry.noViewing')}/> : null}
    {summary && summary.totals.plays ? <OwnerStatisticTiles figures={figures}/> : null}
    {summary && played.length ? <RankedList title={t('server.viewing.mostPlayed')} t={t} rows={played}/> : null}
    {summary && active.length ? <RankedList title={t('server.viewing.mostActive')} t={t} rows={active}/> : null}
  </OwnerSection>;
}

function RankedList({title, rows, t}: {title: string; rows: ReturnType<typeof ownerBarRows>; t: Translate}) {
  const theme = useTheme();
  return <View style={{marginTop: 12, borderRadius: 14, backgroundColor: theme.colors.raised, paddingHorizontal: 16, paddingTop: 14, paddingBottom: 10}}>
    <Text style={[shellType(14, 700), {color: theme.colors.textSecondary}]}>{title}</Text>
    <BarList rows={rows.map(({initial, ...row}, index) => ({...row, leading: initial ? <ProfileFace initial={initial} color={faceColors[index % faceColors.length]} size={24} scaleOnFocus={false}/> : undefined}))}/>
  </View>;
}
