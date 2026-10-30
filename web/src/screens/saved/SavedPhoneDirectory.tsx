import {useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';

import type {ShellArtwork} from '../../shell/artwork';
import {savedCatalogueEntry} from './catalogue-selection';
import {savedReaderRoute} from './runtime.ts';
import {sameSavedRoute} from './route';
import {Icon} from '../../kit/Icon';
import {PosterCard} from '../../kit/cards';
import {homeCardModel} from '../home/model';
import {shapeFor} from '@core/presentation/card-shape.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {SavedRuntime, SavedScreenRoute} from './runtime.ts';
import styles from './saved.module.css';
import directory from './saved-phone-directory.module.css';

const t = defaultI18n.t;
/** phone-web frame 6: Saved is a list of rows on a phone, each opening its own
 * pushed page. Counts are the summary's published fields only; no row is offered
 * that the runtime cannot open, and there is no selection or write here. */
export function SavedPhoneDirectory({runtime, downloadsOffered, artwork, select, openEntry}: {
  runtime: SavedRuntime;
  downloadsOffered: boolean;
  artwork: ShellArtwork;
  select(route: SavedScreenRoute): void;
  openEntry(entry: ContentEntry): void;
}) {
  const summary = useSyncExternalStore(runtime.summary.subscribe, runtime.summary.getSnapshot, runtime.summary.getSnapshot);
  const posters = useSyncExternalStore(runtime.posters.subscribe, runtime.posters.getSnapshot, runtime.posters.getSnapshot);
  const value = summary.value;
  // The drawn My List rail: at most three real poster titles in the server's own order,
  // and only from the canonical poster projection this screen admitted. The guard is
  // the frozen canonical route with the poster shape, so a filtered or sorted
  // projection is not accepted; there is no square fallback and no forced adaptation.
  const preview: readonly ContentEntry[] = (() => {
    const canonical = Object.freeze({...savedReaderRoute({view: 'home'}), shape: 'posters' as const});
    if (posters.phase !== 'ready' || !posters.projection || !sameSavedRoute(posters.route, canonical)) return [];
    const bounded: ContentEntry[] = [];
    for (const section of posters.projection.sections) for (const entry of section.entries) {
      const media = savedCatalogueEntry(entry);
      if (!media?.posterUrl || shapeFor(media.kind, 'grid') !== 'poster') continue;
      bounded.push(media);
      if (bounded.length === 3) return bounded;
    }
    return bounded;
  })();
  const count = (shapes: Readonly<{posters: number; squares: number}> | undefined) => shapes === undefined ? undefined : shapes.posters + shapes.squares;
  const rows: readonly Readonly<{id: string; label: string; count?: number; icon: Parameters<typeof Icon>[0]['name']; route: SavedScreenRoute}>[] = [
    {id: 'watchlist', label: t('saved.tab.watchlist'), count: count(value?.myList), icon: 'i-bookmark', route: {view: 'watchlist'}},
    {id: 'favorites', label: t('saved.tab.favorites'), count: count(value?.favorites), icon: 'i-heart', route: {view: 'favorites'}},
    {id: 'playlists', label: t('saved.tab.playlists'), count: value?.playlists, icon: 'i-queue', route: {view: 'playlists'}},
    {id: 'collections', label: t('saved.tab.collections'), count: value?.collections, icon: 'i-stack', route: {view: 'collections'}},
    {id: 'views', label: t('saved.tab.savedViews'), count: value?.savedViews, icon: 'i-filter', route: {view: 'views'}},
    {id: 'history', label: t('saved.tab.history'), icon: 'i-clock', route: {view: 'history'}},
    ...(downloadsOffered ? [{id: 'downloads', label: t('downloads.title'), icon: 'i-dl' as const, route: {view: 'downloads'} as SavedScreenRoute}] : []),
  ];
  const rowsShown = preview.length ? rows.filter(row => row.id !== 'watchlist') : rows;
  return <div className={styles.phoneDirectory}>
    <header className={styles.header}><h1>{t('saved.title')}</h1></header>
    {summary.error && !value ? <p className={styles.directoryHelp} role="alert">{t('saved.loadFailed')}</p> : null}
    {preview.length > 0 && <div className={styles.phonePreview}>
      <button type="button" className={styles.phonePreviewHead} onClick={() => select({view: 'watchlist'})}>
        <b>{t('saved.tab.watchlist')}</b>{count(value?.myList) !== undefined && <span>{defaultI18n.number(count(value!.myList)!)}</span>}<Icon name="i-right" size={16} />
      </button>
      <div className={styles.phonePreviewRail}>{preview.map(media => <PosterCard key={media.id} model={homeCardModel(media, false, {t, locale: defaultI18n.region.formatLocale})}
        artwork={artwork.foreground(media.posterUrl, 118 * (globalThis.devicePixelRatio || 1), 2 / 3)} context="shelf" onNavigate={() => openEntry(media)} />)}</div>
    </div>}
    <div className={directory.rows} role="list">
      {rowsShown.map(row => <div key={row.id} role="listitem">
        <button type="button" className={directory.row} onClick={() => select(row.route)}>
          <span className={directory.icon}><Icon name={row.icon} size={18} /></span>
          <span className={directory.label}>{row.label}</span>
          {row.count !== undefined && <span className={directory.count}>{defaultI18n.number(row.count)}</span>}
          <Icon name="i-right" size={18} className={directory.chevron} />
        </button>
      </div>)}
    </div>
  </div>;
}
