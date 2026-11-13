import {Artwork} from '../../kit/artwork/Artwork';
import {accountAvatarStyle} from '../../kit/settings/account-avatar';
import {useCallback, useMemo, useState, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {STATISTICS_KINDS, type PlaySummary, type ServerStatistics, type StatisticsRange, type SUMMARY_KINDS} from '@core/admin/statistics.ts';
import {Select, Button, Segmented} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import type {DashboardBindings} from './bindings';
import {useDashboardRead, type DashboardAccounts} from './use-read';
import {fmtDur, fmtHrs, formatQuietHours, heatMapLabels} from './format';
import {hours, quietestHours} from './model';
import {Chart} from './Chart';
import {FloatingTip, type TipPoint} from './FloatingTip';
import {mediaLabel, Range, ReadFeedback, Section, t} from './shared';
import styles from './dashboard.module.css';

/** The server's kinds in its totals as the page names and colours them. */
export const statisticsMedia = {film: 'movies', episode: 'tv', music: 'music', audiobook: 'audiobooks', other: 'other'} as const;
const summaryRanges: readonly StatisticsRange[] = ['7d', '30d', '90d', 'all'];
const historyRanges: readonly StatisticsRange[] = ['7d', '30d', '90d', '1y', 'all'];
const statisticsChanges = ['playback.history', 'stream.resync'];
const duration = (ms: number) => fmtHrs(hours(ms));
const watchedDuration = (ms: number) => fmtDur(hours(ms));
type TopKind = keyof typeof SUMMARY_KINDS;

/** One summary read (Top people, Top played, Busiest times) for its own range and filters (rule 13). */
export function useSummary(b: DashboardBindings, range: StatisticsRange, kind?: TopKind, person?: string) {
  const load = useCallback((signal: AbortSignal) => b.read.summary({range, kind, person}, signal), [b, range, kind, person]);
  return useDashboardRead<PlaySummary>(b, load, true, 0, statisticsChanges);
}
export function useStatistics(b: DashboardBindings, range: StatisticsRange, enabled = true) {
  const load = useCallback((signal: AbortSignal) => b.read.statistics(range, signal), [b, range]);
  return useDashboardRead<ServerStatistics>(b, load, enabled, 0, statisticsChanges);
}

/** "From the latest 20,000 plays, since …": the summary counted only the newest plays of a long range. */
function CountedFrom({summary}: {summary?: PlaySummary}) {
  return summary?.countedFrom ? <p className={styles.countedFrom}>{t('next.dashboard.countedFrom', {count: summary.window, date: defaultI18n.date(summary.countedFrom)})}</p> : null;
}

export function Analytics({bindings: b, accounts}: {bindings: DashboardBindings; accounts: DashboardAccounts}) {
  const [peopleRange, setPeopleRange] = useState<StatisticsRange>('7d'), [historyRange, setHistoryRange] = useState<StatisticsRange>('30d'), [media, setMedia] = useState('all');
  const [topKind, setTopKind] = useState<TopKind>('movies'), [topRange, setTopRange] = useState<StatisticsRange>('30d'), [topPerson, setTopPerson] = useState('all');
  const [showAll, setShowAll] = useState(false);
  const people = useSummary(b, peopleRange), busiest = useSummary(b, historyRange), top = useSummary(b, topRange, topKind, topPerson === 'all' ? undefined : topPerson);
  const history = useStatistics(b, historyRange);
  const peopleOptions = [{value: 'all', label: t('next.dashboard.everyone')}, ...accounts.items.map(p => ({value: p.accountId, label: p.username}))];
  const ranked = people.value?.people ?? [], maxTime = Math.max(1, ...ranked.map(p => p.watchedMs));
  const firstTop = top.value?.titles.find(item => item.id), topBackdrop = firstTop?.id ? b.artwork(firstTop.id, 'backdrop') : undefined;
  return <>
    <Section title={t('next.dashboard.people')} actions={<Range value={peopleRange} values={summaryRanges} onChange={value => setPeopleRange(value as StatisticsRange)} />}>
      <ReadFeedback openHome={b.effects.openHome} state={people} />{people.value && <div className={styles.peopleCard}>
        <ol className={styles.peopleList}>{(showAll ? ranked : ranked.slice(0, 6)).map((p, index) => <li key={p.id ?? p.name} className={styles.personRow}><span>{index + 1}</span><span className={styles.avatar} style={accountAvatarStyle(p.name)} aria-hidden="true">{p.name.slice(0, 1)}</span><div className={styles.personName}><b>{p.name}</b></div><span>{t('next.dashboard.plays', {count: p.plays})}</span><div className={styles.barTrack}><span className={styles.peopleBar} style={{width: `${p.watchedMs / maxTime * 100}%`}} role="img" aria-label={`${p.name}: ${watchedDuration(p.watchedMs)}`}><i data-series="movies" style={{flexGrow: 1}} /></span></div><b>{watchedDuration(p.watchedMs)}</b></li>)}</ol>
        {!ranked.length && <p className={styles.empty}>{t('next.dashboard.nothing')}</p>}
        {ranked.length > 6 && <Button size="xs" variant="text" className={styles.showAll} onClick={() => setShowAll(!showAll)} aria-expanded={showAll}>{showAll ? t('next.dashboard.fewer') : t('next.dashboard.showAll', {count: ranked.length})}<Icon name={showAll ? 'i-up' : 'i-down'} size={16} /></Button>}
        <CountedFrom summary={people.value} />
      </div>}
    </Section>
    <Section title={t('next.dashboard.history')} extra={<Button size="xs" variant="text" onClick={b.effects.openHistory}>{t('next.dashboard.fullHistory')}<Icon name="i-right" size={16} /></Button>} actions={<><Select aria-label={t('next.dashboard.media')} value={media} onValueChange={setMedia} options={['all', ...STATISTICS_KINDS].map(kind => ({value: kind, label: mediaLabel(kind === 'all' ? 'all' : statisticsMedia[kind as keyof typeof statisticsMedia])}))} /><Range value={historyRange} values={historyRanges} onChange={value => setHistoryRange(value as StatisticsRange)} /></>}>
      <ReadFeedback openHome={b.effects.openHome} state={history} />{history.value && <PlaysOverTime statistics={history.value} media={media} />}
      <ReadFeedback openHome={b.effects.openHome} state={busiest} />{busiest.value && <Busiest summary={busiest.value} />}
    </Section>
    <Section title={t('next.dashboard.top')} actions={<><Segmented aria-label={t('next.dashboard.media')} value={topKind} onValueChange={value => setTopKind(value as TopKind)} items={(['movies', 'tv', 'music'] as const).map(kind => ({value: kind, label: mediaLabel(kind)}))} /><Select aria-label={t('next.dashboard.person')} value={topPerson} options={peopleOptions} onValueChange={setTopPerson} /><Range value={topRange} values={summaryRanges} onChange={value => setTopRange(value as StatisticsRange)} /></>}>
      <ReadFeedback openHome={b.effects.openHome} state={top} />{top.value && (top.value.titles.length ? <div className={styles.rankedShelf}>{topBackdrop && <div className={styles.shelfAmbient} aria-hidden="true"><Artwork source={topBackdrop} alt="" width="100%" height="100%" /></div>}<ol className={styles.shelf} data-square={topKind === 'music' || undefined}>{top.value.titles.map((item, index) => {
        const art = item.id ? b.artwork(item.id, topKind === 'music' ? 'square' : 'poster') : undefined;
        return <li key={item.id ?? item.name}><span className={styles.rank} aria-hidden="true">{index + 1}</span><div className={styles.rankedArt}>{art && <Artwork source={art} alt="" width="100%" height="100%" />}</div><h4>{item.name}</h4>{item.detail && <p>{item.detail}</p>}<p>{t('next.dashboard.plays', {count: item.plays})}</p></li>;
      })}</ol></div> : <p className={styles.empty}>{t('next.dashboard.nothing')}</p>)}
      <CountedFrom summary={top.value} />
    </Section>
  </>;
}

/** Time played over the range from the totals per day, stacked by kind, and the range's totals (rule 86). */
function PlaysOverTime({statistics: s, media}: {statistics: ServerStatistics; media: string}) {
  const kinds = STATISTICS_KINDS.filter(kind => (media === 'all' || media === kind) && s.points.some(p => p.playedMs[kind] > 0));
  const series = kinds.map(kind => ({key: statisticsMedia[kind], label: mediaLabel(statisticsMedia[kind]), points: s.points.map(p => ({t: Date.parse(p.start), v: hours(p.playedMs[kind])}))}));
  return <div className={styles.card}><h4>{t('next.dashboard.timePlayed')}</h4>
    {series.length ? <Chart stacked kind="played" interval={s} title={t('next.dashboard.timePlayed')} series={series} format={value => duration(value * 3_600_000)} /> : <p className={styles.historyEmpty}>{t('next.dashboard.nothing')}</p>}
    <div className={styles.legend}><small>{t('next.dashboard.timePlayed')} <b>{duration(s.totals.playedMs)}</b></small><small>{t('server.viewing.plays')} <b>{defaultI18n.number(s.totals.plays)}</b></small><small>{t('next.dashboard.dataSent')} <b>{defaultI18n.bytes(s.totals.bytes)}</b></small></div>
  </div>;
}

/** Plays by weekday and hour, counted from Play history (rule 13). */
export function Busiest({summary}: {summary: PlaySummary}) {
  const [cell, setCell] = useState<{day: number; hour: number; point: TipPoint} | undefined>();
  // Bounded per component: one table per locale, so moving the pointer across the grid
  // re-renders the chart without rebuilding a weekday or hour formatter per cell.
  const locale = defaultI18n.locale, labels = useMemo(() => heatMapLabels(locale), [locale]);
  const grid = summary.busiest, quiet = quietestHours(grid), heatMax = Math.max(1, ...grid.flat()), plays = (count: number) => t('next.dashboard.plays', {count});
  return <div className={styles.card}><div className={styles.heading}><h4>{t('next.dashboard.busiest')}</h4>{quiet !== undefined && <span className={styles.quiet}>{t('next.dashboard.quietest')} <b>{formatQuietHours(quiet)}</b></span>}</div>
    <div className={styles.heatScroll}><svg className={`${styles.chart} ${styles.heat}`} viewBox="0 0 912 203" role="group" aria-label={t('next.dashboard.busiest')}>
      {grid.map((day, d) => <g key={d}><text x="32" y={19 + d * 25} textAnchor="end">{labels.weekdayShort[d]}</text>{day.map((count, hour) => <rect tabIndex={0} role="button" key={hour} className={styles.heatCell} x={(44 + hour * 868 / 24).toFixed(1)} y={4 + d * 25} width={(868 / 24 - 3).toFixed(1)} height="22" rx="5"
        style={{'--heat-intensity': `${(.06 + .94 * Math.pow(count / heatMax, .8)) * 100}%`} as CSSProperties} aria-label={`${labels.weekdayLong[d]} ${labels.hour[hour]}: ${plays(count)}`}
        onFocus={event => {const r = event.currentTarget.getBoundingClientRect(); setCell({day: d, hour, point: {x: r.left + r.width / 2, y: r.bottom}});}} onBlur={() => setCell(undefined)}
        onPointerMove={event => setCell({day: d, hour, point: {x: event.clientX, y: event.clientY}})} onPointerLeave={() => setCell(undefined)} />)}</g>)}
      {[0, 3, 6, 9, 12, 15, 18, 21].map(h => <text x={(44 + h * 868 / 24).toFixed(1)} y="197" key={h}>{labels.hour[h]}</text>)}
      {quiet !== undefined && <rect className={styles.quietOutline} x={(44 + quiet * 868 / 24 - 2).toFixed(1)} y="1" width={(3 * 868 / 24 + 1).toFixed(1)} height="178" rx="8" />}
    </svg></div>
    <FloatingTip point={cell?.point}>{cell && <><b>{labels.weekdayLong[cell.day]} {labels.hour[cell.hour]}</b><span data-series="movies"><i />{t('next.dashboard.played')}<strong>{plays(grid[cell.day]![cell.hour]!)}</strong></span></>}</FloatingTip>
    <div className={styles.heatLegend}>{t('next.dashboard.quieter')}{[.08, .28, .5, .75, 1].map(intensity => <i key={intensity} style={{'--heat-intensity': `${intensity * 100}%`} as CSSProperties} />)}{t('next.dashboard.busier')}</div>
    <CountedFrom summary={summary} />
  </div>;
}
