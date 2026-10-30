import {forwardIdentity} from '../detail/context';
import {createSavedCatalogueSelection, savedCatalogueEntry} from './catalogue-selection';
import {useRef, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {useSearch} from '@tanstack/react-router';
import type {ContentEntry} from '@core/library-content.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {useShellWidth} from '../../shell/viewport';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import {useScreenAppearance} from '../browse/screen-preferences';
import {SavedScreen, type SavedEffects} from './Saved';
import {SavedHost, useRetainedSavedOwner} from './SavedHost';
import type {RetainedSavedOwner} from './retained-owner';
import {parseSavedScreenRoute, savedRouteKey, sameSavedRoute} from './route';
import {SavedOperations} from './operations';
import type {ScreenContext} from '../detail/context';
import {savedReaderRoute, type SavedScreenRoute} from './runtime';

const networkSnapshot = () => navigator.onLine;
const networkSubscribe = (changed: () => void) => {
  window.addEventListener('online', changed);window.addEventListener('offline', changed);
  return () => {window.removeEventListener('online', changed);window.removeEventListener('offline', changed);};
};
function savedLocationCurrent(key: string) {
  const location = router.state.location;
  if (location.pathname !== '/saved') return false;
  try {return savedRouteKey(parseSavedScreenRoute(location.search)) === key;} catch {return false;}
}
function savedReadersMatch(runtime: RetainedSavedOwner['runtime'], route: SavedScreenRoute) {
  const reader = savedReaderRoute(route);
  if (reader.view === 'downloads') return true;
  if (reader.view === 'watchlist' || reader.view === 'favorites') return sameSavedRoute(runtime.posters.getSnapshot().route, {...reader, shape: 'posters'})
    && sameSavedRoute(runtime.squares.getSnapshot().route, {...reader, shape: 'squares'});
  return sameSavedRoute(['collections', 'views', 'history', 'resource'].includes(reader.view) ? runtime.personal.getSnapshot().route : runtime.saved.getSnapshot().route, reader);
}
type SavedRouteLease = Readonly<{context: ScreenContext; owner: RetainedSavedOwner; route: SavedScreenRoute; routeKey: string;
  playback: NonNullable<ProtectedLibraryScreen['binding']['playback']>['runtime'] | undefined;
  current: () => boolean; signal: (input?: AbortSignal) => AbortSignal; selection: ReturnType<typeof createSavedCatalogueSelection>;
  viewRuntime: RetainedSavedOwner['runtime']}>;
function BoundSaved({captured, owner}: {captured: ProtectedLibraryScreen; owner: RetainedSavedOwner}) {
  const appearance = useScreenAppearance(captured.context)!, search = useSearch({from: '/saved'});
  const route = useMemo(() => parseSavedScreenRoute(search), [search]), routeKey = savedRouteKey(route);
  const context = useCommittedLibraryScreenContext(captured, routeKey, () => savedLocationCurrent(routeKey));
  const playback = captured.binding.playback?.runtime;
  const [choice, setChoice] = useState<Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>>();
  const [published, publish] = useState<SavedRouteLease>();
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({route});
  useLayoutEffect(() => {setupPorts.current = {route};});
  useLayoutEffect(() => {
    const {route} = setupPorts.current;
    if (!context?.isCurrent() || !owner.runtime.operations.current) return;
    const viewer = captured.context.session().viewer;
    if (viewer.authority !== 'local' && viewer.authority !== 'hosted') return;
    const lifetime = new AbortController(), originalRoute = Object.freeze({...route});
    const originalReader = Object.freeze({...savedReaderRoute(originalRoute)});
    const current = () => !lifetime.signal.aborted && context.isCurrent() && owner.runtime.operations.current && savedLocationCurrent(routeKey);
    const signal = (input?: AbortSignal) => input ? AbortSignal.any([lifetime.signal, input]) : lifetime.signal;
    const original: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const operations = new SavedOperations(current), borrowed = owner.runtime;
    const selection = createSavedCatalogueSelection(original, borrowed, savedReaderRoute(originalRoute),
      {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId},
      () => captured.context.viewerFence, current, playback);
    const select: typeof borrowed.select = async (value, restore) => {
      // SavedScreen passes its exact normalized reader to a retry (bare /saved
      // remains the home URL). Admit only those two captured representations.
      if (!current() || !sameSavedRoute(value, originalRoute) && !sameSavedRoute(value, originalReader)) throw new DOMException('Saved route changed', 'AbortError');
      await borrowed.select(originalRoute, restore);
      if (!current()) throw new DOMException('Saved route changed', 'AbortError');
    };
    // Borrow the actual fields and live getters, including READY readiness.
    // This only retires route commands; retained transports keep their owner.
    const methods = new Map<PropertyKey, Readonly<{original: unknown; bound: unknown}>>();
    const viewRuntime = new Proxy(borrowed, {get(target, property) {
      if (property === 'operations') return operations;
      if (property === 'select') return select;
      const value = Reflect.get(target, property, target);
      if (typeof value !== 'function') return value;
      const held = methods.get(property);
      if (held && held.original === value) return held.bound;
      const bound = value.bind(target);methods.set(property, {original: value, bound});return bound;
    }});
    selection.start();
    // Align a genuinely new route before its rows can draw. A cached matching
    // route keeps its readers and drafts; the screen's passive load is an echo.
    if (!savedReadersMatch(borrowed, originalRoute)) void select(originalRoute).catch(error => {if (current()) context.effects.reportError(error);});
    publish({context, owner, route: originalRoute, routeKey, playback, current, signal, selection, viewRuntime});
    // Only the route's work retires. Parent readers, drafts, READY/native work
    // and the retained transport remain owned by SavedHost.
    return () => {lifetime.abort();operations.dispose();selection.dispose();};
  }, [context, owner, routeKey, captured.context, playback]);
  const owned = context && published && published.context === context && published.owner === owner && published.routeKey === routeKey
    && published.playback === playback && published.current() ? published : undefined;
  return owned ? <CommittedSaved captured={captured} lease={owned} appearance={appearance} choice={choice} setChoice={setChoice}/> : null;
}
function CommittedSaved({captured, lease, appearance, choice, setChoice}: {captured: ProtectedLibraryScreen; lease: SavedRouteLease;
  appearance: NonNullable<ReturnType<typeof useScreenAppearance>>; choice?: Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>;
  setChoice: (choice: Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>) => void}) {
  const {context, owner, route, selection, playback} = lease, runtime = owner.runtime;
  const native = useSyncExternalStore(owner.subscribe, owner.getSnapshot, owner.getSnapshot);
  const saved = useSyncExternalStore(runtime.saved.subscribe, runtime.saved.getSnapshot, runtime.saved.getSnapshot);
  const posters = useSyncExternalStore(runtime.posters.subscribe, runtime.posters.getSnapshot, runtime.posters.getSnapshot);
  const squares = useSyncExternalStore(runtime.squares.subscribe, runtime.squares.getSnapshot, runtime.squares.getSnapshot);
  const personal = useSyncExternalStore(runtime.personal.subscribe, runtime.personal.getSnapshot, runtime.personal.getSnapshot);
  const downloads = useSyncExternalStore(runtime.downloads.subscribe, runtime.downloads.getSnapshot, runtime.downloads.getSnapshot);
  const online = useSyncExternalStore(networkSubscribe, networkSnapshot, () => true), width = useShellWidth();
  const effects: SavedEffects = useMemo(() => {
    // One canonical data route for every witness: `home` is the UI identity only.
    const dataRoute = savedReaderRoute(route);
    const documents = dataRoute.view === 'playlist' ? [{service: runtime.saved, state: saved, expected: dataRoute}]
      : dataRoute.view === 'watchlist' || dataRoute.view === 'favorites' ? [
        {service: runtime.posters, state: posters, expected: {...dataRoute, shape: 'posters' as const}},
        {service: runtime.squares, state: squares, expected: {...dataRoute, shape: 'squares' as const}}] : [];
    const documentCurrent = (held: typeof documents[number]) => {
      const now = held.service.getSnapshot();
      return lease.current() && held.state.phase === 'ready' && !!held.state.projection && now.phase === 'ready'
        && now.projection === held.state.projection && sameSavedRoute(now.route, held.expected);
    };
    const personalCurrent = () => {
      const now = runtime.personal.getSnapshot();
      return lease.current() && !personal.loading && !personal.error && !now.loading && !now.error
        && sameSavedRoute(now.route, dataRoute) && now.entries === personal.entries && now.resource === personal.resource
        && now.revision === personal.revision && now.viewerFence === personal.viewerFence;
    };
    const member = (entry: ContentEntry) => documents.some(held => documentCurrent(held)
      && held.state.projection!.sections.some(section => section.entries.some(value => savedCatalogueEntry(value) === entry)))
      || (dataRoute.view === 'resource' || dataRoute.view === 'history') && personalCurrent() && personal.entries.some(value => !value.hidden && value.media === entry);
    const reportError = (error: unknown) => {if (lease.current()) context.effects.reportError(error);};
    return {
      openEntry(entry) {if (member(entry)) context.effects.openEntry(entry);},
      openReadingGroup(card) {
        if (dataRoute.view !== 'watchlist' || !documents.some(held => documentCurrent(held)
          && held.state.projection!.sections.some(section => section.entries.some(value => value === card)))) return;
        void router.navigate({to: '/library/$libraryId', params: {libraryId: card.libraryId}, search: {view: 'series', entityId: card.readingGroup.seriesId,
          ...(card.navigation.pivot ? {pivot: card.navigation.pivot} : {})}}).catch(reportError);
      },
      async playEntry(entry, input) {
        // A station is its seed, never an ordinary item playback authority.
        if (entry.kind === 'radio' || !member(entry) || !entry.libraryId || !entry.playback || entry.available === false || entry.missing)
          throw new DOMException('Saved playback changed', 'AbortError');
        await context.effects.play({libraryId: entry.libraryId, itemId: entry.playback.itemId, startSeconds: entry.playback.startSeconds ?? 0},
          lease.signal(input), () => member(entry));
      },
      selectRoute(value) {if (lease.current()) void router.navigate({to: '/saved', search: value}).catch(reportError);},
      openItem(itemId) {if (lease.current()) void router.navigate({to: '/item/$itemId', params: {itemId}}).catch(reportError);},
      async saveFile(preparation, input) {
        const active = lease.signal(input), current = () => lease.current() && runtime.downloads.getSnapshot().items.includes(preparation) && downloads.items.includes(preparation);
        active.throwIfAborted();if (!current()) throw new DOMException('Saved download changed', 'AbortError');
        const grant = await runtime.downloads.grant(preparation);
        active.throwIfAborted();if (!current()) throw new DOMException('Saved download changed', 'AbortError');
        window.location.assign(context.api.mediaUrl(grant.url));
      },
      reportError,
      ...(native.saveReadyPreparation ? {saveReadyPreparation: native.saveReadyPreparation} : {}),
      ...(playback ? {async playResource(target: Parameters<NonNullable<SavedEffects['playResource']>>[0], input: AbortSignal) {
        const resource = target.kind === 'playlist' ? saved.playlist : personal.resource;
        const current = () => lease.current() && !!resource && resource.id === target.id
          && (target.kind === 'playlist' ? runtime.saved.getSnapshot().playlist === resource && sameSavedRoute(runtime.saved.getSnapshot().route, route)
            : personalCurrent() && personal.resource?.kind === target.kind);
        if (!current()) throw new DOMException('Saved resource changed', 'AbortError');
        await playback.playFromQueue(target.kind === 'view' ? {saved: {id: target.id}} : {container: {kind: target.kind, id: target.id}},
          {signal: lease.signal(input), isCurrent: current}, {shuffle: target.shuffle});
      }} : {}),
    };
  }, [context, lease, runtime, route, playback, saved, posters, squares, personal, downloads, native.saveReadyPreparation]);
  return <SavedScreen catalogueSelection={selection} runtime={lease.viewRuntime} localDownloads={native.localDownloads} route={route} online={online} downloadsOffered artwork={context.artwork}
    libraries={context.identity?.libraries}
    preparationArtwork={preparation => {
      if (!lease.current()) return undefined;
      // Reuse only authorized bounded Home facts, with no catalogue scan.
      const find = (entries: readonly ContentEntry[]) => entries.find(entry => entry.id === preparation.itemId && entry.libraryId === preparation.libraryId);
      const document = captured.context.home.getSnapshot().document;
      const entry = document && (find(document.spotlight.map(slide => slide.entry)) ?? document.rows.flatMap(row => row.entries).find(entry => entry.id === preparation.itemId && entry.libraryId === preparation.libraryId));
      return context.artwork.foreground(entry?.posterUrl, 120, preparation.title.kind === 'book' || preparation.title.kind === 'album' || preparation.title.kind === 'song' ? 1 : 2 / 3);
    }}
    presentation={{view: choice?.defaultView === appearance.libraryView ? choice.view : appearance.libraryView,
      cardWidth: (width < 600 ? 112 : 173.5) * appearance.cardSizePercent / 100, backdrops: appearance.showBackdrops, reducedMotion: appearance.reducedMotion,
      setView: view => {if (lease.current()) setChoice({defaultView: appearance.libraryView, view});}}}
    effects={effects}/>;
}
function SavedRouteScreen() {
  const captured = useProtectedWebScreenContext();
  return captured ? <SavedRouteOwner captured={captured}/> : null;
}
function SavedRouteOwner({captured}: {captured: ProtectedLibraryScreen}) {
  const owner = useRetainedSavedOwner(captured.context);
  return owner ? <BoundSaved captured={captured} owner={owner}/> : null;
}
export default defineWebScreen({id: 'saved', component: SavedRouteScreen, retainedHost: SavedHost, validateSearch: parseSavedScreenRoute});
