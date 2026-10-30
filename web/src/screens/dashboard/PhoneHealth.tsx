import {useId, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {MetricName, TelemetryReading} from '@core/telemetry.ts';
import {formatBytes} from '@core/presentation/content.ts';
import {Dialog} from '../../kit/overlays';
import {healthDetail, healthPlot, healthTiles} from './health-presentation';
import styles from './phone-health.module.css';

const t = defaultI18n.t;
export type HealthRange = '10m' | '1h' | '24h';
const ranges = [
  {value: '10m', label: 'web.telemetry.window10m'},
  {value: '1h', label: 'web.telemetry.window1h'},
  {value: '24h', label: 'web.telemetry.window24h'},
] as const;

/** Presentation only; the caller owns telemetry, its lifetime and range. */
export function PhoneHealth({reading, loading, failed, range, onRangeChange, onRetry}: Readonly<{
  reading?: TelemetryReading; loading: boolean; failed: boolean; range: HealthRange;
  onRangeChange: (range: HealthRange) => void; onRetry: () => void;
}>) {
  const gradient = useId();
  const [selected, setSelected] = useState<{metric: MetricName; label: string}>();
  const opener = useRef<HTMLButtonElement | null>(null);
  const current = reading?.window === range ? reading : undefined;
  const tiles = healthTiles(current);
  const from = current ? Date.parse(current.from) : NaN, until = current ? Date.parse(current.until) : NaN;
  const selectedTile = tiles.find(tile => tile.metric === selected?.metric);
  const selectedLabel = selected?.label ?? t('settings.server.health');
  const percent = selected?.metric !== 'netOut' && selected?.metric !== 'diskRead';
  const detail = selectedTile?.history === 'samples' ? healthDetail(selectedTile.points, from, until, percent) : undefined;
  const number = (value: number) => percent ? `${Math.round(value)}%` : selected?.metric === 'netOut'
    ? t('next.dashboard.mbps', {value: new Intl.NumberFormat(defaultI18n.locale, {maximumSignificantDigits: 4}).format(value)}) : `${formatBytes(value)}/s`;
  const zone = current?.timeZone;
  const offset = (value: number) => new Intl.DateTimeFormat(defaultI18n.locale, {timeZone: zone, timeZoneName: 'shortOffset'}).formatToParts(value).find(part => part.type === 'timeZoneName')?.value;
  const extendedTime = !!current && (range === '24h' || offset(from) !== offset(until));
  const time = (value: number) => new Intl.DateTimeFormat(defaultI18n.locale, {
    timeZone: zone, hour: 'numeric', minute: '2-digit',
    ...(extendedTime ? {month: 'short', day: 'numeric', year: 'numeric', timeZoneName: 'shortOffset'} : {}),
  }).format(value);
  const shortHistory = tiles.some(tile => tile.history === 'samples' && tile.points.length < 2);
  return <section className={styles.section} aria-labelledby={`${gradient}-title`} aria-busy={loading}>
    <div className={styles.heading}><h2 id={`${gradient}-title`}>{t('settings.server.health')}</h2></div>
    <div className={styles.segment} role="group" aria-label={t('web.telemetry.period')}>
      {ranges.map(item => <button key={item.value} type="button" aria-pressed={range === item.value} onClick={() => onRangeChange(item.value)}>{t(item.label)}</button>)}
    </div>
    {failed && <div className={styles.feedback} role="alert">{t('next.states.pageFailed')} <button type="button" onClick={onRetry}>{t('next.dashboard.retry')}</button></div>}
    {loading && !current && <div className={styles.feedback} role="status">{t('next.settings.loading')}</div>}
    {current && (tiles.length ? <div className={styles.tiles}>{tiles.map(tile => {
      const plot = healthPlot(tile.points, from, until);
      return <button type="button" className={styles.tile} key={tile.metric} onClick={event => {opener.current = event.currentTarget; setSelected({metric: tile.metric, label: t(tile.label)});}}>
        <div className={styles.key}><span>{t(tile.label)}</span><b>{tile.value}</b>{tile.limited && <small>{tile.details.length ? tile.details.join(' · ') : t('next.remoteSources.state.limited')}</small>}</div>
        <div className={`${styles.spark} ${plot ? '' : styles.empty}`}>
          {plot && <svg viewBox="0 0 100 46" preserveAspectRatio="none" aria-hidden="true">
            <defs><linearGradient id={`${gradient}-${tile.metric}`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--color-accent)" stopOpacity=".24"/><stop offset="1" stopColor="var(--color-accent)" stopOpacity="0"/></linearGradient></defs>
            <path d={plot.area} fill={`url(#${gradient}-${tile.metric})`}/><path d={plot.line} fill="none" stroke="var(--color-accent-strong)" strokeWidth="1.6" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
          </svg>}
          {plot && <i className={styles.dot} style={{left: `${plot.endX}%`, top: `${plot.endY / 46 * 100}%`}} aria-hidden="true"/>}
        </div>
      </button>;
    })}{shortHistory && <div className={styles.note}>{t('next.dashboard.healthLater')}</div>}</div> : <div className={styles.feedback}>{t('server.health.loadNotMeasured')}</div>)}
    <Dialog open={selected !== undefined} onOpenChange={open => {if (!open) setSelected(undefined);}} title={selectedLabel} returnFocusRef={opener} headerClassName={styles.detailHeader} bodyClassName={styles.detailBody}>
      {selectedTile && <div className={styles.big}><b>{selectedTile.value}</b>{range === '10m' && <span>{t('next.dashboard.nowValue')}</span>}</div>}
      {selectedTile?.limited && <div className={styles.detailMessage}>{selectedTile.details.length ? selectedTile.details.join(' · ') : t('next.remoteSources.state.limited')}</div>}
      <div className={`${styles.segment} ${styles.detailSegment}`} role="group" aria-label={t('web.telemetry.period')}>
        {ranges.map(item => <button key={item.value} type="button" aria-pressed={range === item.value} onClick={() => onRangeChange(item.value)}>{t(item.label)}</button>)}
      </div>
      {failed ? <div className={`${styles.detailMessage} ${styles.feedback}`} role="alert">{t('next.states.pageFailed')} <button type="button" onClick={onRetry}>{t('next.dashboard.retry')}</button></div>
        : loading && !current ? <div className={styles.detailMessage} role="status">{t('next.settings.loading')}</div>
        : !selectedTile || selectedTile.history === 'gap' ? <div className={styles.detailMessage}>{t('next.kit.browse.unavailable')}</div>
        : !detail ? <div className={styles.detailMessage}>{selectedTile.points.length < 2 ? t('next.dashboard.healthLater') : t('next.kit.browse.unavailable')}</div>
        : <>
          {selectedTile.points.some(point => point.status === 'limited') && <div className={styles.detailMessage}>{[...new Set(selectedTile.points.filter(point => point.status === 'limited').map(point => point.detail || t('next.remoteSources.state.limited')))].join(' · ')}</div>}
          <div className={styles.chart}>
            {(detail.top === 0 ? [0] : [1, .5, 0]).map(f => <div className={`${styles.gridline} ${f === 0 ? styles.baseline : ''}`} style={{top: `${(1 - f) * 100}%`}} key={f}><span>{number(detail.top * f)}</span></div>)}
            <div className={styles.chartPlot}><svg viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
              <defs><linearGradient id={`${gradient}-detail`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--color-accent)" stopOpacity=".24"/><stop offset="1" stopColor="var(--color-accent)" stopOpacity="0"/></linearGradient></defs>
              <path d={detail.area} fill={`url(#${gradient}-detail)`}/><path d={detail.line} fill="none" stroke="var(--color-accent-strong)" strokeWidth="2" vectorEffect="non-scaling-stroke" strokeLinejoin="round"/>
            </svg><i className={styles.detailDot} style={{left: `${detail.endX}%`, top: `${detail.endY}%`}} aria-hidden="true"/></div>
          </div>
          <div className={styles.axis}><span>{time(from)}</span><span>{time((from + until) / 2)}</span><span>{time(until)}</span></div>
          <div className={styles.stats}><div><small>{t('next.dashboard.lowest')}</small><b>{number(detail.low)}</b></div><div><small>{t('next.dashboard.averageValue')}</small><b>{number(detail.average)}</b></div><div><small>{t('next.dashboard.highest')}</small><b>{number(detail.high)}</b><em>{t('next.dashboard.highestAt', {time: time(detail.highAt)})}</em></div></div>
        </>}
    </Dialog>
  </section>;
}
