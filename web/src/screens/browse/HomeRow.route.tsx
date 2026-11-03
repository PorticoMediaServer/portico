import {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {entryStartDisplay} from '../../platform/playback/subject';
import {useLocation, useParams, useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import {fetchHomeRow, type HomeRow} from '@core/home.ts';
import type {BrowseSortSelection} from '@core/browse.ts';
import {acrossCondition, acrossPivot, groupOrderSort} from '@core/presentation/browse-groups.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {GridSkeleton} from '../../kit/states';
import {useLoadingClock} from '../detail/Loading';
import type {AmbientArtSource} from '../../kit/artwork';
import {useShellWidth} from '../../shell/viewport';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {ScreenProblem} from '../detail/ScreenProblem';
import {useBrowseLease, type BrowseLease} from './useBrowseLease';
import type {LibraryBrowseBindings} from './Library';
import {HomeRowScreen} from './HomeRow';
import {useScreenAppearance} from './screen-preferences';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from './screen-context';
import {parseHomeRowScreenSearch, type HomeRowScreenSearch} from './route-search';
import {addressedQuery, addressedQueryKey, addressedSearch, appliedSorts, type AddressedQuery} from './address-query';

/** Best match is the row's own server order; the frame draws no other default. */
const bestMatch: readonly BrowseSortSelection[] = [{field: groupOrderSort, direction: 'asc'}];

function LoadedHomeRow({captured, context, row}: {captured: ProtectedLibraryScreen; context: ScreenContext; row: HomeRow}) {
  const width = useShellWidth();
  // Frame 47's toolbar changes are this page's real state, so its own address carries
  // them: a reload, a share and Back from a title land on the same filtered grid. The
  // row's membership and the viewer's grant stay server-owned; only the toolbar's
  // scoped filters and order are read back from the route. The grid opens on the
  // address's query and then follows the address, whoever moved it.
  const rawSearch = useSearch({from: '/home/rows/$rowId'});
  const routed = useMemo(() => parseHomeRowScreenSearch(rawSearch), [rawSearch]);
  const [choice, setChoice] = useState<Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>>();
  const [size, setSize] = useState<'small' | 'medium' | 'large'>('medium');
  const pathname = useLocation({select: location => location.pathname}), runtime = captured.binding.playback?.runtime;
  const sourceKey = JSON.stringify({libraryId: '*', pivot: acrossPivot('homeRow'), homeRowId: row.id, base: [acrossCondition('homeRow', row.id)]});
  const owner = useBrowseLease(context, sourceKey, pathname, () => router.state.location.pathname === pathname, runtime, addressedQuery(routed, bestMatch));
  const presentation = {choice, setChoice, size, setSize};
  return owner ? <CommittedHomeRow key={owner.id} captured={captured} context={context} row={row} owner={owner} routed={routed} presentation={presentation}/>
    : <GridSkeleton elapsedMs={0} window={{width, height: window.innerHeight, scrollTop: 0}} itemCount={1}/>;
}
function CommittedHomeRow({captured, context, row, owner, routed, presentation}: {captured: ProtectedLibraryScreen; context: ScreenContext; row: HomeRow;
  owner: BrowseLease; presentation: {choice?: Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>; setChoice: (choice: Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>) => void; size: 'small' | 'medium' | 'large'; setSize: (size: 'small' | 'medium' | 'large') => void}; routed: HomeRowScreenSearch}) {
  const appearance = useScreenAppearance(captured.context)!, width = useShellWidth();
  const route = useMemo(() => addressedQuery(routed, bestMatch), [routed]), routeKey = useMemo(() => addressedQueryKey(route), [route]);
  const {choice, setChoice, size, setSize} = presentation;
  const href = useLocation({select: location => location.href});
  const openingCurrent = () => owner.life.current() && router.state.location.href === href;
  const state = useSyncExternalStore(owner.controller.subscribe, owner.controller.getSnapshot, owner.controller.getSnapshot);
  const page = useSyncExternalStore(owner.controller.collection.subscribe, owner.controller.collection.getSnapshot, owner.controller.collection.getSnapshot);
  // The controller's real applied state, in the same vocabulary as the address.
  const controllerQuery = useMemo<AddressedQuery | undefined>(() => { /* Preserve the original ownership/invalidation keys. */ void state.queryRevision;
    if (state.phase !== 'ready') return undefined;
    try {
      const sorts = owner.controller.query().sort ?? [];
      return {predicates: state.predicates, sorts: appliedSorts(sorts.length ? sorts : bestMatch)};
    } catch {return undefined;}
  }, [state.phase, state.predicates, state.queryRevision, owner]);
  const controllerKey = controllerQuery && addressedQueryKey(controllerQuery);
  // Frame #seeall draws the page's own art as a three-image wash behind the header; the
  // sources are the titles actually on this page, so nothing is drawn before they arrive.
  const ambient = useMemo(() => {
    if (appearance.showBackdrops === false || !page.total) return undefined;
    const seen = new Set<string>(), sources: AmbientArtSource[] = [];
    for (const member of owner.controller.collection.residentItems()) {
      const url = member.entry.posterUrl;
      if (!url || seen.has(url)) continue;
      seen.add(url);
      const source = context.artwork.ambient(url, {group: 'home-row', order: sources.length});
      if (!source) continue;
      sources.push(source);
      if (sources.length === 3) break;
    }
    return sources;
  }, [owner, context.artwork, appearance.showBackdrops, page]);
  // The toolbar writes the page's own address (a replace), so Back from a title and a reload
  // restore the same scoped filters and order; the grid follows the address, whoever moved it.
  const latest = useRef({context});
  useLayoutEffect(() => {latest.current = {context};});
  useLayoutEffect(() => owner.controller.routeQueries(query => {
    const {context} = latest.current;
    if (!owner.life.current() || !context.isCurrent()) return;
    void router.navigate({to: '/home/rows/$rowId', params: {rowId: row.id}, search: addressedSearch(query, bestMatch), replace: true, resetScroll: false})
      .catch(error => {if (owner.life.current() && context.isCurrent()) context.effects.reportError(error);});
  }), [owner, row.id]);
  useLayoutEffect(() => {
    if (!controllerKey || controllerKey === routeKey || !owner.life.current() || !context.isCurrent()) return;
    try {owner.controller.follow(route);} catch (error) {context.effects.reportError(error);}
  }, [controllerKey, routeKey, route, owner, context]);
  const library = context.identity?.libraries.find(library => library.id === row.libraryId);
  const browse: LibraryBrowseBindings = {menuContext: context, controller: owner.controller, library: {id: '*', name: row.title, kind: library?.kind ?? ''},
    pivot: owner.controller.source.pivot, artwork: context.artwork,
    appearance: {view: appearance.libraryView, cardWidth: (width < 600 ? 103 : 170) * (size === 'small' ? .8 : size === 'large' ? 1.2 : 1), backdrops: appearance.showBackdrops, cardSize: size},
    preferences: appearance, libraryViewOverride: choice?.defaultView === appearance.libraryView ? choice.view : undefined,
    shape: row.artworkShape === 'square' ? 'square' : 'poster',
    onAppearanceChange: change => {if (!openingCurrent()) return;if (change.view) setChoice({defaultView: appearance.libraryView, view: change.view});if (change.cardSize) setSize(change.cardSize);},
    owner: context.identity?.viewer.role === 'owner', reducedMotion: appearance.reducedMotion,
    env: {t: defaultI18n.t, locale: defaultI18n.region.formatLocale}, reportError: context.effects.reportError,
    canSaveView: false, empty: undefined,
    // Frame #seeall draws one quick toggle and names the row's own order "Best match";
    // the scope chips take the viewer's library names ("TV Shows"), not the kind's generic name.
    sortLabel: defaultI18n.t('next.browse.bestMatch'), quickIds: ['unwatched'],
    scopeLabel: kind => context.identity?.libraries.find(library => library.kind === kind)?.name ?? kind,
    familyForEntry: entry => context.identity?.libraries.find(library => library.id === entry.libraryId)?.kind,
    scopeLibraries: context.identity?.libraries,
    selection: {actions: member => {
      const queryRevision = state.queryRevision, revision = state.revision;
      const current = () => openingCurrent() && controllerKey === routeKey && owner.controller.getSnapshot().queryRevision === queryRevision
        && owner.controller.getSnapshot().revision === revision && owner.controller.contains(member.entry);
      return {onOpen: () => {if (current()) context.effects.openEntry(member.entry);},
        onPlay: member.entry.playback && member.entry.libraryId && member.entry.available !== false && !member.entry.missing ? () => {
          if (!current()) return;
          const entry = member.entry;
          void context.effects.play({libraryId: entry.libraryId!, itemId: entry.playback!.itemId, startSeconds: entry.playback!.startSeconds ?? 0, display: entryStartDisplay(entry)}, owner.life.signal(), current).catch(context.effects.reportError);
        } : undefined};
    }}};
  // Capture the rendered query and runtime now, never query a successor from a retained click.
  const playback = captured.binding.playback, queryRevision = state.queryRevision, revision = state.revision;
  const query = controllerQuery && owner.controller.query();
  const play = playback && query && controllerKey === routeKey && state.phase === 'ready' && revision ? (shuffle: boolean) => {
    const current = () => openingCurrent() && controllerKey === routeKey && owner.controller.getSnapshot().queryRevision === queryRevision
      && owner.controller.getSnapshot().revision === revision;
    if (!current()) return;
    const command = new AbortController(), off = owner.controller.subscribe(() => {if (!current()) command.abort();});
    void playback.runtime.playFromQueue({query: {libraryId: '*', pivot: owner.controller.source.pivot, query: query.query, sort: query.sort}},
      {signal: owner.life.signal(command.signal), isCurrent: current}, {shuffle})
      .catch(context.effects.reportError).finally(off);
  } : undefined;
  const backHome = () => {
    if (!openingCurrent() || !context.isCurrent()) return;
    // The drawn "‹ Home" leaves the way the viewer came. The router's history index
    // counts only entries this app pushed, so a tab that opened See all first has
    // nothing to return to and gets an explicit Home instead of leaving for unrelated
    // browser history.
    if (router.history.canGoBack()) router.history.back();
    else void router.navigate({to: '/'}).catch(error => {if (owner.life.current() && context.isCurrent()) context.effects.reportError(error);});
  };
  return <HomeRowScreen row={row} browse={browse} ambient={ambient} onBack={backHome}
    onPlay={play ? () => play(false) : undefined} onShuffle={play ? () => play(true) : undefined}/>;
}
function BoundHomeRow({captured, rowId}: {captured: ProtectedLibraryScreen; rowId: string}) {
  const pathname = useLocation({select: location => location.pathname}), width = useShellWidth(), elapsed = useLoadingClock();
  const context = useCommittedLibraryScreenContext(captured, pathname, () => router.state.location.pathname === pathname);
  return context ? <ContextHomeRow captured={captured} rowId={rowId} context={context}/>
    : <GridSkeleton elapsedMs={elapsed} window={{width, height: window.innerHeight, scrollTop: 0}} itemCount={1}/>;
}
function ContextHomeRow({captured, rowId, context}: {captured: ProtectedLibraryScreen; rowId: string; context: ScreenContext}) {
  const width = useShellWidth(), elapsed = useLoadingClock();
  const pathname = useLocation({select: location => location.pathname}), [attempt, setAttempt] = useState(0);
  type Opening = Readonly<{context: ScreenContext; rowId: string; attempt: number; life: ReturnType<typeof screenLifetime>}>;
  const [snapshot, publish] = useState<Readonly<{opening: Opening; row?: HomeRow; failure?: unknown}>>();
  useLayoutEffect(() => {
    const life = screenLifetime(context), opening: Opening = {context, rowId, attempt, life};
    const current = () => life.current() && router.state.location.pathname === pathname;
    publish({opening});
    void fetchHomeRow(life.api, rowId, {limit: 1}, life.signal()).then(row => {if (current()) publish({opening, row});})
      .catch(failure => {if (current()) publish({opening, failure});});
    return life.dispose;
  }, [context, rowId, attempt, pathname]);
  const offered = snapshot?.opening.context === context && snapshot.opening.rowId === rowId && snapshot.opening.attempt === attempt
    && snapshot.opening.life.current() ? snapshot : undefined;
  return offered?.failure ? <ScreenProblem context={context} error={offered.failure} retry={() => {if (offered.opening.life.current()) setAttempt(value => value + 1);}}/>
    : offered?.row ? <LoadedHomeRow captured={captured} context={context} row={offered.row}/>
      : <GridSkeleton elapsedMs={elapsed} window={{width, height: window.innerHeight, scrollTop: 0}} itemCount={1}/>;
}
function HomeRowRouteScreen() {
  const captured = useProtectedWebScreenContext(), {rowId} = useParams({from: '/home/rows/$rowId'});
  return captured ? <BoundHomeRow key={rowId} captured={captured} rowId={rowId}/> : null;
}
export default defineWebScreen({id: 'homeRow', component: HomeRowRouteScreen, validateSearch: parseHomeRowScreenSearch});
