import {ReadingGroupCard} from './ReadingGroupCard';
import type {SavedReadingGroupCard} from '@core/reading-saved-group.ts';
import type {SavedCatalogueSelection} from './catalogue-selection';
import {CatalogueSelectionEntry, useCatalogueSelectionActive} from '../browse/document-selection';
import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {ContentEntry} from '@core/library-content.ts';
import type {SavedEntry, SavedListFilter, SavedProjection, SavedRoute, SavedService} from '@core/saved.ts';
import type {PlaylistListFilter} from '@core/saved.ts';
import type {PersonalResource, PersonalResourceEntry, PersonalSavedService} from '@core/personal-saved.ts';
import {serverTextLabel} from '@core/presentation/server-text.ts';
import {shapeFor} from '@core/presentation/card-shape.ts';
import {dependsOn} from '@core/freshness-hub.ts';
import {Button, Chip, Tabs, Switch} from '../../kit/controls';
import {FiltersButton, SortMenu, ViewToggle} from '../../kit/browse';
import {MenuRow, Popover} from '../../kit/overlays';
import {PlaylistMosaic, PosterCard, SquareCard, NewPlaylistCard, type CardSelection} from '../../kit/cards';
import {AmbientArt, CoverMosaic, type AmbientArtSource, type ArtworkSource} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {WindowedGrid} from '../../kit/layout';
import {Skeleton, StateView} from '../../kit/states';
import type {ShellArtwork} from '../../shell/artwork';
import {useQuietRefresh} from '../../platform/freshness';
import {useShellWidth} from '../../shell/viewport';
import {homeCardModel} from '../home/model';
import {BrowseEntryCard} from '../browse';
import {LocalDownloads, ServerDownloads, type SavedDownloadEffects} from './Downloads';
import {sameSavedDownloadsScope, type SavedDownloadsPort} from './downloads-port.ts';
import {savedReaderRoute, type SavedRuntime, type SavedScreenRoute} from './runtime.ts';
import {savedSectionWindow} from './window.ts';
import {savedText as s} from './text.ts';
import {SavedHistory, SavedResourceScreen} from './Resource';
import {ResourceFields} from './ResourceFields';
import {ResourceRow} from './ResourceRow';
import {SavedDialog} from './SavedDialog';
import {SavedPhoneDirectory} from './SavedPhoneDirectory';
import {SavedPhoneNav} from './SavedPhoneNav';
import type {SavedPresentation} from './presentation.ts';
export type {SavedPresentation} from './presentation.ts';
import type {SavedOperations} from './operations.ts';
import {sameSavedRoute, savedViewKey} from './route.ts';
import {createSavedRetryAdmission, retireSavedRetryAdmission, savedRetryCurrent, type SavedRetryAdmission, type SavedRetryTicket} from './retry-admission.ts';
import {useSavedViewOperations} from './use-view-operations.ts';
import styles from './saved.module.css';

const t = defaultI18n.t;
export type SavedSelection = Readonly<{
  toolbar: ReactNode;
  card(service: SavedService, sectionId: string, entry: SavedEntry): CardSelection | undefined;
  registerWindow(service: SavedService, sectionId: string, first: number, last: number): (() => void) | undefined;
  personalCard(service: PersonalSavedService, entry: PersonalResourceEntry): CardSelection | undefined;
}>;
export type SavedEffects = SavedDownloadEffects & Readonly<{
  openEntry(entry: ContentEntry): void;
  openReadingGroup?: (card: Extract<SavedReadingGroupCard, {hidden: false}>) => void;
  playEntry(entry: ContentEntry, signal: AbortSignal): Promise<void>;
  openMore?: (service: SavedService, sectionId: string, entry: SavedEntry, anchor: HTMLElement) => void;
  selectRoute(route: SavedScreenRoute): void;
  /** Present only when the playback owner supports this complete server resource. */
  playResource?: (target: Readonly<{kind: 'playlist' | 'collection' | 'view'; id: string; shuffle: boolean}>, signal: AbortSignal) => Promise<void>;
}>;
export type SavedBindings = Readonly<{
  runtime: SavedRuntime;
  route: SavedScreenRoute;
  online: boolean;
  downloadsOffered: boolean;
  localDownloads?: SavedDownloadsPort;
  artwork: ShellArtwork;
  libraries?: readonly Readonly<{id: string; name: string}>[];
  preparationArtwork: Parameters<typeof ServerDownloads>[0]['artwork'];
  presentation: SavedPresentation;
  selection?: SavedSelection;
  catalogueSelection?: SavedCatalogueSelection;
  effects: SavedEffects;
}>;
export type SavedViewBindings = SavedBindings & Readonly<{operations: SavedOperations}>;
const tabLabels = {watchlist: 'saved.tab.watchlist', favorites: 'saved.tab.favorites', playlists: 'saved.tab.playlists', collections: 'saved.tab.collections', views: 'saved.tab.savedViews', history: 'saved.tab.history', downloads: 'downloads.title'} as const;

export function SavedScreen(bindings: SavedBindings) {
  const {runtime, route, presentation, effects} = bindings;
  const viewKey = savedViewKey(runtime.scope, route), operations = useSavedViewOperations(runtime.operations, viewKey);
  // `home` is the phone directory; every reader below uses its canonical My List.
  const readerRoute = useMemo(() => savedReaderRoute(route), [route]), phone = useShellWidth() < 600;
  const viewBindings: SavedViewBindings = {...bindings, operations};
  const [creating, setCreating] = useState<Readonly<{viewKey: string; kind: 'playlist' | 'collection'}>>();
  useEffect(() => {
    if (readerRoute.view === 'downloads') return;
    const current = ['collections', 'views', 'history', 'resource'].includes(readerRoute.view) ? runtime.personal.getSnapshot().route : runtime.saved.getSnapshot().route;
    const same = readerRoute.view === 'watchlist' || readerRoute.view === 'favorites'
      ? sameSavedRoute(runtime.posters.getSnapshot().route, {...readerRoute, shape: 'posters'}) && sameSavedRoute(runtime.squares.getSnapshot().route, {...readerRoute, shape: 'squares'})
      : sameSavedRoute(current, readerRoute);
    if (!same) void runtime.select(route).catch(effects.reportError);
  }, [runtime, route, readerRoute, effects.reportError]);
  // The directory's counts are one summary read per actual entry. It starts whatever
  // phase the borrowed summary was left in — an abandoned, aborted load included —
  // carries its own abort, and a stable dependency set keeps it from looping. It
  // never adopts an older request and never disposes the shared summary.
  const reportSummaryError = effects.reportError;
  useEffect(() => {
    if (route.view !== 'home' || !runtime.operations.current) return;
    const controller = new AbortController();
    void runtime.summary.refresh(controller.signal).catch(error => {if (!controller.signal.aborted) reportSummaryError(error);});
    return () => controller.abort();
  }, [runtime, route.view, reportSummaryError]);
  const refresh = useCallback(async (context?: {signal?: AbortSignal; settle?: () => Promise<void>}) => {
    if (!runtime.operations.current || readerRoute.view === 'downloads') return;
    if (['collections', 'views', 'history', 'resource'].includes(readerRoute.view)) await Promise.all([runtime.personal.revalidate(context), readerRoute.view === 'collections' || readerRoute.view === 'views' ? runtime.summary.refresh(context?.signal) : Promise.resolve()]);
    else if (readerRoute.view === 'watchlist' || readerRoute.view === 'favorites') await Promise.all([runtime.posters.revalidate(context), runtime.squares.revalidate(context)]);
    else await runtime.saved.revalidate(context);
  }, [runtime, readerRoute]);
  useQuietRefresh('saved', refresh, () => runtime.operations.current, {dependencies: [dependsOn.personal(), dependsOn.progress(), dependsOn.content(), dependsOn.settings('preferences')], everyMs: 0});
  const localSnapshot = useSyncExternalStore(bindings.localDownloads?.subscribe ?? noSubscribe, bindings.localDownloads?.getSnapshot ?? noLocalSnapshot, noLocalSnapshot);
  const localUsable = !!bindings.localDownloads && sameSavedDownloadsScope(bindings.localDownloads.scope, runtime.scope) && !!localSnapshot?.available;
  useEffect(() => {
    if (localUsable && effects.saveReadyPreparation) runtime.installReadyDownloads(effects.saveReadyPreparation, effects.reportError);
    // The captured runtime, rather than this route's effect, owns retirement.
  }, [runtime, localUsable, effects.saveReadyPreparation, effects.reportError]);
  if (!runtime.operations.current) return null;
  if (route.view === 'playlist' || route.view === 'resource') return <SavedResourceScreen key={viewKey} bindings={viewBindings} />;
  // At phone width the home route is the drawn directory (phone-web frame 6);
  // desktop and tablet keep today's tabbed page with My List active.
  if (phone && route.view === 'home') return <main className={styles.page}>
    {presentation.backdrop && <div className={styles.wash} aria-hidden="true"><AmbientArt source={presentation.backdrop} treatment="hero" /></div>}
    <SavedPhoneDirectory runtime={runtime} downloadsOffered={bindings.downloadsOffered || localUsable} artwork={bindings.artwork} select={effects.selectRoute} openEntry={effects.openEntry} />
  </main>;
  const dataBindings = readerRoute === route ? viewBindings : {...viewBindings, route: readerRoute};
  const active = readerRoute.view;
  const tabs = (Object.keys(tabLabels) as (keyof typeof tabLabels)[]).filter(tab => tab !== 'downloads' || bindings.downloadsOffered || localUsable);
  const server = <ServerDownloads key={viewKey} service={runtime.downloads} operations={operations} effects={effects} artwork={bindings.preparationArtwork} online={bindings.online} />;
  return <main className={styles.page}>
    {presentation.backdrop && <div className={styles.wash} aria-hidden="true"><AmbientArt source={presentation.backdrop} treatment="hero" /></div>}
    {phone && active in tabLabels ? <SavedPhoneNav title={t(tabLabels[active as keyof typeof tabLabels])} onBack={() => effects.selectRoute({view: 'home'})}
      action={active === 'playlists' && runtime.saved.getSnapshot().projection?.actions?.includes('create_playlist')
        ? <Button variant="icon" size="small" icon="i-plus" iconOnly aria-label={t('web.saved.newPlaylist')} onClick={() => setCreating({viewKey, kind: 'playlist'})} />
        : active === 'collections' ? <Button variant="icon" size="small" icon="i-plus" iconOnly aria-label={t('web.saved.newCollection')} onClick={() => setCreating({viewKey, kind: 'collection'})} /> : undefined} /> : phone ? null : <>
      <header className={styles.header}><h1>{t('saved.title')}</h1></header>
      <Tabs className={styles.tabs} listClassName={styles.tabsList} value={active} aria-label={t('web.saved.sections')} onValueChange={value => { if (value in tabLabels) effects.selectRoute({view: value as keyof typeof tabLabels}); }}
        items={tabs.map(tab => ({value: tab, label: t(tabLabels[tab])}))} />
    </>}
    {route.view === 'downloads' ? localUsable && bindings.localDownloads
      ? <LocalDownloads key={viewKey} port={bindings.localDownloads} operations={operations} effects={effects} server={<ServerDownloads service={runtime.downloads} operations={operations} effects={effects} artwork={bindings.preparationArtwork} online={bindings.online} embedded managed={!!effects.saveReadyPreparation} />} /> : server
      : route.view === 'history' ? <SavedHistory key={viewKey} bindings={dataBindings} />
        : readerRoute.view === 'collections' || readerRoute.view === 'views' ? <PersonalResources key={viewKey} {...dataBindings} onCreate={() => setCreating({viewKey, kind: 'collection'})} />
          : <SavedLists key={viewKey} {...dataBindings} onCreate={() => setCreating({viewKey, kind: 'playlist'})} />}
    <CreateResourceDialog key={`create:${viewKey}`} kind={creating?.viewKey === viewKey ? creating.kind : undefined} bindings={viewBindings} close={() => setCreating(undefined)} />
  </main>;
}
const noSubscribe = () => () => {};
const noLocalSnapshot = () => undefined;

function SavedLists(bindings: SavedViewBindings & {onCreate: () => void}) {
  const {runtime, route, presentation, effects, artwork} = bindings;
  const first = route.view === 'watchlist' || route.view === 'favorites' ? runtime.posters : runtime.saved;
  const primary = useSyncExternalStore(first.subscribe, first.getSnapshot, first.getSnapshot), squares = useSyncExternalStore(runtime.squares.subscribe, runtime.squares.getSnapshot, runtime.squares.getSnapshot);
  const routeMatches = route.view === 'watchlist' || route.view === 'favorites'
    ? sameSavedRoute(primary.route, {...route, shape: 'posters'}) && sameSavedRoute(squares.route, {...route, shape: 'squares'}) : sameSavedRoute(primary.route, route);
  const split = route.view === 'watchlist' || route.view === 'favorites', projection = primary.projection ?? (split ? squares.projection : null);
  const retryRoute = route.view === 'home' || route.view === 'downloads' ? undefined : route;
  const routeKey = retryRoute ? JSON.stringify(retryRoute) : '';
  // The admitted route is the committed route witness for this exact routeKey: the
  // capture pass runs first in this commit, so a later equal-content object cannot
  // replace the route this retry lifetime was built from.
  const routeWitness = useRef(retryRoute);
  useLayoutEffect(() => {routeWitness.current = retryRoute;});
  const postersService = runtime.posters, squaresService = runtime.squares, savedService = runtime.saved;
  const [published, setPublished] = useState<SavedRetryAdmission | undefined>(undefined), [retryBusy, setRetryBusy] = useState(false);
  // A fresh layout-owned admission per exact ticket. Strict setup→cleanup→setup
  // publishes a live admission for this render; an old callback captured the old
  // admission object and never adopts the fresh one.
  useLayoutEffect(() => {
    const route = routeWitness.current;
    if (!route) {setPublished(undefined); return;}
    const ticket: SavedRetryTicket = {runtime, route, routeKey, posters: postersService, squares: squaresService, saved: savedService, reportError: effects.reportError};
    const created = createSavedRetryAdmission(ticket);
    setPublished(created);
    setRetryBusy(false);
    return () => {
      retireSavedRetryAdmission(created);
      setPublished(previous => previous === created ? undefined : previous);
    };
  }, [runtime, routeKey, postersService, squaresService, savedService, effects.reportError]);
  const active = published && published.live && published.ticket.routeKey === routeKey && published.ticket.runtime === runtime
    && published.ticket.posters === postersService && published.ticket.squares === squaresService && published.ticket.saved === savedService
    && published.ticket.reportError === effects.reportError ? published : undefined;
  const retry = useCallback(() => {
    const value = active;
    if (!value || !value.live || value.pending) return;
    if (!savedRetryCurrent(value)) return;
    if (!value.live || value.pending) return;
    const token = Symbol('saved-retry');
    value.pending = token;
    setRetryBusy(true);
    const current = () => {
      if (!value.live || value.pending !== token) return false;
      if (!savedRetryCurrent(value)) return false;
      return value.live && value.pending === token;
    };
    const report = (error: unknown) => {
      if (!value.live || value.pending !== token) return;
      if (!savedRetryCurrent(value)) return;
      if (!value.live || value.pending !== token) return;
      value.ticket.reportError(error);
    };
    const unlock = () => {
      if (!value.live || value.pending !== token) return;
      if (!savedRetryCurrent(value)) {
        if (value.pending === token) value.pending = undefined;
        return;
      }
      if (!value.live || value.pending !== token) return;
      value.pending = undefined;
      setRetryBusy(false);
    };
    const tasks: Promise<void>[] = [];
    const start = (select: () => Promise<void>) => {
      if (!current()) return;
      let task: Promise<void>;
      try {task = select();}
      catch (error) {report(error); return;}
      // A synchronous route/opening replacement stops the sibling before it starts.
      if (!current()) {void task.catch(() => {}); return;}
      tasks.push(task.catch(error => {report(error);}));
    };
    const run = async () => {
      try {
        const exact = value.ticket.route;
        if (exact.view === 'watchlist' || exact.view === 'favorites') {
          if (current() && value.ticket.posters.getSnapshot().phase === 'error') start(() => value.ticket.posters.select({...exact, shape: 'posters'}));
          if (current() && value.ticket.squares.getSnapshot().phase === 'error') start(() => value.ticket.squares.select({...exact, shape: 'squares'}));
        } else if (current()) start(() => value.ticket.saved.select(exact));
        await Promise.all(tasks);
      } catch (error) {
        report(error);
      } finally {
        unlock();
      }
    };
    void run();
  }, [active]);
  const failures = split ? [
    ...(primary.phase === 'error' ? [{id: 'posters'}] : []),
    ...(squares.phase === 'error' ? [{id: 'squares'}] : []),
  ] : [];
  // The frame's wash comes from the list's own first titles. The canonical
  // library treatment carries it; no separate artwork request or tint is made.
  const wash = useMemo(() => {
    if (!presentation.backdrops) return [];
    if (presentation.backdrop) return [presentation.backdrop];
    const urls = (projection?.sections ?? []).flatMap(section => section.entries).flatMap(entry => {
      const media = mediaEntry(entry), url = media && (media.backdropUrl ?? media.posterUrl);
      return url ? [url] : [];
    });
    return [...new Set(urls)].slice(0, 3).flatMap(url => { const source = artwork.ambient(url); return source ? [source] : []; });
  }, [presentation.backdrops, presentation.backdrop, projection, artwork]);
  if (!routeMatches) return null;
  const list = route.view === 'favorites' ? 'favorites' : route.view === 'playlists' ? 'playlists' : 'watchlist';
  const total = split ? primary.phase === 'ready' && squares.phase === 'ready' ? [...(primary.projection?.sections ?? []), ...(squares.projection?.sections ?? [])].reduce((sum, section) => sum + section.totalCount, 0) : undefined : projection?.sections.reduce((sum, section) => sum + section.totalCount, 0);
  const hasContent = !!primary.projection?.sections.some(section => section.entries.length) || split && !!squares.projection?.sections.some(section => section.entries.length);
  if (!split && !projection && primary.phase === 'error') return <StateView title={t('saved.loadFailed')} primary={{id: 'retry', label: t('action.tryAgain'), onPress: retry, loading: retryBusy}} />;
  return <>
    {wash.length ? <div className={styles.wash} aria-hidden="true"><AmbientArt sources={wash} treatment="library" height={640} /></div> : null}
    {failures.length ? <div className={styles.readerFailures} data-saved-reader-failures>
      {failures.map(failure => <StateView key={failure.id} variant="compact" className={styles.readerFailure}
        title={t('saved.listLoadFailed')} primary={{id: `retry-${failure.id}`, label: t('action.tryAgain'), onPress: retry, loading: retryBusy}} />)}
    </div> : null}
    {list === 'playlists'
      ? <div className={styles.toolbar}>
          {projection?.sorts.length ? <SortMenu variant="saved" sorts={projection.sorts.map(sort => ({...sort, defaultDirection: sort.directions[0] ?? 'asc', expensive: false, applicableKinds: []}))}
            value={savedSortSelection(route, projection)} label={playlistSortName}
            onValueChange={value => { if (route.view === 'playlists') effects.selectRoute({...route, sort: value.field, direction: value.direction}); }} /> : null}
          {projection?.filters.length ? <><span className={styles.separator} aria-hidden="true"/>
            {projection.filters.map(filter => {
              const selected = 'filter' in route ? route.filter ?? 'all' : 'all';
              return <div key={filter.id} className={styles.playlistFilters} role="group" aria-label={defaultI18n.has(filter.labelKey) ? t(filter.labelKey) : t('playlists.filter')}>
                {filter.options.map(option => <Chip key={option.id} variant="quick" selected={selected === option.id} count={option.count}
                  onClick={() => { if (route.view === 'playlists') effects.selectRoute({...route, filter: selected === option.id ? 'all' : option.id as PlaylistListFilter}); }}>{option.label}</Chip>)}
              </div>;
            })}</> : null}
          {bindings.catalogueSelection?.toolbar ?? bindings.selection?.toolbar}
          <div className={styles.toolbarEnd}>
            {total !== undefined && <span>{t('web.saved.playlists', {count: total})}</span>}
            {projection?.actions?.includes('create_playlist') && <Button className={styles.newPlaylist} icon="i-plus" size="small" onClick={bindings.onCreate}>{t('web.saved.newPlaylist')}</Button>}
          </div>
        </div>
      : <SavedListToolbar bindings={bindings} projection={projection} total={total} />}
    {!hasContent && primary.phase === 'ready' && (!split || squares.phase === 'ready') ? <div className={styles.empty}><StateView title={t(`web.saved.empty.${list}`)} body={t(`web.saved.empty.${list}Body`)} /></div> : null}
    {primary.projection?.sections.map(section => <SavedSection key={`posters:${section.id}`} {...bindings} service={first} section={section} />)}
    {split && squares.projection?.sections.map(section => <SavedSection key={`squares:${section.id}`} {...bindings} service={runtime.squares} section={section} heading={{label: s('next.saved.musicAndAudiobooks'), count: section.totalCount}} />)}
    {list === 'playlists' && projection?.actions?.includes('create_playlist') && <div className={styles.newTile}><NewPlaylistCard model={{title: t('web.saved.newPlaylist'), caption: []}} onCreate={bindings.onCreate} /></div>}
  </>;
}

/** The catalogue names the server's saved sorts; an unknown one keeps the fallback. */
const savedSortKeys = {title: 'web.saved.sort.title', added: 'web.saved.sort.added', updated: 'web.saved.sort.recent', year: 'web.saved.sort.year', duration: 'web.saved.sort.duration', progress: 'web.saved.sort.progress'} as const;
function savedSortName(sort: Readonly<{id: string; labelKey: string}>): string {
  const key = savedSortKeys[sort.id as keyof typeof savedSortKeys];
  return key ? t(key) : defaultI18n.has(sort.labelKey) ? t(sort.labelKey) : t('web.saved.sort.other');
}
/** The playlist directory's own orders are the catalogue's playlist words: Name,
 * Date created, Last changed and Number of items (collections.html 1 and 14a). */
const playlistSortFields = {title: 'title', created: 'created', updated: 'updated', count: 'count'} as const;
function playlistSortName(sort: Readonly<{id: string; labelKey: string}>): string {
  const field = playlistSortFields[sort.id as keyof typeof playlistSortFields];
  return field ? t('playlists.sort', {field}) : defaultI18n.has(sort.labelKey) ? t(sort.labelKey) : t('web.saved.sort.other');
}
/** The saved list's own order, as the server published it. */
function savedSortSelection(route: SavedViewBindings['route'], projection: SavedProjection | null) {
  const field = ('sort' in route ? route.sort : undefined) ?? projection?.query.sort ?? 'title';
  const direction = (('direction' in route ? route.direction : undefined) ?? projection?.query.direction) === 'desc' ? 'desc' as const : 'asc' as const;
  const selected = projection?.sorts.find(sort => sort.id === field);
  return {field, direction, flippable: (selected?.directions.length ?? 0) > 1, reshufflable: false};
}

/** My List and Favorites: the library toolbar the frame draws, over the saved
 * list's published sorts and filter. The rows in the frame that the saved
 * contract does not publish (Watched, Favorites as a quick filter) are absent
 * rather than faked; the Filters menu carries the same published options. */
function SavedListToolbar({bindings, projection, total}: {bindings: SavedViewBindings; projection: SavedProjection | null; total: number | undefined}) {
  const {route, effects, presentation} = bindings;
  const listRoute = route.view === 'watchlist' || route.view === 'favorites' ? route : undefined;
  const filter = listRoute?.filter ?? 'all', [open, setOpen] = useState(false);
  const filters = projection?.filters ?? [], quick = filters.flatMap(entry => entry.options.filter(option => option.id !== 'all'));
  const sorts = projection?.sorts ?? [], sort = savedSortSelection(route, projection);
  const sortLabel = (value: Readonly<{id: string; labelKey: string}>) => savedSortName(value);
  return <div className={styles.toolbar}>
    {filters.length ? <>
      <Popover open={open} onOpenChange={setOpen} title={t('next.browse.filters')} accessibilityLabel={t('next.browse.filters')} placement="bottom-start" width={260}
        trigger={<FiltersButton count={filter === 'all' ? 0 : 1} />}>
        {quick.map(option => <MenuRow key={option.id} selected={filter === option.id} onClick={() => { setOpen(false); if (listRoute) effects.selectRoute({...listRoute, filter: filter === option.id ? 'all' : option.id as SavedListFilter}); }}>{option.label}</MenuRow>)}
      </Popover>
      <span className={styles.separator} aria-hidden="true" />
      {quick.map(option => <Chip key={option.id} variant="toolbar" selected={filter === option.id} onClick={() => { if (listRoute) effects.selectRoute({...listRoute, filter: filter === option.id ? 'all' : option.id as SavedListFilter}); }}>{option.label}</Chip>)}
    </> : null}
    {bindings.catalogueSelection?.toolbar ?? bindings.selection?.toolbar}
    <div className={styles.toolbarEnd}>
      {total !== undefined && <span className={styles.resultCount}>{t('lib.count', {count: total, pivot: 'other'})}</span>}
      {sorts.length ? <SortMenu sorts={sorts.map(value => ({...value, defaultDirection: value.directions[0] ?? 'asc', expensive: false, applicableKinds: []}))} value={sort} label={sortLabel}
        onValueChange={value => { if (listRoute) effects.selectRoute({...listRoute, sort: value.field, direction: value.direction}); }} /> : null}
      <ViewToggle value={presentation.view} onValueChange={presentation.setView} />
    </div>
  </div>;
}

export function SavedSection(bindings: SavedViewBindings & {service: SavedService; section: SavedProjection['sections'][number]; heading?: Readonly<{label: string; count?: number}>; onMore?: (entry: SavedEntry, anchor: HTMLButtonElement) => void; orderHandle?: (entryId: string, title: string) => ReactNode; heldEntryId?: string}) {
  const {service, section, artwork, effects, presentation, selection} = bindings;
  const collection = useMemo(() => savedSectionWindow(service, section.id), [service, section.id]);
  const cleanup = useRef<(() => void) | undefined>(undefined);
  const root = useRef<HTMLElement>(null), [width, setWidth] = useState(0);
  useEffect(() => {
    const element = root.current; if (!element) return;
    const read = () => setWidth(element.clientWidth);
    const observer = new ResizeObserver(read); observer.observe(element); read();
    return () => observer.disconnect();
  }, []);
  useEffect(() => () => { cleanup.current?.(); cleanup.current = undefined; }, [collection]);
  const onVisibleRange = useCallback((first: number, last: number) => { cleanup.current?.(); cleanup.current = selection?.registerWindow(service, section.id, first, last); }, [selection, service, section.id]);
  const first = section.entries[0], media = first && mediaEntry(first), square = first?.kind === 'playlist' || media && shapeFor(media.kind) === 'square';
  // collections.html 14a: at phone width the playlist directory is the frame's
  // list of rows, not the tile grid. The selection's own live mode decides: while
  // it is active the tiles stay, because its circles and range previews live there.
  const selecting = useCatalogueSelectionActive(bindings.catalogueSelection);
  const phoneRows = width > 0 && width < 600 && !selecting && section.entries.length > 0 && section.entries.every(entry => entry.kind === 'playlist');
  const oneColumn = presentation.view === 'list' || phoneRows;
  const resourceRows = bindings.route.view === 'playlist' && presentation.view === 'list';
  const ratio = square ? 1 : 2 / 3, rowHeight = phoneRows ? 80 : presentation.view === 'list' ? resourceRows ? 88 : 90 : presentation.cardWidth / ratio + 48;
  const columns = oneColumn ? 1 : Math.max(1, Math.floor((width + 16) / (presentation.cardWidth + 16)));
  const rows = Math.ceil(section.entries.length / columns);
  const height = Math.max(rowHeight, Math.min(window.innerHeight, rows * rowHeight + Math.max(0, rows - 1) * 16));
  return <section className={styles.savedSection} ref={root}>
    {bindings.heading && <div className={styles.sectionHeading}><h2>{bindings.heading.label}</h2>{bindings.heading.count !== undefined && <span>{defaultI18n.number(bindings.heading.count)}</span>}</div>}
    <WindowedGrid collection={collection} pinnedIndex={bindings.heldEntryId ? section.entries.findIndex(entry => entry.id === bindings.heldEntryId) : undefined} label={bindings.heading?.label ?? serverTextLabel(defaultI18n, section.heading)} height={height} cardWidth={oneColumn ? Math.max(1, width) : presentation.cardWidth} rowHeight={rowHeight} rowGap={oneColumn ? 0 : 16} gap={phoneRows ? 0 : 16} reducedMotion={presentation.reducedMotion} onVisibleRange={onVisibleRange}
      renderPlaceholder={() => phoneRows ? <Skeleton width={Math.max(1, width)} height={72} /> : <Skeleton width={presentation.cardWidth} height={presentation.cardWidth / ratio} />}
      renderItem={(entry, context) => {
        if (entry.kind === 'reading_group') return <ReadingGroupCard card={entry} service={service} runtime={bindings.runtime} operations={bindings.operations} artwork={artwork} width={presentation.cardWidth} open={effects.openReadingGroup} reportError={effects.reportError}/>;
        const cardSelection = selection?.card(service, section.id, entry), media = mediaEntry(entry);
        if (entry.kind === 'playlist') {
          // A generic content entry may also name a playlist; the card's own two
          // optional facts are read only when the entry actually carries them.
          const pinned = 'pinned' in entry && entry.pinned === true;
          const lastChanged = 'lastChanged' in entry && typeof entry.lastChanged === 'string' ? entry.lastChanged : undefined;
          const covers = (entry.artworkPaths ?? []).map(path => artwork.foreground(path, phoneRows ? 64 : presentation.cardWidth, 1)).filter((source): source is ArtworkSource => !!source);
          const changed = playlistChangedLabel(lastChanged);
          if (phoneRows) return <PlaylistRow first={context.index === 0} title={entry.title} count={entry.count} pinned={pinned} lastChanged={lastChanged} covers={covers} open={() => effects.selectRoute({view: 'playlist', playlistId: entry.id})} />;
          const caption = [
            ...(entry.count !== undefined ? [{id: 'count', role: 'value' as const, text: t('web.saved.items', {count: entry.count})}] : []),
            ...(changed ? [{id: 'changed', role: 'context' as const, text: changed}] : []),
          ];
          return <PlaylistMosaic model={{title: entry.title, caption}} covers={covers} context="grid" selection={cardSelection} onNavigate={() => effects.selectRoute({view: 'playlist', playlistId: entry.id})} onMore={effects.openMore ? anchor => effects.openMore!(service, section.id, entry, anchor) : undefined} />;
        }
        if (!media) return <StateView title={t('card.unavailable')} />;
        const selectable = bindings.catalogueSelection?.member(media);
        return <CatalogueSelectionEntry selection={selectable ? bindings.catalogueSelection : undefined} entry={selectable ?? media} position={selectable ? bindings.catalogueSelection!.position(selectable) : context.index}>{documentSelection => {
        const common = {model: homeCardModel(media, false, {t, locale: defaultI18n.region.formatLocale}), artwork: artwork.foreground(media.posterUrl, presentation.cardWidth * devicePixelRatio, ratio),
          selection: documentSelection ?? cardSelection, context: 'grid' as const, watched: media.watched, onNavigate: () => effects.openEntry(media),
          onPlay: media.playback && media.available !== false && !media.missing ? () => { void bindings.operations.run(signal => effects.playEntry(media, signal)).catch(effects.reportError); } : undefined,
          onMore: bindings.onMore ? (anchor: HTMLButtonElement) => bindings.onMore!(entry, anchor)
            : effects.openMore ? (anchor: HTMLButtonElement) => effects.openMore!(service, section.id, entry, anchor) : undefined};
        if (resourceRows) return <ResourceRow {...common} entryId={entry.id} media={media} artwork={artwork} handle={bindings.orderHandle?.(entry.id, common.model.title)}
          ordinal={section.start + context.index + 1} library={bindings.libraries?.find(library => library.id === media.libraryId)?.name}/>;
        if (presentation.view === 'list') return <div className={styles.mediaListRow}><PosterCard {...common} /></div>;
        return <BrowseEntryCard entry={media} artwork={artwork} env={{t, locale: defaultI18n.region.formatLocale}} width={presentation.cardWidth} mixed visible order={context.index} group={`saved:${section.id}`} backdrops={presentation.backdrops}
          actions={{onOpen: common.onNavigate, onPlay: common.onPlay, onMore: common.onMore, selection: documentSelection ?? cardSelection}} />;
        }}</CatalogueSelectionEntry>;
      }} />
    {section.nextCursor && <Button className={styles.showMore} size="small" loading={service.getSnapshot().phase === 'loading'} onClick={() => { void service.next(section.id); }}>{t('action.showMore')}</Button>}
  </section>;
}
/** collections.html 1 and 14a name a playlist's own last change by day. */
const playlistChangedDay = new Intl.DateTimeFormat(defaultI18n.region.uiLocale, {month: 'short', day: 'numeric'});
function playlistChangedLabel(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const changed = new Date(value); if (!Number.isFinite(changed.getTime())) return undefined;
  const now = new Date(), today = new Date(now.getFullYear(), now.getMonth(), now.getDate()), day = new Date(changed.getFullYear(), changed.getMonth(), changed.getDate());
  return day.getTime() === today.getTime() ? t('saved.today') : playlistChangedDay.format(day);
}
/** collections.html 14a: a playlist at phone width is one row, its 2 by 2 mosaic
 * on the left, the name, its count, its own last change and this viewer's pin
 * beside it, and the chevron on the right. The rows sit in individual virtual
 * cells, so each draws its own inset divider and the first one leaves it out. */
function PlaylistRow({first, title, count, pinned, lastChanged, covers, open}: {first: boolean; title: string; count: number | undefined; pinned: boolean; lastChanged: string | undefined; covers: readonly ArtworkSource[]; open: () => void}) {
  const changed = playlistChangedLabel(lastChanged);
  return <button type="button" className={styles.playlistRow} data-first={first || undefined} onClick={open}>
    <span className={styles.playlistRowArt}><CoverMosaic covers={covers} shape="square" alt="" /></span>
    <span className={styles.playlistRowCopy}><b>{title}</b><small>{count !== undefined && <span>{t('web.saved.items', {count})}</span>}{changed && <span>{changed}</span>}</small></span>
    {pinned && <span className={styles.playlistRowPin} role="img" aria-label={t('pins.heading')}><Icon name="i-bookmark-f" size={16} /></span>}
    <Icon name="i-right" size={18} className={styles.playlistRowChevron} />
  </button>;
}
function mediaEntry(entry: SavedEntry): ContentEntry | undefined {
  return entry.kind === 'playlist' || entry.kind === 'reading_group' ? undefined : entry.kind === 'playlist_entry' ? entry.hidden ? undefined : entry.media : entry;
}

function PersonalResources(bindings: SavedViewBindings & {onCreate: () => void}) {
  const service = bindings.runtime.personal, snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot), kind = bindings.route.view === 'views' ? 'views' : 'collections';
  const summary = useSyncExternalStore(bindings.runtime.summary.subscribe, bindings.runtime.summary.getSnapshot, bindings.runtime.summary.getSnapshot);
  const phone = useShellWidth() < 600;
  const count = kind === 'views' ? summary.value?.savedViews : summary.value?.collections;
  if (!sameSavedRoute(snapshot.route, bindings.route)) return null;
  return <>
    <div className={styles.toolbar}>{kind === 'collections' ? <><Button size="small" icon="i-plus" onClick={bindings.onCreate}>{t('web.saved.newCollection')}</Button><div className={styles.toolbarEnd}>{count !== undefined && t('lib.count', {count, pivot: 'collections'})}</div></>
      : <>{count !== undefined && <span className={styles.directoryCount}>{s('next.saved.savedViewsCount', {count})}</span>}<span className={styles.directoryHelp}>{s('next.saved.viewsHelp')}</span></>}</div>
    {snapshot.error && !snapshot.resources.length && <StateView title={t('saved.loadFailed')} primary={{id: 'retry', label: t('action.tryAgain'), onPress: () => { void service.retryRead(); }}} />}
    {!snapshot.loading && !snapshot.error && !snapshot.resources.length && <StateView title={t(`web.saved.empty.${kind}`)} body={t(`web.saved.empty.${kind}Body`)} />}
    <div className={phone ? styles.resourceList : styles.resources}>{snapshot.resources.map(resource => phone
      ? <PersonalResourceRow key={resource.id} resource={resource} bindings={bindings}/>
      : <PersonalResourceTile key={resource.id} resource={resource} bindings={bindings}/>)}</div>
    {snapshot.nextCursor && <Button className={styles.showMore} size="small" loading={snapshot.loading} onClick={() => { void service.next(); }}>{t('action.showMore')}</Button>}
  </>;
}
/** collections.html 10 and 14: at phone width a collection or saved view is a row.
 * A collection names who can see it: everyone on the server, only you, or the
 * owner's share. A saved view marks its place on Home. */
function PersonalResourceRow({resource, bindings}: {resource: PersonalResource; bindings: SavedViewBindings}) {
  const covers = (resource.artworkPaths ?? []).map(path => bindings.artwork.foreground(path, 64, 1)).filter((source): source is ArtworkSource => !!source);
  const collection = resource.kind === 'collection', shared = collection && resource.role !== 'owner', visible = collection && resource.visibility === 'server';
  return <button type="button" className={styles.resourceRow} onClick={() => bindings.effects.selectRoute({view: 'resource', resourceId: resource.id})}>
    <span className={styles.resourceRowArt}><CoverMosaic covers={covers} shape="square" alt="" /></span>
    <span className={styles.resourceRowCopy}><b>{resource.name}</b>
      <small>{collection ? t('lib.count', {count: resource.entryCount, pivot: 'collections'}) : t('lib.count', {count: resource.entryCount, pivot: 'other'})}</small>
      {shared ? <small className={styles.resourceRowNote}><Icon name="i-people" size={13}/>{t('web.saved.sharedByOwner')}</small>
        : collection ? <small className={visible ? styles.resourceRowVisible : styles.resourceRowNote}><Icon name={visible ? 'i-people' : 'i-lock'} size={13}/>{t(visible ? 'web.saved.visibleToEveryone' : 'web.saved.onlyYou')}</small>
          : resource.onHome ? <small className={styles.resourceRowVisible}><Icon name="i-home" size={13}/>{s('next.saved.onHome')}</small> : null}
    </span>
    <Icon name="i-right" size={18} className={styles.playlistRowChevron} />
  </button>;
}
function PersonalResourceTile({resource, bindings}: {resource: PersonalResource; bindings: SavedViewBindings}) {
  const covers = (resource.artworkPaths ?? []).map(path => bindings.artwork.foreground(path, bindings.presentation.cardWidth, 1)).filter((source): source is ArtworkSource => !!source);
  return <PlaylistMosaic covers={covers} model={{title: resource.name, caption: [{id: 'count', role: 'value', text: t('web.saved.items', {count: resource.entryCount})}]}}
    context="grid" onNavigate={() => bindings.effects.selectRoute({view: 'resource', resourceId: resource.id})} />;
}

function CreateResourceDialog({kind, bindings, close}: {kind: 'playlist' | 'collection' | undefined; bindings: SavedViewBindings; close: () => void}) {
  const [name, setName] = useState(''), [summary, setSummary] = useState(''), [shared, setShared] = useState(false), [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => { setName(''); setSummary(''); setShared(false); }, [kind]);
  const create = () => {
    if (!kind || busy || !name.trim() || !bindings.operations.current) return;
    setBusy(true);
    void bindings.operations.run(async () => {
      if (kind === 'playlist') await bindings.runtime.saved.mutate({action: 'create', name: name.trim(), summary});
      else if (!await bindings.runtime.personal.mutate({action: 'create', kind: 'collection', name: name.trim(), summary, visibility: shared ? 'server' : 'private'})) throw bindings.runtime.personal.getSnapshot().mutationError;
    }, () => { setBusy(false); close(); }).catch(error => { setBusy(false); bindings.effects.reportError(error); });
  };
  return <SavedDialog busy={busy} open={!!kind} onOpenChange={open => { if (!open && !busy) close(); }} title={t(kind === 'collection' ? 'web.saved.newCollection' : 'web.saved.newPlaylist')} initialFocusRef={input}
    footer={<><Button size="small" disabled={busy} onClick={close}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={busy} disabled={!name.trim()} onClick={create}>{t('web.saved.create')}</Button></>}>
    <form onSubmit={event => { event.preventDefault(); create(); }}><ResourceFields nameRef={input} name={name} summary={summary} onNameChange={setName} onSummaryChange={setSummary} nameMaxLength={kind === 'collection' ? 400 : 200} summaryMaxLength={kind === 'collection' ? 8000 : 4000} disabled={busy} />
      {kind === 'collection' && <><label className={styles.visibility}><span>{t('web.saved.visibleToAll')}</span><Switch checked={shared} disabled={busy} onCheckedChange={setShared} /></label><p className={styles.visibilityHelp}>{t(shared ? 'web.saved.sharedHelp' : 'web.saved.privateHelp')}</p></>}
    </form>
  </SavedDialog>;
}
