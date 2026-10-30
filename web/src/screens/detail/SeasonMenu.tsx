import {useEffect, useRef, useState} from 'react';
import type {WorkspaceSeason} from '@core/show-workspace.ts';
import {seasonProgress} from '@core/presentation/title-page.ts';
import {Chip, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Popover} from '../../kit/overlays';
import {t} from './text';
import styles from './show.module.css';

/** The drawn season menu: every loaded season with its years, count and real progress. */
export function SeasonMenu({open, onOpenChange, label, seasons, selectedId, totalCount, hasPrevious, hasNext, onSelect, onPrevious, onNext, onJump}: Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  label: string;
  seasons: readonly WorkspaceSeason[];
  selectedId?: string;
  totalCount: number;
  hasPrevious: boolean;
  hasNext: boolean;
  onSelect: (id: string) => void;
  onPrevious: () => void;
  onNext: () => void;
  onJump: (number: number) => Promise<boolean>;
}>) {
  const [query, setQuery] = useState('');
  const firstRow = useRef<HTMLButtonElement>(null), jump = useRef<HTMLInputElement>(null), list = useRef<HTMLDivElement>(null);
  useEffect(() => { if (!open) setQuery(''); }, [open]);
  const submit = async () => {
    const number = Number(query);
    if (!Number.isSafeInteger(number) || number < 1 || number > totalCount) return;
    if (await onJump(number)) onOpenChange(false);
  };
  return <Popover open={open} onOpenChange={value => onOpenChange(value)} title={t('title.seasonMenu')} trigger={<Chip variant="toolbar" trailingIcon="i-down">{label}</Chip>}
    placement="bottom-start" width={380} className={styles.seasonPopover} initialFocusRef={totalCount > 30 ? jump : firstRow}>
    <header className={styles.seasonHeader}><b>{t('next.detail.seasons', {count: totalCount})}</b>
      {totalCount > 30 && <form className={styles.seasonJump} onSubmit={event => { event.preventDefault(); void submit(); }}>
        <TextField ref={jump} variant="compact" leadingIcon="i-search" inputMode="numeric" aria-label={t('next.detail.jumpSeason')} placeholder={t('next.detail.jumpSeason')}
          value={query} onChange={event => setQuery(event.currentTarget.value.replace(/[^0-9]/g, '').slice(0, 4))}
          trailing={<span>{t('next.detail.seasonNumberRange', {first: 1, last: totalCount})}</span>} />
      </form>}
    </header>
    <div ref={list} role="listbox" aria-label={t('title.seasonMenu')} onKeyDown={event => {
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      const rows = [...(list.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])];
      if (!rows.length) return;
      const index = rows.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? rows.length - 1 : index < 0 ? 0 : (index + (event.key === 'ArrowDown' ? 1 : -1) + rows.length) % rows.length;
      event.preventDefault(); rows[next]?.focus();
    }}>
      <div className={styles.seasonList}>{seasons.filter(season => season.number !== 0).map((season, index) => {
        const progress = seasonProgress(season.watchedCount, season.unwatchedCount);
        const complete = !!progress && progress.total > 0 && progress.watched >= progress.total;
        const first = year(season.firstAired), last = year(season.lastAired);
        return <button key={season.id} ref={index === 0 ? firstRow : undefined} type="button" role="option" aria-selected={season.id === selectedId}
          className={styles.seasonRow} onClick={() => onSelect(season.id)}>
          <span className={styles.seasonName}><b>{season.number === 0 ? t('next.kit.special') : t('next.kit.season', {number: season.number})}</b>
            {first !== undefined && <small>{t('next.detail.seasonYears', {first, last: last ?? first})}</small>}</span>
          <span className={styles.seasonFacts}>{season.episodeCount !== undefined && <span>{t('count.episodes', {count: season.episodeCount})}</span>}
            {complete ? <span className={styles.seasonWatched}><Icon name="i-check" size={14} />{t('next.detail.watched')}</span>
              : progress && progress.total > 0 ? <span className={styles.seasonProgress} aria-hidden="true"><i style={{width: `${Math.round(100 * progress.watched / progress.total)}%`}} /></span> : null}
          </span>
        </button>;
      })}</div>
      {seasons.filter(season => season.number === 0).map(season => <div className={styles.seasonSpecials} key={season.id}>
        <button type="button" role="option" aria-selected={season.id === selectedId} className={styles.seasonRow} onClick={() => onSelect(season.id)}>
          <span className={styles.seasonName}><b>{t('next.kit.special')}</b></span>
          {season.episodeCount !== undefined && <span className={styles.seasonFacts}>{t('count.episodes', {count: season.episodeCount})}</span>}
        </button>
      </div>)}
    </div>
    {(hasPrevious || hasNext) && <div className={styles.seasonPaging}>
      {hasPrevious && <button type="button" onClick={onPrevious}>{t('title.earlierSeasons')}</button>}
      {hasNext && <button type="button" onClick={onNext}>{t('title.laterSeasons')}</button>}
    </div>}
  </Popover>;
}

function year(value?: string): number | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.getUTCFullYear() : undefined;
}
