import {forwardIdentity} from '../detail/context';
import {Fragment, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {t} from './text';
import {defaultI18n} from '@i18n';
import {useLocation} from '@tanstack/react-router';
import {router} from '../../router';
import type {FactPart} from '@core/presentation/kit-model';
import {clockText} from '@core/presentation/kit-model/text.ts';
import {LibraryContentService, readListeningSimilar, type ContentEntry, type ContentProjection, type ContentSection, type LibraryContentApi} from '@core/library-content.ts';
import {dependsOn} from '@core/freshness-hub.ts';
import {useFreshDocument} from '../../platform/freshness';
import {AmbientArt, CircleArt} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Lockup} from '../../kit/layout';
import {TitleFactsLine, TitleProse} from '../../kit/title/TitleParts';
import {ScreenProblem} from './ScreenProblem';
import {TrackTable, type TrackRowData} from '../../kit/browse';
import {Menu} from '../../kit/overlays';
import {useShellWidth} from '../../shell/viewport';
import {runScreenCommand, screenLifetime, type ScreenContext} from './context';
import {SimilarTitleRow, TitleRow} from './Detail';
import type {ListeningSimilarRead} from '@core/relation-rows.ts';
import type {ListeningViewerScope} from '@core/listening.ts';
import {entrySubject} from './model';
import {cardText} from '@core/presentation/kit-model';
import {MusicEntryMenu} from './MusicEntryMenu';
import {MusicDetailLoading} from './MusicLoading';
import styles from './artist.module.css';

export type ArtistScreenProps = Readonly<{context: ScreenContext; libraryId: string; artistId: string; onBack?: () => void}>;

/** One page's own content lease: a fresh guarded service, admission signal and lifetime, owned by a
 * layout effect so a replayed setup never reuses a disposed one. */
type Lease = {service: LibraryContentService; api: LibraryContentApi; signal: AbortSignal; context: ScreenContext; libraryId: string; artistId: string; entry: typeof router.state.location; retry: number; onBack?: () => void; live: boolean; claimed: boolean; isCurrent: () => boolean; dispose: () => void};
type Witness = () => boolean;
type Similar = Readonly<{page: ContentProjection; token: object; value: ListeningSimilarRead; scope: ListeningViewerScope}>;

/** desktop-listen's artist page on its canonical library route (`?view=artist&entityId=`): the
 * artist's circle, the facts the view publishes, Play/Shuffle of the whole server-side artist
 * container, the server-ranked Top songs, then the artist's albums. */
export function ArtistScreen({context, libraryId, artistId, onBack}: ArtistScreenProps) {
  const entry = useLocation({select: value => value});
  const [retry, setRetry] = useState(0), [lease, setLease] = useState<Lease>();
  const committed = useRef<Lease | undefined>(undefined);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({onBack});
  useLayoutEffect(() => {setupPorts.current = {onBack};});
  useLayoutEffect(() => {
    const {onBack} = setupPorts.current;
    const scope = screenLifetime(context), life = new AbortController();
    const service = new LibraryContentService({api: scope.api, scope: {serverId: context.scope.serverId, viewerId: context.scope.viewerId}});
    const next: Lease = {service, api: scope.api, signal: life.signal, context, libraryId, artistId, entry, retry, onBack, live: true, claimed: false,
      isCurrent: () => {
        if (!next.live || committed.current !== next || router.state.location !== entry || life.signal.aborted) return false;
        if (!scope.current() || !context.isCurrent()) return false;
        return next.live && committed.current === next && router.state.location === entry && !life.signal.aborted;
      },
      dispose: () => {next.live = false; if (committed.current === next) committed.current = undefined; life.abort(); service.cancel(); service.dispose(); scope.dispose();}};
    committed.current = next;
    setLease(next);
    return () => {next.dispose(); setLease(current => current === next ? undefined : current);};
  }, [context, libraryId, artistId, entry, retry]);
  const active = lease && lease.live && lease.context === context && lease.libraryId === libraryId && lease.artistId === artistId && lease.entry === entry && lease.retry === retry ? lease : undefined;
  const back = () => {if (active?.isCurrent()) active.onBack?.();};
  const retryOriginal = (failureCurrent: () => boolean) => {if (!active || active.claimed || !active.isCurrent() || !failureCurrent() || !active.isCurrent() || !failureCurrent()) return; active.claimed = true; active.live = false; setRetry(value => value === active.retry ? value + 1 : value);};
  return <div className={styles.page}>
    {active ? <ArtistBody context={context} libraryId={libraryId} artistId={artistId} lease={active} retry={retryOriginal} back={back} /> : <><div className={styles.bar}>{onBack && <Button variant="icon" icon="i-left" iconOnly aria-label={t('next.states.back')} disabled onClick={back}/>}</div><MusicDetailLoading kind="artist" /></>}
  </div>;
}

function ArtistBody({context, libraryId, artistId, lease, retry, back}: {context: ScreenContext; libraryId: string; artistId: string; lease: Lease; retry: (failureCurrent: () => boolean) => void; back: () => void}) {
  const phone = useShellWidth() < 1024, {service} = lease;
  const [menu, setMenu] = useState<{page: ContentProjection; anchor: HTMLButtonElement}>(), more = useRef<HTMLButtonElement>(null);
  const content = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const [failed, setFailed] = useState<{lease: Lease; snapshot: ReturnType<LibraryContentService['getSnapshot']>; error: unknown}>();
  const failureRef = useRef<typeof failed>(undefined);
  useEffect(() => {
    let active = true;
    failureRef.current = undefined; setFailed(undefined);
    void service.select({libraryId, view: 'artist', entityId: artistId}).then(() => {
      if (!active || !lease.isCurrent()) return;
      const value = service.getSnapshot(), projection = value.projection;
      if (value.phase !== 'ready' || !projection || projection.entity?.kind !== 'artist' || projection.entity.id !== artistId || projection.scope.libraryId !== libraryId || projection.scope.view !== 'artist' || projection.scope.entityId !== artistId) {const failure = {lease, snapshot: value, error: value.error ?? new Error('Artist projection did not match its opening')}; failureRef.current = failure; setFailed(failure);}
    }, error => { if (active && lease.isCurrent()) { const failure = {lease, snapshot: service.getSnapshot(), error}; failureRef.current = failure; setFailed(failure); context.effects.reportError(error); } });
    return () => { active = false; };
  }, [service, lease, libraryId, artistId, context]);
  useFreshDocument(() => [dependsOn.libraries], async refresh => { if (lease.isCurrent()) await service.revalidate(refresh); }, () => lease.isCurrent() && service.getSnapshot().phase === 'ready');
  const projection: ContentProjection | undefined = content.phase === 'ready' || content.phase === 'loading' ? content.projection ?? undefined : undefined;
  const artist = projection?.entity;
  const admitted = !!projection && !!artist && artist.kind === 'artist' && artist.id === artistId
    && projection.scope.libraryId === libraryId && projection.scope.view === 'artist' && projection.scope.entityId === artistId
    ? {catalog: projection.revision.catalog, viewer: projection.revision.viewer} : undefined;
  const [similar, setSimilar] = useState<Similar>();
  const similarRead = useRef<object | undefined>(undefined);
  useLayoutEffect(() => {
    const token = {};similarRead.current = token;
    return () => {if (similarRead.current === token) similarRead.current = undefined;};
  }, [lease, projection, artistId, libraryId, context]);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({admitted, service});
  useEffect(() => {setupPorts1.current = {admitted, service};});
  useEffect(() => {
    const {admitted, service} = setupPorts1.current;
    setSimilar(undefined);
    const viewer = context.identity?.viewer;
    const page = projection, token = similarRead.current;
    if (!page || !token || !admitted || !viewer || (viewer.authority !== 'local' && viewer.authority !== 'hosted')) return;
    const request = new AbortController();
    const current = () => !request.signal.aborted && lease.isCurrent() && context.isCurrent() && similarRead.current === token &&
      service.getSnapshot().phase === 'ready' && service.getSnapshot().projection === page;
    if (!current()) return;
    const scope: ListeningViewerScope = {serverId: context.scope.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId};
    void readListeningSimilar(lease.api, scope, artistId, {limit: 12, by: 'sound', signal: AbortSignal.any([lease.signal, request.signal])})
      .then(value => {
        if (!current() || value.itemId !== artistId || value.libraryId !== libraryId
          || value.revision.catalog !== admitted.catalog || value.revision.viewer !== admitted.viewer) return;
        setSimilar({page, token, value, scope});
      }, error => {if (current()) context.effects.reportError(error);});
    return () => request.abort();
  }, [lease, artistId, libraryId, context, projection, admitted?.catalog, admitted?.viewer]);
  const section = (id: string): ContentSection | undefined => projection && admitted
    ? (content.appended.find(value => value.id === id) ?? projection.sections.find(value => value.id === id)) : undefined;
  const recoveryContext: ScreenContext = forwardIdentity(context, {isCurrent: lease.isCurrent, effects: {...context.effects,
    back: lease.onBack ? () => {if (lease.isCurrent()) lease.onBack?.();} : undefined,
    openHome: context.effects.openHome ? () => {if (lease.isCurrent()) context.effects.openHome?.();} : undefined,
    chooseProfile: context.effects.chooseProfile ? () => {if (lease.isCurrent()) context.effects.chooseProfile?.();} : undefined}});
  const localFailure = failed?.lease === lease && failed.snapshot === content ? failed : undefined;
  const readFailure = content.phase === 'error' || content.phase === 'refresh-required';
  const failureCurrent = () => service.getSnapshot() === content && (readFailure
    ? content.phase === 'error' || content.phase === 'refresh-required'
    : !!localFailure && failureRef.current === localFailure && localFailure.lease === lease);
  const bar = <div className={styles.bar}>{lease.onBack && <Button variant="icon" icon="i-left" iconOnly aria-label={t('next.states.back')} onClick={back}/>} {admitted && artist && <span className={styles.barTitle}>{artist.title}</span>}</div>;
  if (!projection || !artist || !admitted) return <>{bar}{localFailure || readFailure
    ? <ScreenProblem context={recoveryContext} error={content.error ?? localFailure?.error} retry={() => retry(failureCurrent)} />
    : <MusicDetailLoading kind="artist" />}</>;
  const albumsSection = section('items'), top = section('top_songs');
  const albums: readonly ContentEntry[] = albumsSection?.entries ?? [];
  const playArtist = context.effects.playArtist;
  const total = albumsSection && typeof albumsSection.totalCount === 'number' && Number.isFinite(albumsSection.totalCount) ? albumsSection.totalCount : undefined;
  const witness: Witness = () => {
    if (!lease.isCurrent() || !context.isCurrent()) return false;
    const live = service.getSnapshot();
    if (live.phase !== 'ready') return false;
    const page = live.projection;
    return page === projection && page.entity?.kind === 'artist' && page.entity.id === artistId && page.scope.libraryId === libraryId
      && page.scope.view === 'artist' && page.scope.entityId === artistId
      && page.revision.catalog === admitted.catalog && page.revision.viewer === admitted.viewer;
  };
  const cover = artist.posterUrl, discArt = cover ? context.artwork.foreground(cover, 480, 1) : undefined;
  const facts: FactPart[] = [
    ...(artist.genres && artist.genres.length > 0 ? [{id: 'genres', role: 'context' as const, text: artist.genres.slice(0, 3).join(', ')}] : []),
    ...(total === undefined ? [] : [{id: 'albums', role: 'supplement' as const, text: t('lib.count', {count: total, pivot: 'albums'})}]),
  ];
  const long = artist.title.length > 24, details: [string, string][] = [];
  if (artist.artistType) details.push([t('title.albumType'), artist.artistType]);
  if (artist.genres?.length) details.push([t('title.genres'), artist.genres.filter(Boolean).join(', ')]);
  const hasDetails = !!artist.overview || details.length > 0;
  return <div data-layout={phone ? 'phone' : 'desktop'}>
    {bar}
    <header className={styles.hero}>
      {!phone && <AmbientArt className={styles.ambient} treatment="artist" source={context.artwork.ambient(cover)} />}
      <span className={styles.disc}>{phone ? <AmbientArt className={styles.phoneDiscAmbient} treatment="phone" source={context.artwork.ambient(cover)} foreground={discArt} /> : <AmbientArt className={styles.discGlow} treatment="cover" source={context.artwork.ambient(cover)} />}
        <CircleArt covers={discArt ? [discArt] : []} initials={artist.title.split(/\s+/).filter(Boolean).slice(0, 2).map(word => Array.from(word)[0]).join('')} alt="" /></span>
      <div className={styles.copy}>
        <p className={styles.kind}>{t('filter.field.artist')}</p>
        <Lockup title={artist.title} long={long} platform={phone ? 'phone-web' : 'desktop'} className={styles.title}
          style={{fontSize: phone ? (long ? 42 : 56) : (long ? 68 : 104), lineHeight: .98}}/>
        <div className={styles.meta}><TitleFactsLine parts={facts}/></div>
        <div className={styles.actions}>
          {playArtist && <>
            <Button variant="primary" icon="i-play" onClick={() => runScreenCommand(context, () => {
              if (!witness()) throw new DOMException('Artist playback changed', 'AbortError');
              return playArtist(artistId, lease.signal, witness);
            })}>{t('next.controls.play')}</Button>
            <Button variant="glass" icon="i-shuffle" onClick={() => runScreenCommand(context, () => {
              if (!witness()) throw new DOMException('Artist playback changed', 'AbortError');
              return playArtist(artistId, lease.signal, witness, true);
            })}>{t('title.shuffle')}</Button>
          </>}
          {context.effects.playRadio && <Button ref={more} variant="glass" icon="i-more" iconOnly aria-label={t('card.more', {title: artist.title})}
            onClick={event => {if (witness()) setMenu({page: projection, anchor: event.currentTarget});}}/>}
        </div>
      </div>
    </header>
    {menu?.page === projection && <Menu open onOpenChange={open => {if (!open) setMenu(current => current === menu ? undefined : current);}} anchorRef={{current: menu.anchor}} returnFocusRef={{current: menu.anchor}} title={artist.title}>
      <MusicEntryMenu context={context} seed={{id: artist.id, title: artist.title}} signal={lease.signal} current={witness}/>
    </Menu>}
    {top && <ArtistTopSongs context={context} lease={lease} libraryId={libraryId} top={top} witness={witness} phone={phone} />}
    <section className={hasDetails ? styles.split : styles.full}>
      <div className={styles.albums}>
        {(albums.length > 0 || albumsSection?.nextCursor) && <TitleRow heading={t('music.albums')} entries={albums} binding={{context}}
          guard={() => {if (!witness()) throw new DOMException('Artist page changed', 'AbortError');}}
          pagination={{cursor: albumsSection?.nextCursor ?? '', loading: content.phase === 'loading', onMore: () => runScreenCommand(context, () => {
            const now = service.getSnapshot();
            if (!witness() || (now.appended.find(row => row.id === 'items') ?? now.projection?.sections.find(row => row.id === 'items')) !== albumsSection)
              throw new DOMException('Artist page changed', 'AbortError');
            return service.next('items');
          })}} />}
      </div>
      {hasDetails && <aside className={styles.aside}>
        <h2 className={styles.detailsTitle}>{t('title.details')}</h2>
        {artist.overview && <><TitleProse className={styles.overview} markup={artist.overview} />{artist.overviewSource && <small className={styles.source}>{t('web.person.biographyFrom', {source: artist.overviewSource})}</small>}</>}
        {details.length > 0 && <dl className={styles.facts}>{details.map(([label, value]) => <Fragment key={label}><dt>{label}</dt><dd>{value}</dd></Fragment>)}</dl>}
      </aside>}
    </section>
    {similar?.page === projection && similarRead.current === similar.token && similar.value.entries.length > 0 && <SimilarTitleRow heading={t('music.similarArtists')} subtitle={t(similar.value.bySound ? 'music.moreLikeThisBySound' : 'music.moreLikeThisHint')}
      first={similar.value} scope={similar.scope} binding={{context}} guard={() => {
        if (!witness() || similarRead.current !== similar.token) throw new DOMException('Artist related read changed', 'AbortError');
      }} />}
  </div>;
}

/** The server-ranked table and its own row menu, owned by one exact opening: published in a
 * layout-owned ref, retired with the section, the page or unmount, and never able to act after a
 * same-revision replacement of the drawn rows. */
type Opening = Readonly<{song: ContentEntry; anchor: HTMLButtonElement}>;

/** Without a real radio port there is no menu to open, so the table draws no ⋯ at all. */
const columnsWithoutActions = Object.freeze([
  {id: 'number' as const, width: '44px'}, {id: 'title' as const, width: 'minmax(0,1fr)'}, {id: 'plays' as const, width: '90px'}, {id: 'duration' as const, width: '80px'},
]);

function playableSong(song: ContentEntry, libraryId: string): boolean {
  return song.kind === 'song' && song.libraryId === libraryId && song.available !== false && !song.missing
    && song.playback !== undefined && song.playback.itemId === song.id;
}

function ArtistTopSongs({context, lease, libraryId, top, witness, phone}: {
  context: ScreenContext; lease: Lease; libraryId: string; top: ContentSection; witness: Witness; phone: boolean;
}) {
  const [opening, setOpening] = useState<Opening>();
  const live = useRef<Opening | undefined>(undefined);
  const currentRows = useRef<readonly ContentEntry[]>([]), anchor = useRef<HTMLButtonElement | null>(null);
  const rows = top.entries.filter(entry => entry.kind === 'song' && entry.libraryId === libraryId), radioPort = context.effects.playRadio;
  // The row membership a retained handler may act on is published after commit, never during render.
  useLayoutEffect(() => { currentRows.current = rows; }, [rows]);
  anchor.current = opening?.anchor ?? null;
  // Retirement is synchronous: a replaced section or page takes exactly this opening with it.
  useLayoutEffect(() => {
    live.current = undefined;
    setOpening(undefined);
    return () => { live.current = undefined; };
  }, [lease, top]);
  const retire = (expected: Opening) => {
    if (live.current !== expected) return;
    live.current = undefined;
    setOpening(current => current === expected ? undefined : current);
  };
  // A radio seed needs the admitted row, not item-play eligibility; only a real radio port can act.
  const owns = (value: Opening) => live.current === value && !!radioPort && witness() && currentRows.current.includes(value.song);
  const textEnv = {t, get locale() { return defaultI18n.region.formatLocale; }};
  const data: readonly TrackRowData[] = rows.map((song, index) => {
    const duration = clockText(song.duration);
    return {
    id: song.id, text: cardText(entrySubject(song), 'song-row', textEnv), number: (top.start ?? 0) + index + 1,
    ...(song.plays === undefined ? {} : {plays: song.plays}),
    ...(duration === undefined ? {} : {duration: {id: 'duration', role: 'value' as const, text: duration}}),
    playable: playableSong(song, libraryId),
    };
  });
  const play = (id: string) => {
    const song = rows.find(entry => entry.id === id), target = song?.playback;
    if (!song || !target || !currentRows.current.includes(song) || !playableSong(song, libraryId)) throw new DOMException('Song unavailable', 'AbortError');
    if (!witness()) throw new DOMException('Artist playback changed', 'AbortError');
    return context.effects.play({libraryId, itemId: target.itemId, startSeconds: target.startSeconds ?? 0}, lease.signal,
      () => witness() && currentRows.current.includes(song) && playableSong(song, libraryId));
  };
  if (rows.length === 0) return null;
  return <section className={styles.topSongs} aria-label={t('music.topSongs')}>
    <h2 className={styles.sectionHeading}>{t('music.topSongs')}</h2>
    <TrackTable variant="top-songs" label={t('music.topSongs')} rows={data} platform={phone ? 'phone' : 'desktop'} startIndex={top.start ?? 0} totalCount={top.totalCount}
      columns={radioPort ? undefined : columnsWithoutActions}
      onPlay={id => runScreenCommand(context, () => play(id))}
      onMore={(id, anchor) => {
        const song = rows.find(entry => entry.id === id);
        if (!anchor || !radioPort || !song || !witness() || !currentRows.current.includes(song)) return;
        const next: Opening = Object.freeze({song, anchor});
        live.current = next;
        setOpening(next);
      }} />
    {top.nextCursor && <div className={styles.more}><Button size="small" onClick={() => runScreenCommand(context, () => {
      const snapshot = lease.service.getSnapshot();
      const current = snapshot.appended.find(section => section.id === 'top_songs')
        ?? snapshot.projection?.sections.find(section => section.id === 'top_songs');
      if (!witness() || current !== top)
        throw new DOMException('Artist page changed', 'AbortError');
      return lease.service.next('top_songs');
    })}>{t('title.nextPage')}</Button></div>}
    {opening && owns(opening) && <Menu open onOpenChange={value => { if (!value) retire(opening); }}
      title={opening.song.title} anchorRef={anchor} returnFocusRef={anchor}>
      <MusicEntryMenu context={context} seed={{id: opening.song.id, title: opening.song.title}} signal={lease.signal} current={() => owns(opening)} />
    </Menu>}
  </section>;
}
