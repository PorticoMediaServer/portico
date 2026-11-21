import {cloneElement, Fragment, useEffect, useRef, useState, type MouseEvent, type ReactElement, type RefObject} from 'react';
import {t, environment as env} from './text';
import {cardText, type FactPart} from '@core/presentation/kit-model';
import {LibraryContentService, readListeningSimilar, type ContentEntry, type ContentProjection, type LibraryContentSnapshot} from '@core/library-content.ts';
import {albumBadgeText} from '@core/music-badges.ts';
import {formatSignalText} from '@core/music-signal.ts';
import {browseNameId} from '@core/presentation/browse-toolbar.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {serviceI18n} from '@core/presentation/service-text.ts';
import {defaultI18n} from '@i18n';
import {generatedCoverTint} from '@design';
import {Artwork, GeneratedCover} from '../../kit/artwork';
import {CoverHeroLink} from '../../kit/title/CoverHero';
import {PanelBlock, PanelHeading, TitleFrame, type TitleAction} from '../../kit/frames';
import {TitleFactsLine} from '../../kit/title/TitleParts';
import {setContainerFlag} from '@core/container-state.ts';
import {Button} from '../../kit/controls';
import {SortMenu, TrackTable} from '../../kit/browse';
import {Banner, FileInfo, Menu, MenuItem} from '../../kit/overlays';
import {albumAudioRows} from '@core/presentation/kit-model/album-audio.ts';
import {TitleProse} from '../../kit/title/TitleParts';
import {useShellWidth} from '../../shell/viewport';
import {runScreenCommand, screenLifetime, type ScreenContext} from './context';
import {calendarDate, entrySubject, instantDate} from './model';
import {ScreenProblem} from './ScreenProblem';
import {MusicEntryMenu} from './MusicEntryMenu';
import {OwnerMetadataGroup} from './OwnerMetadataGroup';
import {SimilarTitleRow, TitleRow} from './Detail';
import type {ListeningSimilarRead} from '@core/relation-rows.ts';
import type {ListeningViewerScope} from '@core/listening.ts';
import {albumSortRoute, albumTrackGroups} from './album-model';
import styles from './album.module.css';

export type AlbumActions = (entry: ContentEntry) => Readonly<{onOpen: () => void; onPlay?: () => void}>;
export type AlbumScreenProps = Readonly<{context: ScreenContext; libraryId: string; albumId: string; actions: AlbumActions; onBack?: () => void;
  /** Opens the album artist's page (the hero's artist name is a link, desktop-listen#album). */
  onArtist?: (artistId: string) => void;
  /** The song playing now (its item), when it is one of this page's: its row is marked (listen-2). */
  playingItemId?: string}>;
type Page = Readonly<{context: ScreenContext; libraryId: string; albumId: string; life: ReturnType<typeof screenLifetime>; service: LibraryContentService; snapshot: LibraryContentSnapshot}>;
type Similar = Readonly<{life: Page['life']; projection: ContentProjection; value: ListeningSimilarRead; scope: ListeningViewerScope}>;
type OpenMenu = Readonly<{page: Page; projection: ContentProjection; seed: ContentEntry; anchor: HTMLButtonElement}>;

/** The canonical album route owns a fresh service/lease on each effect setup,
 * including Strict Mode replay. The server's entity supplies complete header
 * facts; track pages are read only when the person asks for the next page. */
export function AlbumScreen({context, libraryId, albumId, onBack, onArtist, playingItemId}: AlbumScreenProps) {
  const width = useShellWidth(), phone = width < 1024, more = useRef<HTMLButtonElement>(null);
  const [page, setPage] = useState<Page>(), [retry, setRetry] = useState(0);
  const [similar, setSimilar] = useState<Similar>(), [menu, setMenu] = useState<OpenMenu>(), [fileInfo, setFileInfo] = useState(false);
  /** listen-2: the album's artist's other albums, read once the album is (the artist page's own read). */
  const [moreBy, setMoreBy] = useState<Readonly<{life: Page['life']; projection: ContentProjection; artistId: string; albums: readonly ContentEntry[]}>>();
  /** The favourite as this page last set it, until the album is read again. */
  const [favourite, setFavourite] = useState<Readonly<{album: string; value: boolean}>>();
  useEffect(() => {
    // The album is a document in the viewer's cache: Back draws it at once, and the hub reads it again in place only if it moved.
    const life = screenLifetime(context), service = new LibraryContentService({queries: context.queries, scope: context.scope});
    const publish = () => {const snapshot = service.getSnapshot(); if (life.current()) setPage({context, libraryId, albumId, life, service, snapshot});};
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
          && value.itemId === albumId && value.libraryId === libraryId)
          setSimilar({life, projection, value, scope});
      }, error => {if (!request.signal.aborted && life.current()) context.effects.reportError(error);});
    return () => request.abort();
  }, [active?.life, active?.snapshot.phase, projection, context, libraryId, albumId]);
  useEffect(() => {
    const {active} = setupPorts.current;
    setMoreBy(undefined);
    const entity = projection?.entity, artistId = entity?.kind === 'album' ? entity.artist?.id : undefined;
    if (!active || active.snapshot.phase !== 'ready' || !projection || !artistId) return;
    const {life} = active, artists = new LibraryContentService({queries: context.queries, scope: context.scope});
    let live = true;
    const off = artists.subscribe(() => {
      const read = artists.getSnapshot(), page = read.projection;
      if (!live || !life.current() || read.phase !== 'ready' || !page || page.entity?.kind !== 'artist' || page.entity.id !== artistId) return;
      const releases = read.appended.find(section => section.id === 'releases') ?? page.sections.find(section => section.id === 'releases');
      setMoreBy({life, projection, artistId, albums: (releases?.entries ?? []).filter(entry => entry.kind === 'album' && entry.id !== albumId)});
    });
    // An extra: a failed read leaves the page without the row, and says nothing.
    void artists.select({libraryId, view: 'artist', entityId: artistId}).catch(() => {});
    return () => {live = false; off(); artists.dispose();};
  }, [active?.life, active?.snapshot.phase, projection, context, libraryId, albumId]);
  const snapshot = active?.snapshot, album = projection?.entity;
  if (!active || !snapshot || !projection || !album || album.id !== albumId || album.kind !== 'album' || album.libraryId !== libraryId) {
    if (snapshot?.phase === 'error' || snapshot?.phase === 'refresh-required') return <ScreenProblem context={context} error={snapshot.error} retry={() => setRetry(value => value + 1)}/>;
    return <TitleFrame title="" cover={{shape: 'square', node: null}} state={{kind: 'loading'}} />;
  }
  const {life, service} = active;
  const artistMore = moreBy && moreBy.life === life && moreBy.projection === projection ? moreBy : undefined;
  const current = () => life.current() && service.getSnapshot().phase === 'ready' && service.getSnapshot().projection === projection;
  const groups = albumTrackGroups(snapshot.appended), songs = groups.flatMap(group => group.entries);
  const discNumbers = new Set(groups.flatMap(group => group.discNumber === undefined ? [] : [group.discNumber]));
  const songSections = snapshot.appended.filter(section => section.entries.some(entry => entry.kind === 'song'));
  // FX-P2 (J-23's rule): an album none of whose loaded tracks can be reached draws a disabled Play and
  // Shuffle with the reason, never an enabled Play that fails. Only once every track page is loaded.
  const unplayable = songs.length > 0 && songSections.every(section => !section.nextCursor)
    && !songs.some(song => !!song.playback && song.available !== false && !song.missing);
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
  const locale = env.locale ?? 'en', genres = album.genres?.filter(Boolean) ?? [];
  // desktop-listen#album: artist, year, genre, then the song count.
  const metaParts: FactPart[] = [...parts.filter(part => part.id === 'year'), ...(genres.length ? [{id: 'genres', role: 'supplement' as const, text: genres.join(', ')}] : []), ...parts.filter(part => part.id !== 'year')];
  const details: [string, string][] = [];
  const fact = (label: string, value: string | number | null | undefined) => {if (value !== undefined && value !== null && value !== '') details.push([label, String(value)]);};
  fact(t('title.year'), album.year && album.year > 0 ? defaultI18n.number(album.year, {useGrouping: false}) : undefined);
  fact(t('title.releaseDate'), calendarDate(album.releaseDate, locale) ?? instantDate(album.releaseDate, locale));
  fact(t('title.label'), album.label);
  fact(t('title.albumType'), album.albumType);
  fact(t('title.genres'), genres.join(', '));
  fact(t('title.added'), instantDate(album.addedAt, locale));
  const hasDetails = !!album.overview || details.length > 0;
  // The album on the title frame (desktop-listen album; phone-app 13): the square cover, Album, the artist and facts, Play,
  // Shuffle, the favourite, Add to playlist and More; the songs are the panel and Details the side.
  const favorite = favourite?.album === album.id ? favourite.value : album.favorite === true;
  const setFavorite = () => runScreenCommand(context, async () => {
    if (!current()) return;
    const next = !favorite; setFavourite({album: album.id, value: next});
    try { await setContainerFlag(life.api, 'album', album.id, 'favorite', next, life.signal()); }
    catch (error) { if (life.current()) setFavourite(undefined); throw error; }
  });
  const addToPlaylist = context.effects.addToPlaylist;
  const actions: TitleAction[] = [
    ...(context.effects.playAlbum ? [{icon: 'i-shuffle' as const, label: t('title.shuffle'), labelled: true, disabled: !current() || unplayable, onPress: () => runScreenCommand(context, () => context.effects.playAlbum!(albumId, true, life.signal(), current))}] : []),
    {icon: 'i-heart', label: t(favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), active: favorite, disabled: !current(), onPress: setFavorite},
    ...(addToPlaylist ? [{icon: 'i-listadd' as const, label: t('next.selection.addToPlaylist'), disabled: !current(), onPress: () => {}, wrap: (button: ReactElement) => cloneElement(button as ReactElement<{onClick?: (event: MouseEvent<HTMLButtonElement>) => void}>, {onClick: (event: MouseEvent<HTMLButtonElement>) => { const anchor = event.currentTarget; runScreenCommand(context, () => addToPlaylist({libraryId, itemId: album.id, kind: 'album', title: album.title}, anchor)); }})}] : []),
    {icon: 'i-more', label: t('card.more', {title: album.title}), disabled: !current(), onPress: () => {}, wrap: (button: ReactElement) => cloneElement(button as ReactElement<{ref?: RefObject<HTMLButtonElement | null>; onClick?: (event: MouseEvent<HTMLButtonElement>) => void}>, {ref: more, onClick: (event: MouseEvent<HTMLButtonElement>) => open(album, event.currentTarget)})},
  ];
  // desktop-listen album: the artist leads the facts, with the album's cover in its circle, and opens the artist's page.
  const artist = album.artist?.name ? <CoverHeroLink picture={album.posterUrl ? context.artwork.foreground(album.posterUrl, 56, 1) : undefined}
    href={onArtist && album.artist.id ? `/library/${encodeURIComponent(libraryId)}?${new URLSearchParams({view: 'artist', entityId: album.artist.id})}` : undefined}
    onOpen={onArtist && album.artist.id ? () => {const artistId = album.artist!.id!; runScreenCommand(context, () => {if (life.current()) onArtist(artistId);});} : undefined}>{album.artist.name}</CoverHeroLink> : null;
  return <><TitleFrame back={onBack ? {label: t('next.states.back'), onPress: () => runScreenCommand(context, () => {if (life.current()) onBack();})} : undefined}
    cover={{shape: 'square', node: album.posterUrl ? <Artwork source={context.artwork.foreground(album.posterUrl, 760, 1)} alt="" /> : <GeneratedCover title={album.title} author={album.artist?.name} glyph={Array.from(album.title.trim())[0] ?? ''} tint={generatedCoverTint(album.title)}/>}}
    kicker={{label: t('next.browse.album')}}
    title={album.title} metadata={<><TitleFactsLine lead={artist} parts={metaParts} />{badges.length > 0 && <span className={styles.badges}>{badges.map(badge => {
        const label = typeof badge === 'string' ? badge : formatSignalText(badge, t);
        return <span key={label} className={styles.badge}>{label}</span>;
      })}</span>}</>}
    primary={context.effects.playAlbum ? {icon: 'i-play', label: t('next.controls.play'), disabled: !current() || unplayable, onPress: () => runScreenCommand(context, () => context.effects.playAlbum!(albumId, false, life.signal(), current)),
      ...(unplayable ? {note: t('title.fileUnavailable')} : {})} : undefined}
    actions={actions}
    aside={hasDetails ? <div className={styles.aside}>
        <h2 className={styles.detailsTitle}>{t('title.details')}</h2>
        {album.overview && <><TitleProse className={styles.overview} markup={album.overview} />{album.overviewSource && <small className={styles.source}>{t('web.person.biographyFrom', {source: album.overviewSource})}</small>}</>}
        {details.length > 0 && <dl className={styles.facts}>{details.map(([label, value]) => <Fragment key={label}><dt>{label}</dt><dd>{value}</dd></Fragment>)}</dl>}
      </div> : undefined}>
      <PanelBlock label={t('music.allSongs')}><div className={styles.tracks}>
        {sorts.length > 0 && <div className={styles.sortBar}><SortMenu sorts={sorts} value={sort}
          label={value => {const id = browseNameId(value.id, value.labelKey, defaultI18n.has); return id && defaultI18n.has(id) ? t(id) : value.labelKey;}}
          onValueChange={value => runScreenCommand(context, () => {const route = albumSortRoute(projection, value); if (route && current()) return service.select(route);})} /></div>}
        {groups.map(group => {
          const discNumber = group.discNumber !== undefined && (group.discNumber > 1 || discNumbers.size > 1) ? group.discNumber : undefined;
          const heading = discNumber === undefined && songSections.length > 1 ? serverTextLabel(serviceI18n(), group.section.heading) : undefined;
          return <div className={styles.trackGroup} key={group.key}>
            {heading && <h3 className={styles.discHeading}>{heading}</h3>}
            <TrackTable variant="album" platform={phone ? 'phone' : 'desktop'} label={discNumber === undefined ? heading ?? t('music.allSongs') : t('next.browse.disc', {number: discNumber})} discNumber={discNumber}
              rows={group.entries.map((song, index) => ({id: song.id, playing: !!playingItemId && song.playback?.itemId === playingItemId, number: song.trackNumber ?? index + 1, plays: song.plays, playable: current() && !!song.playback && song.available !== false && !song.missing && song.libraryId === libraryId, text: cardText(entrySubject(song), 'song-row', env)}))}
              onPlay={id => {const song = group.entries.find(song => song.id === id); if (song) play(song);}}
              onMore={(id, anchor) => {const song = group.entries.find(song => song.id === id); if (song && anchor) open(song, anchor);}} />
          </div>;
        })}
        {snapshot.appended.filter(section => section.nextCursor).map(section => <Button key={section.id} variant="glass" disabled={snapshot.phase !== 'ready'} onClick={() => runScreenCommand(context, () => {if (current()) return service.next(section.id);})}>{t('next.settings.loadMore')}</Button>)}
        {snapshot.phase === 'loading' && <p role="status">{t('music.loading')}</p>}
        {(snapshot.phase === 'error' || snapshot.phase === 'refresh-required') && <ScreenProblem context={context} error={snapshot.error} retry={() => setRetry(value => value + 1)}/>}
      </div></PanelBlock>
    {artistMore && album.artist?.name && (artistMore.albums.length
      ? <TitleRow heading={t('music.moreBy', {name: album.artist.name})} entries={artistMore.albums} binding={{context}}
        guard={() => {if (!current()) throw new DOMException('Album page changed', 'AbortError');}}/>
      : <section className={styles.moreBy} aria-label={t('music.moreBy', {name: album.artist.name})}>
        <PanelHeading title={t('music.moreBy', {name: album.artist.name})}/>
        <Banner icon="i-note" action={onArtist ? {label: t('music.goToArtist', {name: album.artist.name}), onAction: () => {const artistId = artistMore.artistId; runScreenCommand(context, () => {if (life.current()) onArtist(artistId);});}} : undefined}>
          {t('music.onlyAlbumBy', {title: album.displayTitle ?? album.title, name: album.artist.name})}</Banner>
      </section>)}
    {recommendations && <SimilarTitleRow first={recommendations.value} scope={recommendations.scope} heading={t('music.moreLikeThis')}
      subtitle={t(recommendations.value.bySound ? 'music.moreLikeThisBySound' : 'music.moreLikeThisHint')} binding={{context}}/>}
  </TitleFrame>
    {menu && <Menu open={menuCurrent()} onOpenChange={open => {if (!open) setMenu(undefined);}} anchorRef={menuAnchor} returnFocusRef={menuAnchor} title={menu.seed.title} placement="bottom-end">
      <MusicEntryMenu context={context} seed={menu.seed} signal={menu.page.life.signal()} current={menuCurrent}>
        {/* music-plus frame 7: the album's ⋯ offers File info with the format its songs share; a mixed or unknown album has none to show. */}
        {menu.seed === album && albumAudioRows(album.albumAudio, env) && <MenuItem icon="i-info" onSelect={() => {if (menuCurrent()) setFileInfo(true);}}>{t('next.overlays.fileInfo')}</MenuItem>}
      </MusicEntryMenu>
      <OwnerMetadataGroup separated={!!context.effects.playRadio} context={context} anchor={() => menu.anchor}
        guard={() => {if (!menuCurrent()) throw new DOMException('Album page changed', 'AbortError');}}
        target={menu.seed === album ? {entityKind: 'album', entityId: album.id, libraryId, title: album.title}
          : menu.seed.kind === 'song' ? {libraryId, itemId: menu.seed.id, kind: 'song', title: menu.seed.title} : undefined}/>
    </Menu>}
    {fileInfo && album && (() => {
      const rows = albumAudioRows(album.albumAudio, env, phone ? 'phone' : 'desktop');
      return rows ? <FileInfo open onOpenChange={open => {if (!open) setFileInfo(false);}} model={{heading: t('next.overlays.fileInfo'), files: [], album: rows}} subject={album.title}
        layout={phone ? 'phone' : 'desktop'} returnFocusRef={more} onManage={() => {}}/> : null;
    })()}
  </>;
}
