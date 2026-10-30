import {Fragment, useEffect, useRef, useState} from 'react';
import {t, environment as env} from './text';
import {cardText, type FactPart} from '@core/presentation/kit-model';
import {LibraryContentService, readListeningSimilar, type ContentEntry, type ContentProjection, type LibraryContentSnapshot} from '@core/library-content.ts';
import {albumBadgeText} from '@core/music-badges.ts';
import {formatSignalText} from '@core/music-signal.ts';
import {browseNameId} from '@core/presentation/browse-toolbar.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {serviceI18n} from '@core/presentation/service-text.ts';
import {defaultI18n} from '@i18n';
import {AmbientArt} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Lockup} from '../../kit/layout';
import {SortMenu, TrackTable} from '../../kit/browse';
import {Menu} from '../../kit/overlays';
import {TitleFactsLine, TitleProse} from '../../kit/title/TitleParts';
import {useShellWidth} from '../../shell/viewport';
import {runScreenCommand, screenLifetime, type ScreenContext} from './context';
import {calendarDate, entrySubject, instantDate} from './model';
import {ScreenProblem} from './ScreenProblem';
import {MusicEntryMenu} from './MusicEntryMenu';
import {SimilarTitleRow, TitleRow} from './Detail';
import type {ListeningSimilarRead} from '@core/relation-rows.ts';
import type {ListeningViewerScope} from '@core/listening.ts';
import {albumSortRoute, albumTrackGroups} from './album-model';
import {MusicDetailLoading} from './MusicLoading';
import styles from './album.module.css';

export type AlbumActions = (entry: ContentEntry) => Readonly<{onOpen: () => void; onPlay?: () => void}>;
export type AlbumScreenProps = Readonly<{context: ScreenContext; libraryId: string; albumId: string; actions: AlbumActions; onBack?: () => void}>;
type Page = Readonly<{context: ScreenContext; libraryId: string; albumId: string; life: ReturnType<typeof screenLifetime>; service: LibraryContentService; snapshot: LibraryContentSnapshot}>;
type Similar = Readonly<{life: Page['life']; projection: ContentProjection; value: ListeningSimilarRead; scope: ListeningViewerScope}>;
type OpenMenu = Readonly<{page: Page; projection: ContentProjection; seed: ContentEntry; anchor: HTMLButtonElement}>;

/** The canonical album route owns a fresh service/lease on each effect setup,
 * including Strict Mode replay. The server's entity supplies complete header
 * facts; track pages are read only when the person asks for the next page. */
export function AlbumScreen({context, libraryId, albumId, onBack}: AlbumScreenProps) {
  const width = useShellWidth(), phone = width < 1024, more = useRef<HTMLButtonElement>(null);
  const [page, setPage] = useState<Page>(), [retry, setRetry] = useState(0);
  const [similar, setSimilar] = useState<Similar>(), [menu, setMenu] = useState<OpenMenu>();
  useEffect(() => {
    const life = screenLifetime(context), service = new LibraryContentService({api: life.api, scope: context.scope});
    const publish = () => {if (life.current()) setPage({context, libraryId, albumId, life, service, snapshot: service.getSnapshot()});};
    const off = service.subscribe(publish);
    publish();
    void service.select({libraryId, view: 'album', entityId: albumId}).catch(error => {if (life.current()) context.effects.reportError(error);});
    return () => {life.dispose(); off(); service.dispose();};
  }, [context, libraryId, albumId, retry]);
  const active = page?.context === context && page.libraryId === libraryId && page.albumId === albumId && page.life.current() ? page : undefined;
  const projection = active?.snapshot.projection;
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({active});
  useEffect(() => {setupPorts.current = {active};});
  useEffect(() => {
    const {active} = setupPorts.current;
    setSimilar(undefined);
    if (!active || active.snapshot.phase !== 'ready' || !projection) return;
    const viewer = context.identity?.viewer;
    if (!viewer || (viewer.authority !== 'local' && viewer.authority !== 'hosted')) return;
    const request = new AbortController(), {life, service} = active;
    const scope: ListeningViewerScope = {serverId: context.scope.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId};
    void readListeningSimilar(life.api, scope, albumId, {limit: 12, by: 'sound', signal: life.signal(request.signal)})
      .then(value => {
        if (!request.signal.aborted && life.current() && service.getSnapshot().projection === projection
          && value.itemId === albumId && value.libraryId === libraryId
          && value.revision.catalog === projection.revision.catalog && value.revision.viewer === projection.revision.viewer)
          setSimilar({life, projection, value, scope});
      }, error => {if (!request.signal.aborted && life.current()) context.effects.reportError(error);});
    return () => request.abort();
  }, [active?.life, active?.snapshot.phase, projection, context, libraryId, albumId]);
  const snapshot = active?.snapshot, album = projection?.entity;
  if (!active || !snapshot || !projection || !album || album.id !== albumId || album.kind !== 'album' || album.libraryId !== libraryId) {
    if (snapshot?.phase === 'error' || snapshot?.phase === 'refresh-required') return <ScreenProblem context={context} error={snapshot.error} retry={() => setRetry(value => value + 1)}/>;
    return <div className={styles.page}><div className={styles.bar} /><MusicDetailLoading kind="album" /></div>;
  }
  const {life, service} = active;
  const current = () => life.current() && service.getSnapshot().phase === 'ready' && service.getSnapshot().projection === projection;
  const groups = albumTrackGroups(snapshot.appended), songs = groups.flatMap(group => group.entries);
  const discNumbers = new Set(groups.flatMap(group => group.discNumber === undefined ? [] : [group.discNumber]));
  const songSections = snapshot.appended.filter(section => section.entries.some(entry => entry.kind === 'song'));
  const sorts = projection.sorts.map(sort => ({...sort, defaultDirection: sort.directions[0] ?? 'asc' as const, expensive: false, applicableKinds: []}));
  const selectedSort = sorts.find(sort => sort.id === projection.query.sort);
  const sort = {field: projection.query.sort, direction: projection.query.direction === 'desc' ? 'desc' as const : 'asc' as const, flippable: (selectedSort?.directions.length ?? 0) > 1, reshufflable: false};
  const songCurrent = (song: ContentEntry) => () => current() && service.getSnapshot().appended.some(section => section.entries.includes(song));
  const play = (song: ContentEntry) => {
    const witness = songCurrent(song);
    if (!witness() || !song.playback || song.available === false || song.missing || song.libraryId !== libraryId) return;
    runScreenCommand(context, () => context.effects.play({libraryId, itemId: song.playback!.itemId, startSeconds: song.playback!.startSeconds ?? 0}, life.signal(), witness));
  };
  const badges = albumBadgeText(album.badges);
  const parts: FactPart[] = [
    ...(album.year && album.year > 0 ? [{id: 'year', role: 'supplement' as const, text: defaultI18n.number(album.year, {useGrouping: false})}] : []),
    ...(album.count === undefined ? [] : [{id: 'songs', role: 'supplement' as const, text: t('lib.count', {count: album.count, pivot: 'songs'})}]),
  ];
  const open = (seed: ContentEntry, anchor: HTMLButtonElement) => {if (current()) setMenu({page: active, projection, seed, anchor});};
  const menuCurrent = () => !!menu && menu.page.life === life && menu.projection === projection && current()
    && (menu.seed === album || songs.includes(menu.seed));
  const menuAnchor = {current: menu?.anchor ?? null};
  const recommendations = similar?.life === life && similar.projection === projection ? similar : undefined;
  const long = album.title.length > 24, locale = env.locale ?? 'en', genres = album.genres?.filter(Boolean) ?? [];
  const metaParts: FactPart[] = [...parts, ...(genres.length ? [{id: 'genres', role: 'supplement' as const, text: genres.join(', ')}] : [])];
  const details: [string, string][] = [];
  const fact = (label: string, value: string | number | null | undefined) => {if (value !== undefined && value !== null && value !== '') details.push([label, String(value)]);};
  fact(t('title.year'), album.year && album.year > 0 ? defaultI18n.number(album.year, {useGrouping: false}) : undefined);
  fact(t('title.releaseDate'), calendarDate(album.releaseDate, locale) ?? instantDate(album.releaseDate, locale));
  fact(t('title.label'), album.label);
  fact(t('title.albumType'), album.albumType);
  fact(t('title.genres'), genres.join(', '));
  fact(t('title.added'), instantDate(album.addedAt, locale));
  const hasDetails = !!album.overview || details.length > 0;
  return <div className={styles.page}>
    <AmbientArt className={styles.ambient} treatment="wash" source={context.artwork.ambient(album.posterUrl)} />
    <div className={styles.bar}>
      {onBack && <Button variant="icon" icon="i-left" iconOnly aria-label={t('next.states.back')} onClick={() => runScreenCommand(context, () => {if (life.current()) onBack();})}/>}
      <span className={styles.barTitle}>{album.title}</span>
    </div>
    <header className={styles.hero}>
      <span className={styles.art}><AmbientArt treatment="cover" source={context.artwork.ambient(album.posterUrl)} foreground={context.artwork.foreground(album.posterUrl, 760, 1)} /></span>
      <div className={styles.copy}>
        <p className={styles.kind}>{t('next.browse.album')}</p>
        <Lockup title={album.title} long={long} platform={phone ? 'phone-web' : 'desktop'} className={styles.title}
          style={{fontSize: undefined, fontWeight: undefined, lineHeight: undefined}}/>
        <div className={styles.meta}>
          {album.artist?.name && <span className={styles.by}>{album.artist.name}</span>}
          <TitleFactsLine parts={metaParts}/>
          {badges.length > 0 && <span className={styles.badges}>{badges.map(badge => {
            const label = typeof badge === 'string' ? badge : formatSignalText(badge, t);
            return <span key={label} className={styles.badge}>{label}</span>;
          })}</span>}
        </div>
        <div className={styles.actions}>
          {context.effects.playAlbum && <>
            <Button variant="primary" icon="i-play" disabled={!current()} onClick={() => runScreenCommand(context, () => context.effects.playAlbum!(albumId, false, life.signal(), current))}>{t('next.controls.play')}</Button>
            <Button variant="glass" icon="i-shuffle" disabled={!current()} onClick={() => runScreenCommand(context, () => context.effects.playAlbum!(albumId, true, life.signal(), current))}>{t('title.shuffle')}</Button>
          </>}
          <Button ref={more} variant="glass" icon="i-more" iconOnly disabled={!current()} aria-label={t('card.more', {title: album.title})} onClick={event => open(album, event.currentTarget)}/>
        </div>
      </div>
    </header>
    <section className={hasDetails ? styles.split : styles.full}>
      <div className={styles.tracks}>
        {sorts.length > 0 && <div className={styles.sortBar}><SortMenu sorts={sorts} value={sort}
          label={value => {const id = browseNameId(value.id, value.labelKey, defaultI18n.has); return id && defaultI18n.has(id) ? t(id) : value.labelKey;}}
          onValueChange={value => runScreenCommand(context, () => {const route = albumSortRoute(projection, value); if (route && current()) return service.select(route);})} /></div>}
        {groups.map(group => {
          const discNumber = group.discNumber !== undefined && (group.discNumber > 1 || discNumbers.size > 1) ? group.discNumber : undefined;
          const heading = discNumber === undefined && songSections.length > 1 ? serverTextLabel(serviceI18n(), group.section.heading) : undefined;
          return <div className={styles.trackGroup} key={group.key}>
            {heading && <h3 className={styles.discHeading}>{heading}</h3>}
            <TrackTable variant="album" platform={phone ? 'phone' : 'desktop'} label={discNumber === undefined ? heading ?? t('music.allSongs') : t('next.browse.disc', {number: discNumber})} discNumber={discNumber}
              rows={group.entries.map((song, index) => ({id: song.id, number: song.trackNumber ?? index + 1, plays: song.plays, playable: current() && !!song.playback && song.available !== false && !song.missing && song.libraryId === libraryId, text: cardText(entrySubject(song), 'song-row', env)}))}
              onPlay={id => {const song = group.entries.find(song => song.id === id); if (song) play(song);}}
              onMore={(id, anchor) => {const song = group.entries.find(song => song.id === id); if (song && anchor) open(song, anchor);}} />
          </div>;
        })}
        {snapshot.appended.filter(section => section.nextCursor).map(section => <Button key={section.id} variant="glass" disabled={snapshot.phase !== 'ready'} onClick={() => runScreenCommand(context, () => {if (current()) return service.next(section.id);})}>{t('next.settings.loadMore')}</Button>)}
        {snapshot.phase === 'loading' && <p role="status">{t('music.loading')}</p>}
        {(snapshot.phase === 'error' || snapshot.phase === 'refresh-required') && <ScreenProblem context={context} error={snapshot.error} retry={() => setRetry(value => value + 1)}/>}
      </div>
      {hasDetails && <aside className={styles.aside}>
        <h2 className={styles.detailsTitle}>{t('title.details')}</h2>
        {album.overview && <><TitleProse className={styles.overview} markup={album.overview} />{album.overviewSource && <small className={styles.source}>{t('web.person.biographyFrom', {source: album.overviewSource})}</small>}</>}
        {details.length > 0 && <dl className={styles.facts}>{details.map(([label, value]) => <Fragment key={label}><dt>{label}</dt><dd>{value}</dd></Fragment>)}</dl>}
      </aside>}
    </section>
    {recommendations && recommendations.value.entries.length > 0 && <SimilarTitleRow first={recommendations.value} scope={recommendations.scope} heading={t('music.moreLikeThis')}
      subtitle={t(recommendations.value.bySound ? 'music.moreLikeThisBySound' : 'music.moreLikeThisHint')} binding={{context}}/>}
    {menu && <Menu open={menuCurrent()} onOpenChange={open => {if (!open) setMenu(undefined);}} anchorRef={menuAnchor} returnFocusRef={menuAnchor} title={menu.seed.title} placement="bottom-end">
      <MusicEntryMenu context={context} seed={menu.seed} signal={menu.page.life.signal()} current={menuCurrent}/>
    </Menu>}
  </div>;
}
