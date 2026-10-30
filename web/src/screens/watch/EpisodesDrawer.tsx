import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {Artwork} from '../../kit/artwork';
import type {ArtworkSource} from '../../kit/artwork/types';
import {Icon} from '../../kit/Icon';
import {Menu, type MenuEntry} from '../../kit/overlays';
import type {WatchPanelContext} from './types';
import type {WatchEpisodeRow, WatchEpisodes} from './episodes';
import styles from './episodes.module.css';

const t = defaultI18n.t;

function durationLabel(seconds: number): string {
  const hours = Math.floor(seconds / 3600), minutes = Math.round(seconds % 3600 / 60);
  return hours > 0
    ? t('next.kit.duration.hoursMinutes', {hours, minutes})
    : t('next.kit.duration.minutes', {minutes: Math.max(1, minutes)});
}
function airDate(value: string, locale: string | undefined): string | undefined {
  const date = new Date(`${value}T00:00:00`);
  if (Number.isNaN(date.getTime())) return undefined;
  return new Intl.DateTimeFormat(locale ?? 'en-US', {month: 'short', day: 'numeric', year: 'numeric'}).format(date);
}

/** desktop-player f6. The Episodes drawer over the paused picture: the season's own 16:9 cards,
 * the playing episode's live progress, the queue's real next entry, and a season picker that
 * re-reads the same retained title page. Choosing an episode stays inside the current queue. */
export function WatchEpisodesDrawer({episodes, context, locale, artwork, onPlay, onSeason, reportError}: {
  episodes: WatchEpisodes;
  context: WatchPanelContext;
  locale?: string;
  artwork: (url: string) => ArtworkSource | undefined;
  onPlay: (row: WatchEpisodeRow) => void | Promise<void>;
  onSeason: (seasonId: string | null) => void | Promise<void>;
  reportError: (error: unknown) => void;
}) {
  const [seasons, setSeasons] = useState(false), chip = useRef<HTMLButtonElement>(null), root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => { root.current?.focus({preventScroll: true}); }, []);
  const season = episodes.seasons.find(value => value.id === episodes.seasonId) ?? episodes.seasons[0];
  const act = (work: () => void | Promise<void>) => { try { void Promise.resolve(work()).catch(reportError); } catch (error) { reportError(error); } };
  const seasonItems: MenuEntry[] = episodes.seasons.map(value => ({
    id: value.id ?? 'all', label: value.title || t('next.kit.season', {number: value.number}), selected: value.id === episodes.seasonId,
    onSelect: () => { if (value.id !== episodes.seasonId) act(() => onSeason(value.id)); },
  }));
  return <div ref={root} tabIndex={-1} className={styles.drawer} role="dialog" aria-label={t('next.controls.episodes')} data-watch-episodes onKeyDown={event => {
    if (event.key !== 'Escape') return;
    event.preventDefault(); event.stopPropagation(); context.close();
  }}>
    <div className={styles.head}>
      <h3>{t('next.controls.episodes')}</h3>
      {episodes.seasons.length > 1
        ? <button ref={chip} type="button" className={styles.chip} aria-haspopup="menu" aria-expanded={seasons} onClick={() => setSeasons(value => !value)}>
          {season ? season.title || t('next.kit.season', {number: season.number}) : t('next.controls.season')}<Icon name="i-down" size={18} />
        </button>
        : season && <span className={styles.season}>{season.title || t('next.kit.season', {number: season.number})}</span>}
      <span className={styles.count}>{t('next.watch.episodeCount', {count: episodes.rows.length})}</span>
      <button type="button" className={styles.close} aria-label={t('next.overlays.close')} onClick={context.close}><Icon name="i-close" size={22} /></button>
    </div>
    <div className={styles.track}>
      {episodes.rows.map(row => {
        const date = row.airDate ? airDate(row.airDate, locale) : undefined;
        const percent = row.durationSeconds && row.progressSeconds !== undefined
          ? Math.min(100, Math.max(0, row.progressSeconds / row.durationSeconds * 100)) : 0;
        return <button key={row.itemId} type="button" className={styles.ep} data-playing={row.playing || undefined} data-next={row.next || undefined}
          aria-current={row.playing ? 'true' : undefined} onClick={() => { if (row.playing) { context.close(); return; } act(() => onPlay(row)); }}>
          <span className={styles.still}>
            {row.stillUrl ? <Artwork source={artwork(row.stillUrl)} alt="" fit="cover" /> : <span className={styles.blank} aria-hidden="true" />}
            {row.watched && !row.playing && <span className={styles.watched}><Icon name="i-check" size={13} />{t('next.cards.watched')}</span>}
            {row.playing && <span className={styles.playing}><span className={styles.eq} aria-hidden="true"><i /><i /><i /></span>{t('next.dock.playing')}</span>}
            {row.durationSeconds !== undefined && <span className={styles.duration}>{durationLabel(row.durationSeconds)}</span>}
            {percent > 0 && <><span className={styles.partBackground} aria-hidden="true" /><span className={styles.part} style={{width: `${percent}%`}} aria-hidden="true" /></>}
          </span>
          <span className={styles.title}>{row.title}</span>
          <span className={styles.when}>
            {row.next && <b>{t('next.dock.queue')}</b>}
            {row.number !== undefined && <>{row.next && <i className={styles.gap} aria-hidden="true" />}<span>{t('next.kit.episode', {number: row.number})}</span></>}
            {row.playing && row.durationSeconds !== undefined && row.progressSeconds !== undefined && <>
              <i className={styles.gap} aria-hidden="true" />
              <span className={styles.left}>{t('next.kit.remaining', {time: durationLabel(Math.max(0, row.durationSeconds - row.progressSeconds))})}</span>
            </>}
            {date && <><i className={styles.gap} aria-hidden="true" />{date}</>}
          </span>
          {row.overview && <span className={styles.overview}>{row.overview}</span>}
        </button>;
      })}
    </div>
    <Menu open={seasons} onOpenChange={setSeasons} title={t('next.controls.season')} anchorRef={chip} returnFocusRef={chip}
      presentation="anchored" width={240} items={seasonItems} />
  </div>;
}
