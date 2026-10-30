import {useId, useState, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import type {ReportInterval} from '@core/report-context.ts';
import type {TelemetryPoint} from '@core/telemetry.ts';
import {formatLoadAxis, formatReportBucketTitle, loadScale, type DashboardLoadKind} from './format';
import {chartPaths, loadAxisTicks} from './model';
import styles from './dashboard.module.css';
export type ChartSeries = Readonly<{key: string; label: string; points: readonly TelemetryPoint[]}>;
const t = defaultI18n.t;
/** Keyboard and pointer inspect the same actual sample. Missing samples stay gaps. */
export function Chart({title, series, format, formatAverage = format, step = false, compact = false, kind = 'bandwidth', interval}: {interval: ReportInterval; kind?: DashboardLoadKind; title: string; series: readonly ChartSeries[]; format: (value: number) => string; formatAverage?: (value: number) => string; step?: boolean; compact?: boolean}) {
  const id = useId().replace(/:/g, ''), [selected, select] = useState<number | undefined>();
  const stamps = [...new Set(series.flatMap(s => s.points.map(p => p.t)))].sort((a, b) => a - b);
  if (!stamps.length) return null;
  const starts = interval.buckets.map(b => Date.parse(b.start));
  const from = Date.parse(interval.from), lastStart = starts.at(-1), until = interval.period !== 'live' && lastStart !== undefined && lastStart > from ? lastStart : Date.parse(interval.until);
  const expected = interval.period === 'live' ? undefined : starts;
  const actualMax = Math.max(0, ...series.flatMap(s => s.points.map(p => p.v)));
  const scale = loadScale(kind, actualMax), max = compact ? kind === 'memory' ? Math.max(60, actualMax * 1.15) : kind === 'conversions' ? Math.max(3.2, actualMax) : Math.max(1, actualMax * (kind === 'bandwidth' ? 1.12 : 1.15)) : scale.top;
  const geometry = compact ? {left: 3, top: 3, width: 191, height: 34} : {left: kind === 'bandwidth' ? 64 : 40, top: 8, width: kind === 'bandwidth' ? 840 : 864, height: 216};
  const x = (stamp: number) => geometry.left + (stamp - from) / (until - from) * geometry.width;
  const y = (value: number) => geometry.top + geometry.height - value / max * geometry.height;
  const active = selected === undefined ? undefined : stamps[Math.min(stamps.length - 1, selected)];

  return <div className={compact ? styles.spark : styles.plot} tabIndex={compact ? undefined : 0} role={compact ? undefined : 'group'} aria-label={compact ? undefined : title} aria-describedby={compact ? undefined : id}
    onPointerMove={compact ? undefined : event => {const rect = event.currentTarget.getBoundingClientRect(); select(Math.max(0, Math.min(stamps.length - 1, stamps.reduce((best, stamp, i) => Math.abs(stamp - (from + ((event.clientX - rect.left) / rect.width * 912 - geometry.left) / geometry.width * (until - from))) < Math.abs(stamps[best] - (from + ((event.clientX - rect.left) / rect.width * 912 - geometry.left) / geometry.width * (until - from))) ? i : best, 0))));}}
    onPointerLeave={() => select(undefined)} onBlur={() => select(undefined)} onKeyDown={event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault(); select(old => event.key === 'Home' ? 0 : event.key === 'End' ? stamps.length - 1 : Math.max(0, Math.min(stamps.length - 1, (old ?? (event.key === 'ArrowLeft' ? stamps.length : -1)) + (event.key === 'ArrowLeft' ? -1 : 1))));
    }}>
    <svg viewBox={compact ? '0 0 197 40' : '0 0 912 250'} role="img" aria-label={title} className={styles.chart}>
      <defs>{series.map(s => <linearGradient data-series={s.key} key={s.key} id={`${id}-${s.key}`} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="currentColor" stopOpacity={compact ? .22 : .16} /><stop offset="1" stopColor="currentColor" stopOpacity="0" /></linearGradient>)}</defs>
      {!compact && scale.ticks.map(value => <g key={value}><path className={styles.gridline} d={`M${geometry.left},${Math.round(y(value)) + .5}H904`} /><text x={geometry.left - 10} y={Math.round(y(value)) + 4.5} textAnchor="end">{formatLoadAxis(value, kind)}</text></g>)}
      {series.map((s, si) => <g key={s.key} data-series={s.key} className={styles.series}>{chartPaths(s.points, from, until, max, step, geometry, expected).map((path, i) => {
        const first = /^M([\d.-]+),/.exec(path)?.[1], last = /(?:C[^C]* |H|L)([\d.-]+)[,V][\d.-]+$/.exec(path)?.[1] ?? first;
        return <g key={i}>{/^M[\d.-]+,[\d.-]+$/.test(path) && <circle cx={Number(path.slice(1).split(',')[0])} cy={Number(path.slice(1).split(',')[1])} r={3} />}{(si === 0 || !compact && kind === 'bandwidth') && <path className={styles.lineFill} style={{fill: `url(#${id}-${s.key})`}} d={`${path}L${last},${compact ? 40 : 224}L${first},${compact ? 40 : 224}Z`} />}<path d={path} /></g>;
      })}{(compact || s.points.length === 1 || active !== undefined) && (() => {const point = active === undefined ? s.points.at(-1) : s.points.find(p => p.t === active); return point && <circle cx={x(point.t)} cy={y(point.v)} r={active === undefined ? 3 : 4.5} />;})()}</g>)}
      {!compact && loadAxisTicks(interval, from, until, geometry.width).map(tick => <text key={tick.f} x={(geometry.left + geometry.width * tick.f).toFixed(1)} y="244" textAnchor={tick.f < .02 ? 'start' : tick.f > .98 ? 'end' : 'middle'}>{tick.text}</text>)}
      {active !== undefined && <path className={styles.guide} d={`M${x(active)},8V224`} />}
    </svg>
    {!compact && <><div id={id} className={styles.chartTip} style={active === undefined ? undefined : {left: `calc(${(x(active) / 912 * 100).toFixed(2)}% + ${x(active) / 912 > .58 ? -14 : 14}px)`} as CSSProperties} data-flip={active !== undefined && x(active) / 912 > .58 || undefined} aria-live="polite">{active !== undefined && <><b>{formatReportBucketTitle(interval, active)}</b>{series.map(s => {const point = s.points.find(p => p.t === active); return point && <span key={s.key} data-series={s.key}><i />{s.label}<strong>{format(point.v)}</strong></span>;})}</>}</div>
      <div className={styles.legend}>{series.map(s => <span key={s.key} data-series={s.key}><i />{s.label}</span>)}<small className={styles.averages}>{t('next.dashboard.average')}{series.filter(s => s.points.length).map(s => <b key={s.key} data-series={s.key}>{s.label} {formatAverage(s.points.reduce((sum, p) => sum + p.v, 0) / s.points.length)}</b>)}</small></div></>}
  </div>;
}
