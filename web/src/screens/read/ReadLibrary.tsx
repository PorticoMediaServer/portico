import {useId, type ReactNode} from 'react';
import type {FactPart} from '@portico/client-core/kit-model';
import {Icon} from '../../kit/Icon';
import {Shelf, type ShelfProps, type LayoutPlatform} from '../../kit/layout';
import {CoverCard, ReadResumeCard, SeriesTile} from '../../kit/cards';
import type {CoverCardProps, ResumeCardProps, SeriesTileProps} from '../../kit/cards';
import styles from '../../kit/reader/library.module.css';

export type ReadPivot = Readonly<{id: string; label: string}>;
export type ReadLibraryHeaderProps = Readonly<{
  name: string; counts: readonly FactPart[]; pivots: readonly ReadPivot[]; selectedPivot: string;
  onPivot: (id: string) => void; onBack: () => void; onSwitchLibrary: (anchor: HTMLButtonElement) => void;
  onMore: (anchor: HTMLButtonElement) => void; switchExpanded?: boolean; moreExpanded?: boolean;
  /** Verbatim frame copy, localized by assembly. No label is inferred from library kind. */
  copy: Readonly<{libraries: string; switchLibrary: string; options: string}>;
  /** Actual library artwork/palette, bounded by the artwork pipeline. Never a design fixture. */
  wash?: ReactNode;
}>;

/** desktop-read 1/1b/2/2c; phone-read 1/2; tablet-read 1/1b. Shell stays Settings-owned. */
export function ReadLibraryHeader({name, counts, pivots, selectedPivot, onPivot, onBack, onSwitchLibrary, onMore, switchExpanded, moreExpanded, copy, wash}: ReadLibraryHeaderProps) {
  const id = useId();
  return <div className={styles.heading}>
    {wash && <div className={styles.wash} aria-hidden="true">{wash}</div>}
    <header className={styles.header}>
      <button type="button" className={styles.back} onClick={onBack}><Icon name="i-left" size={20} />{copy.libraries}</button>
      <div className={styles.identity}><h1>{name}<button type="button" className={styles.switch} aria-label={copy.switchLibrary} aria-haspopup="dialog" aria-expanded={switchExpanded}
        onClick={event => onSwitchLibrary(event.currentTarget)}><Icon name="i-down" size={20} /></button></h1>
        {counts.length > 0 && <p className={styles.counts}>{counts.map(part => <span key={part.id} data-fact={part.id}>{part.text}</span>)}</p>}
      </div>
      <button type="button" className={styles.more} aria-label={copy.options} aria-haspopup="menu" aria-expanded={moreExpanded} onClick={event => onMore(event.currentTarget)}><Icon name="i-more" size={22} /></button>
    </header>
    <div className={styles.pivots} role="tablist" aria-label={name} onKeyDown={event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')), current = buttons.indexOf(event.target as HTMLButtonElement);
      if (current < 0 || buttons.length === 0) return;
      event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length;
      buttons[next]?.focus();
      const target = pivots[next]; if (target) onPivot(target.id);
    }}>{pivots.map((pivot, index) => <button key={pivot.id} id={`${id}-tab-${index}`} type="button" role="tab" aria-selected={pivot.id === selectedPivot}
      tabIndex={pivot.id === selectedPivot ? 0 : -1} onClick={() => onPivot(pivot.id)}>{pivot.label}</button>)}</div>
  </div>;
}

/** Only bounded published row windows enter this presentation. There is no catalogue store here. */
export type ReadShelfProps<T> = ShelfProps<T>;
export function ReadShelf<T>(props: ReadShelfProps<T>) { return <Shelf {...props} />; }

export type ReadCoverItem = Readonly<{id: string; card: CoverCardProps}>;
export type ReadResumeItem = Readonly<{id: string; card: ResumeCardProps}>;
export type ReadSeriesItem = Readonly<{id: string; card: SeriesTileProps}>;
type RowBase = Readonly<{id: string; heading: string; subtitle?: string; onSeeAll?: () => void; onVisibleRange?: (first: number, last: number) => void}>;
export type ReadDiscoveryRow = RowBase & (
  | Readonly<{kind: 'covers'; items: readonly ReadCoverItem[]}>
  | Readonly<{kind: 'resume'; items: readonly ReadResumeItem[]}>
  | Readonly<{kind: 'series'; items: readonly ReadSeriesItem[]}>
);
const coverKey = (item: ReadCoverItem) => item.id;
const resumeKey = (item: ReadResumeItem) => item.id;
const seriesKey = (item: ReadSeriesItem) => item.id;

/** Actual card models/effects come from the viewer-bound adapter; missing rows are not fabricated. */
export function ReadDiscovery({rows, focusScope, platform, reducedMotion, newspaper, children, onVisibleRange}: Readonly<{
  rows: readonly ReadDiscoveryRow[]; focusScope: object; platform: LayoutPlatform; reducedMotion: boolean; newspaper?: ReactNode; children?: ReactNode;
  /** The library's own bounded content follows the drawn rows, in the same rhythm. */
  onVisibleRange?: (row: ReadDiscoveryRow, range: Readonly<{first: number; last: number}>) => void;
}>) {
  return <div className={styles.rows}>{rows.map(row => {
    const common = {key: row.id, focusScope, heading: row.heading, subtitle: row.subtitle, onSeeAll: row.onSeeAll,
      onVisibleRange: (first: number, last: number) => {row.onVisibleRange?.(first, last);onVisibleRange?.(row, {first, last});}, platform, reducedMotion};
    if (row.kind === 'resume') return <ReadShelf {...common} items={row.items} getKey={resumeKey} renderItem={item => <ReadResumeCard {...item.card} />} />;
    if (row.kind === 'series') return <ReadShelf {...common} items={row.items} getKey={seriesKey} renderItem={item => <SeriesTile {...item.card} />} />;
    return <ReadShelf {...common} items={row.items} getKey={coverKey} renderItem={item => <CoverCard {...item.card} />} />;
  })}{newspaper}{children}</div>;
}

export function ReadLibrarySurface({header, children}: Readonly<{header: ReadLibraryHeaderProps; children: ReactNode}>) {
  return <section className={styles.library}><ReadLibraryHeader {...header} />{children}</section>;
}
