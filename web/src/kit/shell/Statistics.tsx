import {useId, type ReactNode} from 'react';
import styles from './statistics.module.css';

export type StatTile = Readonly<{id: string; label: string; value: string; status?: boolean; samples?: readonly number[]; current?: number; ceiling?: number}>;
/** Samples are actual measurements; gaps do not turn into fabricated zeroes or noise. */
export function Sparkline({samples, current, ceiling, label}: {samples: readonly number[]; current?: number; ceiling?: number; label?: string}) {
  const fill = useId().replaceAll(':', '');
  const values = samples.filter(Number.isFinite);
  if (!values.length) return null;
  const maximum = Math.max(1, ceiling ?? 0, ...values) * 1.1;
  let smooth = values;
  for (let pass = 0; pass < 2; pass++) smooth = smooth.map((value, index) => (smooth[Math.max(0, index - 1)]! + value * 2 + smooth[Math.min(smooth.length - 1, index + 1)]!) / 4);
  smooth[smooth.length - 1] = current !== undefined && Number.isFinite(current) ? current : values.at(-1)!;
  const d = smooth.map((value, index) => `${index ? 'L' : 'M'}${smooth.length === 1 ? 0 : index / (smooth.length - 1) * 100},${44 - Math.max(0, value) / maximum * 40}`).join(' ');
  return <svg className={styles.sparkline} viewBox="0 0 100 46" preserveAspectRatio="none" role={label ? 'img' : undefined} aria-label={label} aria-hidden={label ? undefined : true}>
    <defs><linearGradient id={fill} x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor="var(--color-accent)" stopOpacity=".24" /><stop offset="1" stopColor="var(--color-accent)" stopOpacity="0" /></linearGradient></defs>
    <path d={`${d} L100,46 L0,46 Z`} fill={`url(#${fill})`} /><path d={d} fill="none" stroke="var(--color-accent-strong)" strokeWidth="1.6" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
  </svg>;
}
export function StatTiles({items, variant = 'statistics'}: {items: readonly StatTile[]; variant?: 'statistics' | 'health' | 'segments'}) {
  return <dl className={styles.tiles} data-variant={variant}>{items.map(item => <div key={item.id} data-status={item.status || undefined}><div className={styles.heading}><dt>{item.label}</dt><dd>{item.value}</dd></div>{variant === 'health' && item.samples && <Sparkline samples={item.samples} current={item.current} ceiling={item.ceiling} />}</div>)}</dl>;
}
export function BarList({title, items, maximum, variant = 'titles'}: {title?: string; items: readonly {id: string; label: ReactNode; value: number; valueText: string}[]; maximum: number; variant?: 'titles' | 'people'}) {
  return <section className={styles.barList} data-variant={variant}>{title && <h5>{title}</h5>}<div>{items.map(item => <div className={styles.barRow} key={item.id}><span>{item.label}</span><span className={styles.track} aria-hidden="true"><span style={{transform: `scaleX(${Number.isFinite(item.value) && maximum > 0 ? Math.max(0, Math.min(1, item.value / maximum)) : 0})`}} /></span><span className={styles.value}>{item.valueText}</span></div>)}</div></section>;
}
