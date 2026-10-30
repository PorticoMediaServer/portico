import {Artwork} from '../../kit/artwork/Artwork';
import {accountAvatarStyle} from '../../kit/settings/account-avatar';
import {useMemo, useState, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import type {DashboardHistory, DashboardTopKind} from '@core/admin/dashboard.ts';
import type {PlayHistoryPeriod} from '@core/play-history.ts';
import {Select, Button, Segmented} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import type {DashboardBindings} from './bindings';
import type {DashboardAccounts} from './use-read';
import type {DashboardReportController} from './use-report';
import {fmtDur, fmtHrs, formatReportBucketTitle, formatQuietHours, heatMapLabels, niceScale} from './format';
import {curveSegments, historyAxisTicks, plotLine, hours, mediaKinds, quietestHours} from './model';
import {FloatingTip, type TipPoint} from './FloatingTip';
import {mediaLabel, Range, ReadFeedback, Section, t} from './shared';
import styles from './dashboard.module.css';
const historyKinds = mediaKinds;
const ranges = ['7d', '30d', '90d', 'all'] as const;
const duration = (ms: number) => fmtHrs(hours(ms));
const watchedDuration = (ms: number) => fmtDur(hours(ms));
export function Analytics({bindings: b, report, accounts}: {bindings: DashboardBindings; report: DashboardReportController; accounts: DashboardAccounts}) {
  const [showAll, setShowAll] = useState(false), [personTip, showPersonTip] = useState<{person: NonNullable<DashboardReportController['state']['value']>['people']['items'][number]; point: TipPoint} | undefined>();
  const {query, change, state} = report;
  const peopleRange = query.peoplePeriod, historyRange = query.period, media = query.media ?? 'all', person = query.accountId ?? 'all', topKind = query.topKind, topRange = query.topPeriod, topPerson = query.topAccountId ?? 'all';
  const setPeopleRange = (peoplePeriod: PlayHistoryPeriod) => change({peoplePeriod, peopleCursor: undefined});
  const setHistoryRange = (period: PlayHistoryPeriod) => change({period});
  const setMedia = (value: string) => change({media: value === 'all' ? undefined : value});
  const setPerson = (value: string) => change({accountId: value === 'all' ? undefined : value});
  const setTopKind = (topKind: DashboardTopKind) => change({topKind});
  const setTopRange = (topPeriod: PlayHistoryPeriod) => change({topPeriod});
  const setTopPerson = (value: string) => change({topAccountId: value === 'all' ? undefined : value});
  const people = {...state, value: state.value?.people}, history = {...state, value: state.value?.history}, top = {...state, value: state.value?.topPlayed};
  const peopleOptions = [{value: 'all', label: t('next.dashboard.everyone')}, ...accounts.items.map(p => ({value: p.accountId, label: p.username}))];
  const firstTop = top.value?.items[0], topBackdrop = firstTop ? b.artwork(firstTop.itemId, 'backdrop', firstTop.backdropRevision) : undefined;
  const peoplePage = people.value?.items ?? [], maxTime = Math.max(1, ...peoplePage.map(p => p.watchedMs));
  return <>
    <Section title={t('next.dashboard.people')} actions={<Range value={peopleRange} values={ranges} onChange={value => setPeopleRange(value as PlayHistoryPeriod)} />}>
      <ReadFeedback openHome={b.effects.openHome} state={people} />{people.value && <div className={styles.peopleCard}><div className={styles.legend}>{mediaKinds.map(kind => <span key={kind} data-series={kind}><i />{mediaLabel(kind)}</span>)}<small>{t('next.dashboard.timeWatched')}</small></div>
        <ol className={styles.peopleList}>{(showAll ? peoplePage : peoplePage.slice(0, 6)).map((p, index) => <li key={p.accountId} className={styles.personRow}><span>{index + 1}</span><span className={styles.avatar} style={accountAvatarStyle(p.username)} aria-hidden="true">{p.username.slice(0, 1)}</span><div className={styles.personName}><b>{p.username}</b><small>{p.profiles.filter(name => name !== p.username).join(', ')}</small></div><span>{t('next.dashboard.plays', {count: p.plays})}</span><div className={styles.barTrack}><span tabIndex={0} onPointerMove={event => showPersonTip({person: p, point: {x: event.clientX, y: event.clientY}})} onPointerLeave={() => showPersonTip(undefined)} onFocus={event => {const r = event.currentTarget.getBoundingClientRect(); showPersonTip({person: p, point: {x: r.left + r.width / 2, y: r.bottom}});}} onBlur={() => showPersonTip(undefined)} className={styles.peopleBar} style={{width: `${p.kinds.filter(k => mediaKinds.some(kind => kind === k.kind)).reduce((sum, k) => sum + k.watchedMs, 0) / maxTime * 100}%`}} role="img" aria-label={`${p.username}: ${watchedDuration(p.watchedMs)}`}>{mediaKinds.flatMap(kind => p.kinds.filter(k => k.kind === kind && k.watchedMs / p.watchedMs > .004)).map(k => <i key={k.kind} data-series={k.kind} style={{flexGrow: k.watchedMs}} title={`${mediaLabel(k.kind)}: ${watchedDuration(k.watchedMs)}`} />)}</span></div><b>{watchedDuration(p.watchedMs)}</b></li>)}</ol>
        {!peoplePage.length && <p className={styles.empty}>{t('next.dashboard.nothing')}</p>}
        {peoplePage.length > 6 && !people.value.nextCursor && <Button size="xs" variant="text" className={styles.showAll} onClick={() => setShowAll(!showAll)} aria-expanded={showAll}>{showAll ? t('next.dashboard.fewer') : t('next.dashboard.showAll', {count: peoplePage.length})}<Icon name={showAll ? 'i-up' : 'i-down'} size={16} /></Button>}
      </div>}
    </Section>
    <FloatingTip point={personTip?.point}>{personTip && <><b>{personTip.person.username}{personTip.person.profiles.filter(name => name !== personTip.person.username).join(', ') && ` ${personTip.person.profiles.filter(name => name !== personTip.person.username).join(', ')}`}</b>{mediaKinds.map(kind => {const fact = personTip.person.kinds.find(k => k.kind === kind); return fact && fact.watchedMs > 0 && <span key={kind} data-series={kind}><i />{mediaLabel(kind)}<strong>{watchedDuration(fact.watchedMs)}</strong></span>;})}<span className={styles.tipTotal}>{t('next.dashboard.total')}<strong>{watchedDuration(personTip.person.watchedMs)}</strong></span></>}</FloatingTip>
    <Section title={t('next.dashboard.history')} extra={<Button size="xs" variant="text" onClick={b.effects.openHistory}>{t('next.dashboard.fullHistory')}<Icon name="i-right" size={16} /></Button>} actions={<><Select aria-label={t('next.dashboard.media')} value={media} onValueChange={setMedia} options={['all', ...mediaKinds].map(kind => ({value: kind, label: mediaLabel(kind)}))} /><Select aria-label={t('next.dashboard.person')} value={person} options={peopleOptions} onValueChange={setPerson} /><Range value={historyRange} values={['7d', '30d', '90d', '1y']} onChange={value => setHistoryRange(value as PlayHistoryPeriod)} /></>}>
      <ReadFeedback openHome={b.effects.openHome} state={history} />{history.value && <HistoryCharts history={history.value} media={media} />}
    </Section>
    <Section title={t('next.dashboard.top')} actions={<><Segmented aria-label={t('next.dashboard.media')} value={topKind} onValueChange={value => setTopKind(value as DashboardTopKind)} items={['movies', 'tv', 'music'].map(kind => ({value: kind, label: mediaLabel(kind)}))} /><Select aria-label={t('next.dashboard.person')} value={topPerson} options={peopleOptions} onValueChange={setTopPerson} /><Range value={topRange} values={ranges} onChange={value => setTopRange(value as PlayHistoryPeriod)} /></>}>
      <ReadFeedback openHome={b.effects.openHome} state={top} />{top.value && (top.value.items.length ? <div className={styles.rankedShelf}>{topBackdrop && <div className={styles.shelfAmbient} aria-hidden="true"><Artwork source={topBackdrop} alt="" width="100%" height="100%" /></div>}<ol className={styles.shelf} data-square={topKind === 'music' || undefined}>{top.value.items.map((item, index) => {
        const art = b.artwork(item.itemId, topKind === 'music' ? 'square' : 'poster', item.posterRevision);
        return <li key={item.itemId}><span className={styles.rank} aria-hidden="true">{index + 1}</span><div className={styles.rankedArt}>{art && <Artwork source={art} alt="" width="100%" height="100%" />}</div><h4>{item.title}</h4><p>{t('next.dashboard.plays', {count: item.plays})}</p>{topPerson === 'all' && <p>{t('next.dashboard.peopleCount', {count: item.people})}</p>}</li>;
      })}</ol></div> : <p className={styles.empty}>{t('next.dashboard.nothing')}</p>)}
    </Section>
  </>;
}
function HistoryCharts({history: h, media}: {history: DashboardHistory; media: string}) {
  const [focus, setFocus] = useState<number | undefined>(), [cell, setCell] = useState<{day: number; hour: number; point: TipPoint} | undefined>();
  // Bounded per component: one table per locale, so moving the pointer across the grid
  // re-renders the charts without rebuilding a weekday or hour formatter per cell.
  const locale = defaultI18n.locale, labels = useMemo(() => heatMapLabels(locale), [locale]);
  const total = h.buckets.reduce((s, b) => s + b.kinds.reduce((sum, k) => sum + k.watchedMs, 0), 0), scale = niceScale(Math.max(.5, ...h.buckets.map(b => hours(b.kinds.reduce((sum, k) => sum + k.watchedMs, 0)))) * 1.06, 4), max = scale.top * 3_600_000;
  const kinds = historyKinds.filter(kind => media === 'all' || media === kind);
  const quiet = quietestHours(h.busiestTimes), heatMax = Math.max(1, ...h.busiestTimes.flat());
  const bucketTitle = (bucket: DashboardHistory['buckets'][number]) => formatReportBucketTitle(h, Date.parse(bucket.start), true);
  return <><div className={styles.card}><h4>{t('next.dashboard.timePlayed')}</h4>{total ? <div className={styles.plot} tabIndex={0} role="group" aria-label={t('next.dashboard.timePlayed')}
    onBlur={() => setFocus(undefined)} onPointerLeave={() => setFocus(undefined)} onPointerMove={event => {const rect = event.currentTarget.getBoundingClientRect(); setFocus(Math.max(0, Math.min(h.buckets.length - 1, Math.round(((event.clientX - rect.left) / rect.width * 912 - 50) / 854 * (h.buckets.length - 1)))));}}
    onKeyDown={event => {if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {event.preventDefault(); setFocus(old => event.key === 'Home' ? 0 : event.key === 'End' ? h.buckets.length - 1 : Math.max(0, Math.min(h.buckets.length - 1, (old ?? 0) + (event.key === 'ArrowLeft' ? -1 : 1))));}}}>
    <svg className={styles.chart} viewBox="0 0 912 240" role="img" aria-label={t('next.dashboard.timePlayed')}>
      {scale.ticks.map(value => <g key={value}><path className={styles.gridline} d={`M50,${Math.round(214 - value / scale.top * 206) + .5}H904`} /><text x="40" y={Math.round(214 - value / scale.top * 206) + 4.5} textAnchor="end">{value === 0 ? '0' : t('next.dashboard.hours', {value: String(value)})}</text></g>)}
      {kinds.map((kind, ki) => {
        const x = (i: number) => 50 + (h.buckets.length === 1 ? .5 : i / (h.buckets.length - 1)) * 854;
        const y = (i: number, last: number) => 214 - h.buckets[i].kinds.filter(k => kinds.slice(0, last).includes(k.kind as typeof kinds[number])).reduce((sum, k) => sum + k.watchedMs, 0) / max * 206;
        const hi = h.buckets.map((_, i): [number, number] => [x(i), y(i, ki + 1)]), lo = h.buckets.map((_, i): [number, number] => [x(i), y(i, ki)]).reverse();
        return h.buckets.length > 1 && <g key={kind} data-series={kind}><path className={styles.area} d={`${plotLine(hi)}L${lo[0][0].toFixed(1)},${lo[0][1].toFixed(1)}${curveSegments(lo)}Z`} /><path className={styles.areaLine} d={plotLine(hi)} /></g>;
      })}
      {historyAxisTicks(h).map(tick => <text key={tick.f} x={(50 + 854 * tick.f).toFixed(1)} y="234" textAnchor={tick.f < .02 ? 'start' : tick.f > .98 ? 'end' : 'middle'}>{tick.text}</text>)}
      {focus !== undefined && <path className={styles.guide} d={`M${50 + (h.buckets.length === 1 ? .5 : focus / (h.buckets.length - 1)) * 854},8V214`} />}
    </svg>
    <div className={styles.chartTip} style={focus === undefined ? undefined : {left: `calc(${((50 + (h.buckets.length === 1 ? .5 : focus / (h.buckets.length - 1)) * 854) / 912 * 100).toFixed(2)}% + ${focus / Math.max(1, h.buckets.length - 1) > .58 ? -14 : 14}px)`}} data-flip={focus !== undefined && focus / Math.max(1, h.buckets.length - 1) > .58 || undefined} aria-live="polite">{focus !== undefined && h.buckets[focus] && <><b>{bucketTitle(h.buckets[focus])}</b>{[...kinds].reverse().map(kind => <span key={kind} data-series={kind}><i />{mediaLabel(kind)}<strong>{duration(h.buckets[focus].kinds.find(k => k.kind === kind)?.watchedMs ?? 0)}</strong></span>)}{kinds.length > 1 && <span className={styles.tipTotal}>{t('next.dashboard.total')}<strong>{duration(h.buckets[focus].kinds.reduce((sum, k) => sum + k.watchedMs, 0))}</strong></span>}</>}</div>
    </div> : <p className={styles.historyEmpty}>{t('next.dashboard.nothing')}</p>}
    <div className={styles.legend}>{kinds.map(kind => <span key={kind} data-series={kind}><i />{mediaLabel(kind)}<b>{duration(h.buckets.reduce((sum, b) => sum + (b.kinds.find(k => k.kind === kind)?.watchedMs ?? 0), 0))}</b></span>)}<small>{t('next.dashboard.total')} <b>{duration(total)}</b></small></div>
    </div>
    <div className={styles.card}><div className={styles.heading}><h4>{t('next.dashboard.busiest')}</h4>{quiet !== undefined && <span className={styles.quiet}>{t('next.dashboard.quietest')} <b>{formatQuietHours(quiet)}</b></span>}</div>
      <div className={styles.heatScroll}><svg className={`${styles.chart} ${styles.heat}`} viewBox="0 0 912 203" role="group" aria-label={t('next.dashboard.busiest')}>
        {h.busiestTimes.map((day, d) => <g key={d}><text x="32" y={19 + d * 25} textAnchor="end">{labels.weekdayShort[d]}</text>{day.map((ms, hour) => <rect tabIndex={0} role="button" key={hour} className={styles.heatCell} x={(44 + hour * 868 / 24).toFixed(1)} y={4 + d * 25} width={(868 / 24 - 3).toFixed(1)} height="22" rx="5" style={{'--heat-intensity': `${(.06 + .94 * Math.pow(ms / heatMax, .8)) * 100}%`} as CSSProperties} aria-label={`${labels.weekdayLong[d]} ${labels.hour[hour]}: ${duration(ms)}`} onFocus={event => {const r = event.currentTarget.getBoundingClientRect(); setCell({day: d, hour, point: {x: r.left + r.width / 2, y: r.bottom}});}} onBlur={() => setCell(undefined)} onPointerMove={event => setCell({day: d, hour, point: {x: event.clientX, y: event.clientY}})} onPointerLeave={() => setCell(undefined)} />)}</g>)}
        {[0, 3, 6, 9, 12, 15, 18, 21].map(h => <text x={(44 + h * 868 / 24).toFixed(1)} y="197" key={h}>{labels.hour[h]}</text>)}
        {quiet !== undefined && <rect className={styles.quietOutline} x={(44 + quiet * 868 / 24 - 2).toFixed(1)} y="1" width={(3 * 868 / 24 + 1).toFixed(1)} height="178" rx="8" />}
      </svg></div>
      <FloatingTip point={cell?.point}>{cell && <><b>{labels.weekdayLong[cell.day]} {labels.hour[cell.hour]}</b><span data-series="movies"><i />{t('next.dashboard.played')}<strong>{duration(h.busiestTimes[cell.day][cell.hour])}</strong></span></>}</FloatingTip>
      <div className={styles.heatLegend}>{t('next.dashboard.quieter')}{[.08, .28, .5, .75, 1].map(intensity => <i key={intensity} style={{'--heat-intensity': `${intensity * 100}%`} as CSSProperties} />)}{t('next.dashboard.busier')}</div>
    </div></>;
}
