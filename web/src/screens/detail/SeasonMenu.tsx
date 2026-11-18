import type {WorkspaceSeason} from '@core/show-workspace.ts';
import {seasonProgress} from '@core/presentation/title-page.ts';
import {SeasonMenu as KitSeasonMenu} from '../../kit/frames';
import {t} from './text';

/** The show page's season menu: its seasons as the kit's rows (years, count and real progress). The drawing is the kit's
 * `SeasonMenu` (kit/frames/title-parts.tsx). */
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
  const rows = seasons.map(season => {
    const progress = seasonProgress(season.watchedCount, season.unwatchedCount);
    const complete = !!progress && progress.total > 0 && progress.watched >= progress.total;
    const first = year(season.firstAired), last = year(season.lastAired);
    return {id: season.id, special: season.number === 0, label: season.number === 0 ? t('next.kit.special') : t('next.kit.season', {number: season.number}),
      years: season.number !== 0 && first !== undefined ? t('next.detail.seasonYears', {first, last: last ?? first}) : undefined,
      count: season.episodeCount !== undefined ? t('count.episodes', {count: season.episodeCount}) : undefined,
      watched: season.number !== 0 && complete, progress: season.number !== 0 && progress && progress.total > 0 ? progress.watched / progress.total : undefined};
  });
  return <KitSeasonMenu open={open} onOpenChange={onOpenChange} label={label} title={t('title.seasonMenu')} heading={t('next.detail.seasons', {count: totalCount})} rows={rows} selectedId={selectedId}
    watchedLabel={t('next.detail.watched')} onSelect={onSelect}
    jump={totalCount > 30 ? {placeholder: t('next.detail.jumpSeason'), range: t('next.detail.seasonNumberRange', {first: 1, last: totalCount}), maximum: totalCount, onJump} : undefined}
    paging={{earlierLabel: hasPrevious ? t('title.earlierSeasons') : undefined, laterLabel: hasNext ? t('title.laterSeasons') : undefined, onEarlier: onPrevious, onLater: onNext}} />;
}

function year(value?: string): number | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.getUTCFullYear() : undefined;
}
