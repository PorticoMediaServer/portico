import {readBrowseCapabilitiesResponse} from '@core/query/browse-reads.ts';
import {readLibraryMetadataAgentResponse} from '@core/query/catalogue-actions.ts';
import {createBrowserQueryActions, type BrowserQueryActionHost} from './selection-action-host';
import {entryStartDisplay} from '../../platform/playback/subject';
import {createSelectionActionHostBridge} from './selection-action-lifetime';
import type {BrowseSelectionContext} from '@core/selection-model/types.ts';
import {subscribeFacts} from '../../shell/fact-source';
import {useGuardedLibraryContext, useLibraryRouteGuard} from './library-route-guard';
import {useRouteLibrary} from './route-library';
import {useShellFacts} from '../../shell/fact-hooks';
import {ReadSeriesEntry} from '../read/ReadSeriesEntry';
import {ReadPeriodicalEntry} from '../read/ReadPeriodicalEntry';
import type {ReadingPeriodicalType} from '@portico/client-core';
import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {ToastHost, type ToastData} from '../../kit/overlays';
import {copyLink} from '../../shell/copy-link';
import {useLocation, useParams, useSearch} from '@tanstack/react-router';
import {defaultI18n} from '@i18n';
import type {Library} from '@core/index.ts';
import type {ContentEntry, ContentRoute, ContentSection, ContentView} from '@core/library-content.ts';
import {LibraryManagementService} from '@core/library-management.ts';
import {libraryTabs, readingTabModel, type LibraryTab} from '@core/presentation/library-tabs.ts';
import {parseBrowseCapabilities, type BrowseCapabilities} from '@core/browse.ts';
import {parseMetadataAgent} from '@core/server-admin/metadata-agent.ts';
import type {SavedDefinition} from '@core/personal-saved.ts';
import {randomId} from '@core/random-id.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {Skeleton, StateView} from '../../kit/states';
import {LibraryEmpty} from '../../kit/states/LibraryEmpty';

import {useShellWidth} from '../../shell/viewport';
import {ShowBody} from '../detail/Show';
import {DetailLoading} from '../detail/Loading';
import {useShowRoute} from '../detail/use-show-route';
import {AlbumScreen} from '../detail/Album';
import {ArtistScreen} from '../detail/Artist';
import {AudiobookEntry} from '../detail/audiobooks/Entry';
import type {ScreenContext} from '../detail/context';
import {screenLifetime} from '../detail/context';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback/types';
import {useBrowseLease, type BrowseLease} from './useBrowseLease';
import {createLibraryPlaySelection} from './play-selection';
import {createLibrarySelection} from './selection';
import {useMusicMixes} from './music-mixes-hook';
import {useMusicLibrarySummary} from './music-discover-navigation';
import {LibraryIdentity, LibraryScreen, type LibraryBindings} from './Library';
import {LibraryMenu} from '../../kit/browse';
import {Button} from '../../kit/controls';
import {NeedsAttention} from '../metadata/NeedsAttention';
import type {AttentionEntry} from '@core/metadata-attention.ts';
import {LibraryDiscoveryScreen} from './Discovery';
import {discoverySections} from './discovery-selection';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from './screen-context';
import {useScreenAppearance} from './screen-preferences';
import {browseConjunction, libraryTabAddress, parseLibraryScreenSearch, selectedLibraryTab, type LibraryScreenSearch} from './route-search';
import {addressCacheKey, addressedSearch, appliedSorts, libraryAddressKey} from './address-query';
import type {BrowseSource, BrowseState, BrowseController, BrowseMember} from './controller';
import {useBrowseController} from './browse-cache';
import {queryKeys} from '@core/query/keys.ts';
import {SaveView} from './SaveView';
import {FacetPage, ReadLibraryEntry, useReadLibrarySummary, authorPredicate, type FacetRequest} from '../read';
import {isReadingLibrary} from './reading-library';
import {useReadingMarkAll} from '../read/useReadingMarkAll';
import {libraryCountText} from '@core/presentation/library-counts.ts';
import {usePinEditor} from '../pins/PinEditor';

const t = defaultI18n.t;
const noSelectionSubscribe = () => () => {};
const noPlaybackSubscribe = () => () => {};
const noPlaybackSnapshot = () => undefined;
const noGridSubscribe = () => () => {};
const noGrid: BrowseState = Object.freeze({phase: 'loading', predicates: [], queryRevision: 0});
const noGridSnapshot = () => noGrid;
type PeriodicalTab = Readonly<{id: 'magazines' | 'newspapers'; label: 'library.tab.magazines' | 'library.tab.newspapers'; pivot: 'magazines' | 'newspapers'; shows: 'periodicals'; publicationType: ReadingPeriodicalType}>;
type ReadingTab = LibraryTab | PeriodicalTab;
function readingTabs(capabilities: BrowseCapabilities | undefined, kind: string): readonly ReadingTab[] {
  const base = readingTabModel(capabilities) ?? libraryTabs(kind);
  if (!capabilities || capabilities.library.kind !== 'book') return base;
  const out: ReadingTab[] = [base.find(tab => tab.id === 'discover') ?? base[0]!];
  for (const pivot of capabilities.pivots) {
    if (!pivot.browsable && pivot.facetSource?.field === 'publicationType' && (pivot.id === 'magazines' || pivot.id === 'newspapers')) {
      out.push({id: pivot.id, label: pivot.id === 'magazines' ? 'library.tab.magazines' : 'library.tab.newspapers', pivot: pivot.id, shows: 'periodicals', publicationType: pivot.id === 'magazines' ? 'magazine' : 'newspaper'});
      continue;
    }
    const tab = base.find(tab => tab.pivot === pivot.id || tab.id === pivot.id);
    if (tab && !out.some(candidate => candidate.id === tab.id)) out.push(tab);
  }
  return out.length > 1 ? out : base;
}

/** A committed Reading route owns its original context and its own API lifetime. Strict effect
 *  replay opens another lifetime; it never revives the one it just disposed, and the original
 *  captured actor, library and search are the witnesses every read is admitted against. */
function useReadingLifetime(context: ScreenContext, routeKey: string, routeCurrent: () => boolean) {
  const [published, publish] = useState<Readonly<{id: string; context: ScreenContext; routeKey: string; life: ReturnType<typeof screenLifetime>}>>();
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({routeCurrent});
  useLayoutEffect(() => {setupPorts.current = {routeCurrent};});
  useLayoutEffect(() => {
    const {routeCurrent} = setupPorts.current;
    const original = screenLifetime(context);
    let live = true;
    const current = () => live && original.current() && routeCurrent();
    const retired = () => new DOMException('Reading route retired', 'AbortError');
    const life: ReturnType<typeof screenLifetime> = {...original, current,
      api: {
        async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
          if (!current()) throw retired(); const value = await original.api.request<T>(path, method, body, signal); if (!current()) throw retired(); return value;
        },
        requestRaw: context.api.requestRaw.bind(context.api),
      },
      dispose: () => {live = false; original.dispose();}};
    publish({id: crypto.randomUUID(), context, routeKey, life});
    return () => {life.dispose();};
    // routeCurrent belongs to this exact route key and is never adopted later.
  }, [context, routeKey]);
  return published?.context === context && published.routeKey === routeKey && published.life.current() ? published : undefined;
}
function ReadingLibrary({captured, library, search, onMarked}: {captured: ProtectedLibraryScreen; library: Library; search: LibraryScreenSearch; onMarked: () => void}) {
  const href = useLocation({select: libraryAddressKey});
  const context = useCommittedLibraryScreenContext(captured, href, () => libraryAddressKey(router.state.location) === href);
  return context ? <CommittedReadingLibrary captured={captured} library={library} search={search} onMarked={onMarked} context={context} href={href}/> : <Skeleton height={320}/>;
}
function CommittedReadingLibrary({captured, library, search, onMarked, context, href}: {captured: ProtectedLibraryScreen; library: Library; search: LibraryScreenSearch; onMarked: () => void; context: ScreenContext; href: string}) {
  const published = useReadingLifetime(context, href, () => libraryAddressKey(router.state.location) === href);
  return published ? <LiveReadingLibrary key={published.id} captured={captured} library={library} search={search} onMarked={onMarked} context={context} life={published.life}/>
    : <Skeleton height={320}/>;
}
/** The live reading body: every hook below runs only with a committed, non-optional lifetime,
 *  so no hook order changes when one is published or replaced. */
function LiveReadingLibrary({captured, library, search, onMarked, context, life}: {captured: ProtectedLibraryScreen; library: Library; search: LibraryScreenSearch; onMarked: () => void; context: ScreenContext; life: ReturnType<typeof screenLifetime>}) {
  const appearance = useScreenAppearance(captured.context)!, width = useShellWidth();
  const markAll = useReadingMarkAll(context, library.id, useLocation({select: libraryAddressKey}), onMarked);
  // The same ⋯ as the grid pivot (survey A 3): Copy link, Mark all as read, and the owner's Library settings and Scan.
  // The grid pivot mounts BoundLibrary, whose CommittedLibrary owns the scan offer there; this one stays off so the
  // owner's library is read once (one get_admin_jobs read), not twice.
  const scan = useLibraryScan(context, library, life, !(search.view === 'browse' || search.query));
  const [linkToast, setLinkToast, copyLibraryLink] = useCopyLibraryLink(library.id);
  // phone-app 4c: Edit pins in the library's ⋯.
  const pins = usePinEditor(context);
  type Attempt = {context: ProtectedLibraryScreen['context']; screen: ScreenContext; life: typeof life; libraryId: string; kind: string; controller: AbortController; active: boolean; phase: 'loading' | 'error' | 'ready'};
  const [retry, setRetry] = useState(0), attempt = useRef<Attempt | null>(null);
  const [capabilitySnapshot, setCapabilities] = useState<Readonly<{attempt: Attempt; phase: 'loading' | 'error' | 'ready'; data?: BrowseCapabilities; error?: unknown}>>();
  const snapshot = capabilitySnapshot?.attempt.context === captured.context && capabilitySnapshot.attempt.screen === context
    && capabilitySnapshot.attempt.life === life && capabilitySnapshot.attempt.libraryId === library.id && capabilitySnapshot.attempt.kind === library.kind
    ? capabilitySnapshot : undefined;
  const capabilities = snapshot?.phase === 'ready' ? snapshot.data : undefined;
  /** desktop-read 1b/2/2c draw the library's own wash and its classified count line, and
   *  phone-read 1b lists every library in the switcher with its own count. */
  const {wash, counts, libraryCounts} = useReadLibrarySummary({context: captured.context, libraryId: library.id, artwork: context.artwork,
    api: life.api, signal: life.signal, isCurrent: life.current, reportError: context.effects.reportError, enabled: true});
  const libraryChoices = (context.identity?.libraries ?? []).map(choice => ({...choice, count: libraryCounts.find(row => row.id === choice.id)?.text}));
  useLayoutEffect(() => {
    const original: Attempt = {context: captured.context, screen: context, life, libraryId: library.id, kind: library.kind, controller: new AbortController(), active: true, phase: 'loading'};
    attempt.current = original;
    const current = () => original.active && attempt.current === original && !original.controller.signal.aborted && life.current() && original.active && attempt.current === original;
    const retire = () => {original.active = false; if (attempt.current === original) attempt.current = null; original.controller.abort();};
    setCapabilities({attempt: original, phase: 'loading'});
    void (async () => {
      const raw = await readBrowseCapabilitiesResponse(life.api, original.libraryId, original.kind === 'comic' ? 'comics' : 'books', original.controller.signal);
      if (!current()) return;
      const data = parseBrowseCapabilities(raw);
      if (data.library.id !== original.libraryId || data.library.kind !== original.kind) throw new Error('Reading capability library mismatch');
      if (!current()) return;
      original.phase = 'ready'; setCapabilities({attempt: original, phase: 'ready', data});
    })().catch(error => {
      if (!current()) return;
      original.phase = 'error'; setCapabilities({attempt: original, phase: 'error', error});
      context.effects.reportError(error);
    });
    return retire;
  }, [captured.context, context, life, library.id, library.kind, retry]);
  const retryCapabilities = () => {
    const original = snapshot?.attempt;
    if (!original || snapshot.phase !== 'error' || original.phase !== 'error' || !original.active || attempt.current !== original || !life.current()
      || !original.active || attempt.current !== original) return;
    original.active = false; attempt.current = null; original.controller.abort();
    if (attempt.current !== null || !life.current() || attempt.current !== null) return;
    setCapabilities(undefined); setRetry(value => value + 1);
  };
  /** Root §3: the server's published pivots decide the reading tabs; older servers keep the authored pair. */
  const tabs = useMemo(() => readingTabs(capabilities, library.kind), [capabilities, library.kind]);
  const periodicalPivot = search.pivot === 'magazines' || search.pivot === 'newspapers' ? search.pivot : undefined;
  const selected = tabs.find(tab => tab.id === (periodicalPivot ?? search.view ?? 'discover')) ?? tabs[0]!;
  const gridPivot = tabs.find(tab => tab.id === 'browse')?.pivot;
  const navigate = (value: LibraryScreenSearch) => {if (context.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId: library.id}, search: value}).catch(context.effects.reportError);};
  const header = <LibraryIdentity library={library} libraries={libraryChoices} pivot={periodicalPivot ?? selected.id}
    tabs={tabs.map(tab => ({value: tab.id, label: t(tab.label)}))}
    appearance={{view: appearance.libraryView, cardWidth: width < 600 ? 108 : 190, cardSize: 'medium', backdrops: appearance.showBackdrops}}
    preferences={appearance} artwork={context.artwork} libraryMenu={<>{pins.element}{withLinkToast(libraryMenu({life}, context, library, false, () => {}, scan, markAll, copyLibraryLink, pins.open), linkToast, () => setLinkToast(null))}</>} wash={wash} counts={counts}
    onPivot={id => {const tab = tabs.find(tab => tab.id === id); if (!tab) return;
      if (tab.shows === 'periodicals') {navigate({view: 'series', pivot: tab.id}); return;}
      if (tab.shows === 'facet') {navigate({view: tab.id === 'authors' ? 'authors' : 'series'}); return;}
      navigate({view: tab.id === 'discover' ? 'discover' : 'browse', ...(tab.pivot ? {pivot: tab.pivot} : {})});}}
    onLibrary={libraryId => {if (context.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId}, search: {}}).catch(context.effects.reportError);}}
    onLibraries={() => {if (context.isCurrent()) void router.navigate({to: '/libraries'}).catch(context.effects.reportError);}}/>;
  const request = useMemo<FacetRequest>(() => Object.assign<FacetRequest, Pick<typeof context.api, 'requestRaw'> & {isCurrent: () => boolean}>(
    (path, method, body, signal) => life.api.request(path, method, body, signal), {requestRaw: context.api.requestRaw.bind(context.api), isCurrent: life.current}), [life, context]);
  if (periodicalPivot) {
    if (!capabilities) return <>{header}{snapshot?.phase === 'error' ? <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: retryCapabilities}}/> : <Skeleton height={320}/>}</>;
    if (selected.shows !== 'periodicals' || selected.id !== periodicalPivot || search.query || search.sort?.length || search.q) return <>{header}<StateView title={t('title.nothingHere')}/></>;
    return <>{header}<ReadPeriodicalEntry captured={captured} libraryId={library.id} kind={selected.publicationType} seriesId={search.entityId}/></>;
  }
  if (search.view === 'series' && search.entityId) return <>{header}<ReadSeriesEntry captured={captured} libraryId={library.id} seriesId={search.entityId}/></>;
  if (selected.shows === 'facet') return <>{header}<FacetPage libraryId={library.id} field={selected.facetSource?.field === 'series' ? 'series' : 'author'}
    pivot={gridPivot ?? (library.kind === 'comic' ? 'comics' : 'books')} request={request}
    onSelect={value => {if (selected.facetSource?.field === 'author') navigate({view: 'browse', ...(gridPivot ? {pivot: gridPivot} : {}), query: authorPredicate(value.value)}); else if (value.seriesId) navigate({view: 'series', entityId: value.seriesId});}}/></>;
  if (search.view === 'browse' || search.query) return <BoundLibrary captured={captured} library={library} search={search} tabs={tabs} readingMarkAll={markAll}/>;
  return <ReadLibraryEntry captured={captured} libraryId={library.id} header={header} view="discover"/>;
}
function LibraryShow({captured, libraryId, search}: {captured: ProtectedLibraryScreen; libraryId: string; search: LibraryScreenSearch}) {
  const href = useLocation({select: location => location.href});
  const context = useCommittedLibraryScreenContext(captured, href, () => router.state.location.href === href);
  const owned = useShowRoute(context, captured, {libraryId, ...(search.view === 'season' ? {seasonId: search.entityId} : {showId: search.entityId})});
  return owned ? <ShowBody binding={owned.binding} selection={owned.selection}/> : <DetailLoading/>;
}
type ScanLease = Readonly<{context: ScreenContext; libraryId: string; life: {api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}; current: () => boolean}; signal: AbortSignal; service: LibraryManagementService; pending: {current: boolean}}>;
/** Every library's ⋯ (desktop-browse #grid, desktop-listen #music, phone-app 4c) draws only the rows whose ports this
 *  screen really holds: saving the current view, copying its link, and for the owner the library settings page, Scan for
 *  new files (from its own lease) and Unmatched items (an online library). Mark all read is the reading libraries'. */
/** A library ⋯ menu with its own Copy link confirmation beside it. */
function withLinkToast(menu: ReactNode, toast: ToastData | null, dismiss: () => void): ReactNode {
  return menu === undefined ? undefined : <>{menu}<ToastHost toast={toast} onDismiss={dismiss}/></>;
}
function libraryMenu(resources: {life: {current: () => boolean}; controller?: {canSaveView: () => boolean}}, context: ScreenContext, library: Library, onlineAttention: boolean, save: () => void, scan?: () => void, markAll?: () => void, copy?: () => void, editPins?: () => void) {
  const owner = managesServer(context);
  const unmatched = onlineAttention && context.effects.editMetadata ? () => {if (resources.life.current() && context.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId: library.id}, search: {unmatched: true}}).catch(context.effects.reportError);} : undefined;
  const settings = owner ? () => {if (resources.life.current() && context.isCurrent()) void router.navigate({to: '/settings', search: {scope: 'server', section: 'libraries'}}).catch(context.effects.reportError);} : undefined;
  const canSave = resources.life.current() && context.isCurrent() && !!resources.controller?.canSaveView();
  if (!canSave && !unmatched && !settings && !scan && !markAll && !copy && !editPins) return undefined;
  return <LibraryMenu owner={!!owner} read={isReadingLibrary(library.kind)} {...(copy ? {onCopyLink: copy} : {})} {...(editPins ? {onEditPins: editPins} : {})} {...(markAll ? {onMarkAll: markAll} : {})} {...(canSave ? {onSaveView: save} : {})} {...(unmatched ? {onUnmatched: unmatched} : {})}
    {...(settings ? {onLibrarySettings: settings} : {})} {...(scan ? {onScan: scan} : {})}/>;
}
/** The local owner who may manage this server. A reading screen's identity may carry no viewer yet (it reads its
 * pivots first), so every step is optional. */
function managesServer(context: ScreenContext): boolean {
  const identity = context.identity, viewer = identity?.viewer;
  return viewer?.role === 'owner' && viewer.authority === 'local' && identity?.capabilities?.manageServer === true;
}
/** Scan for new files, offered to the owner when the server lists `scan` for this library. Every library kind's ⋯ uses it. */
function useLibraryScan(context: ScreenContext, library: Library, life: ScanLease['life'], enabled = true): (() => void) | undefined {
  const manageServer = enabled && managesServer(context);
  const [scanOffer, setScanOffer] = useState<Readonly<{lease: ScanLease; allowed: boolean}>>();
  useLayoutEffect(() => {
    if (!manageServer) {setScanOffer(undefined); return;}
    const controller = new AbortController();
    const lease: ScanLease = {context, libraryId: library.id, life, signal: controller.signal, pending: {current: false},
      service: new LibraryManagementService({queries: context.queries, scope: {serverId: context.scope.serverId, viewerId: context.scope.viewerId},
        api: {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) =>
          life.api.request<T>(path, method, body, controller.signal.aborted ? controller.signal : AbortSignal.any([controller.signal, ...(signal ? [signal] : [])]))}})};
    let live = true;
    setScanOffer(undefined);
    void lease.service.selectLibrary(library.id).then(() => {
      if (!live || controller.signal.aborted || !life.current() || !context.isCurrent()) return;
      const selected = lease.service.getSnapshot().selected;
      setScanOffer({lease, allowed: selected.phase === 'ready' && selected.data?.library.id === library.id && selected.data.library.actions.includes('scan')});
    }).catch(error => {if (live && !controller.signal.aborted && life.current() && context.isCurrent()) context.effects.reportError(error);});
    return () => {live = false; controller.abort(); lease.service.dispose();};
  }, [manageServer, library.kind, library.id, life, context]);
  /** The click closes over the exact rendered lease and the exact selected row it offered. */
  const scan = scanOffer?.allowed ? () => {
    const {lease} = scanOffer;
    if (lease.context !== context || lease.libraryId !== library.id || lease.signal.aborted || !lease.life.current()
      || !context.isCurrent() || lease.pending.current) return;
    const selected = lease.service.getSnapshot().selected;
    if (selected.phase !== 'ready' || selected.data?.library.id !== library.id || !selected.data.library.actions.includes('scan')) return;
    lease.pending.current = true;
    void lease.service.scan().catch(error => {if (!lease.signal.aborted && lease.life.current() && context.isCurrent()) context.effects.reportError(error);}).finally(() => {lease.pending.current = false;});
  } : undefined;
  return scan;
}
/** O-161: the library's ⋯ copies its shareable address (this server's, on Hosted), with a toast saying how it went. */
function useCopyLibraryLink(libraryId: string) {
  const [linkToast, setLinkToast] = useState<ToastData | null>(null);
  const copyLibraryLink = () => {void copyLink('library', {libraryId}).then(
    () => setLinkToast({id: crypto.randomUUID(), icon: 'i-check', message: t('next.overlays.linkCopied')}),
    () => setLinkToast({id: crypto.randomUUID(), icon: 'i-warn', tone: 'warning', message: t('next.overlays.copyLinkFailed')}));};
  return [linkToast, setLinkToast, copyLibraryLink] as const;
}
type BoundLibraryProps = {captured: ProtectedLibraryScreen; library: Library; search: LibraryScreenSearch; tabs?: readonly ReadingTab[]; musicLookup?: ReturnType<typeof useMusicLibrarySummary>; readingMarkAll?: () => void};
function useBoundLibraryFrame({captured, library, search, tabs: offered, musicLookup}: BoundLibraryProps, context: ScreenContext, presentation: BoundPresentation) {
  const appearance = useScreenAppearance(captured.context)!, width = useShellWidth();
  // The offer needs this exact library's own online metadata agent. The fact is keyed to the
  // library and screen scope that produced it, so a stale read can never enable another library
  // or survive a scope change; unknown, reset and failed reads all withhold the tab.
  const [agentFact, setAgentFact] = useState<Readonly<{key: string; context: typeof context; online: boolean}>>();
  const agentKey = JSON.stringify([context.scope.serverId, context.scope.viewerId, library.id, library.kind]);
  // The captured ScreenContext itself is part of the fact: a new capture for the same viewer
  // must not reuse a previous true before its own read, including on early lifetime refusal.
  const onlineAttention = agentFact?.key === agentKey && agentFact.context === context && context.isCurrent() && agentFact.online === true;
  const tabs = offered ?? libraryTabs(library.kind, {owner: context.identity?.capabilities?.manageServer === true, onlineAttention});
  const selectedTab = selectedLibraryTab(tabs, search, library.defaultView ?? 'discover');
  // ReadingLibrary owns periodical screens; only real browse tabs supply sorts.
  const requested = selectedTab?.shows === 'periodicals' ? undefined : selectedTab;
  const view: ContentView = search.view ?? (search.pivot ? 'browse' : library.defaultView ?? 'discover');
  const musicMixes = useMusicMixes(captured, library.kind === 'music' && view === 'discover');
  const musicRead = useMusicLibrarySummary(captured, library.id, library.kind === 'music' && !musicLookup);
  const audiobookRead = useMusicLibrarySummary(captured, library.id, library.kind === 'audiobook', 'audiobook');
  const musicSummary = musicLookup?.library ?? musicRead.library, musicLibraries = musicLookup?.libraries ?? musicRead.libraries;
  const pivot = search.pivot ?? requested?.pivot;
  const browse = !!pivot && !search.entityId && !search.category && !search.q && view !== 'discover';
  // The grid is the address's, filters and order included (LB-1): one grid per query in the
  // viewer's cache, so Back to it, or to an order chosen before, draws it with no request. The tab's
  // own opening order is part of it, because two tabs can read one pivot (Shows, Recently aired).
  const defaultSorts = requested?.defaultSort ? [requested.defaultSort] : undefined;
  const gridKey = browse && pivot ? queryKeys.libraryBrowse(library.id, pivot, addressCacheKey(search, defaultSorts)) : undefined;
  const gridSource: BrowseSource = {libraryId: library.id, pivot: pivot ?? '', ...(defaultSorts ? {defaultSorts} : {}),
    ...(search.query ? {predicates: browseConjunction(search.query)} : {}), ...(search.sort ?? defaultSorts ? {sorts: search.sort ?? defaultSorts} : {})};
  const audiobookSummary = audiobookRead.library;
  return {context, appearance, width, ...presentation, onlineAttention, agentKey, setAgentFact, tabs, requested, view, musicMixes, musicSummary, musicLibraries, audiobookSummary, browse, gridKey, gridSource};
}
type BoundPresentation = Readonly<{choice?: Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>; setChoice: (choice: Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>) => void; size: 'small' | 'medium' | 'large'; setSize: (size: 'small' | 'medium' | 'large') => void}>;
function BoundLibrary(props: BoundLibraryProps) {
  const [choice, setChoice] = useState<Readonly<{defaultView: 'grid' | 'list'; view: 'grid' | 'list'}>>();
  const [size, setSize] = useState<'small' | 'medium' | 'large'>('medium');

  const href = useLocation({select: libraryAddressKey});
  const context = useCommittedLibraryScreenContext(props.captured, href, () => libraryAddressKey(router.state.location) === href);
  return context ? <ContextLibrary {...props} context={context} presentation={{choice, setChoice, size, setSize}}/> : <Skeleton height={320}/>;
}
function ContextLibrary(props: BoundLibraryProps & {context: ScreenContext; presentation: BoundPresentation}) {
  const frame = useBoundLibraryFrame(props, props.context, props.presentation), {context, agentKey, setAgentFact} = frame, {library} = props;
  const href = useLocation({select: libraryAddressKey}), runtime = props.captured.binding.playback?.runtime;
  const resources = useBrowseLease(context, href, () => libraryAddressKey(router.state.location) === href, runtime);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({setAgentFact});
  useEffect(() => {setupPorts1.current = {setAgentFact};});
  useEffect(() => {
    const {setAgentFact} = setupPorts1.current;
    if (!resources?.life.current()) return;
    let live = true;
    const withhold = () => {if (live && resources.life.current()) setAgentFact({key: agentKey, context, online: false});};
    void readLibraryMetadataAgentResponse(resources.life.api, library.id).then(raw => {
      if (!live || !resources.life.current()) return;
      let agent;
      try {agent = parseMetadataAgent(raw);} catch {withhold(); return;}
      // A read that answers for another library or kind is not this library's fact.
      if (agent.libraryId !== library.id || agent.libraryKind !== library.kind) {withhold(); return;}
      setAgentFact({key: agentKey, context, online: agent.agent === 'online'});
    }, withhold).catch(withhold);
    return () => {live = false;};
  }, [resources, library.id, library.kind, agentKey, context]);
  return resources ? <CommittedLibrary key={resources.id} {...props} frame={frame} resources={resources} href={href}/> : <Skeleton height={320}/>;
}
function CommittedLibrary({captured, library, search, readingMarkAll, frame, resources, href}: BoundLibraryProps & {frame: ReturnType<typeof useBoundLibraryFrame>; resources: BrowseLease; href: string}) {
  const {context, appearance, width, choice, setChoice, size, setSize, onlineAttention, agentKey, tabs, requested, view, musicMixes, musicSummary, musicLibraries, audiobookSummary, browse} = frame;
  // The grid for this address's filters and order, from the viewer's cache; the one shown before
  // stays until a new query's first page arrives.
  // A test's or a guarded screen's API may have no raw reader; the grid then reads without one.
  const raw = useMemo(() => (context.api.requestRaw as typeof context.api.requestRaw | undefined)?.bind(context.api), [context]);
  const {controller} = useBrowseController(context.queries, frame.gridKey, frame.gridSource, raw);
  const state = useSyncExternalStore(controller?.subscribe ?? noGridSubscribe, controller?.getSnapshot ?? noGridSnapshot, controller?.getSnapshot ?? noGridSnapshot);
  // LB-1: the address owns the grid's filters and order. The toolbar writes them there (a replace
  // that keeps the scroll), and the address's query picks the grid, whether the toolbar,
  // Back/Forward or a link put it there. An absent order is the tab's own, else the pivot's.
  const routing = useRef({search, requested});
  useLayoutEffect(() => {routing.current = {search, requested};});
  useLayoutEffect(() => browse && controller ? controller.routeQueries(query => {
    const {search, requested} = routing.current, caps = controller.getSnapshot().capabilities;
    if (!caps || !resources.life.current() || !context.isCurrent()) return;
    const pivot = caps.resolvedPivot ?? caps.pivots.find(value => value.id === controller.source.pivot);
    const defaults = appliedSorts(requested?.defaultSort ? [requested.defaultSort] : pivot?.defaultSort ?? []);
    const {query: _query, sort: _sort, ...rest} = search;
    void router.navigate({to: '/library/$libraryId', params: {libraryId: library.id}, search: {...rest, ...addressedSearch(query, defaults)}, replace: true, resetScroll: false})
      .catch(error => {if (resources.life.current() && context.isCurrent()) context.effects.reportError(error);});
  }) : undefined, [browse, controller, resources, context, library.id]);
  // O-161: the library's ⋯ copies its shareable address (this server's, on Hosted).
  const [linkToast, setLinkToast, copyLibraryLink] = useCopyLibraryLink(library.id);
  // phone-app 4c: Edit pins in the library's ⋯.
  const pins = usePinEditor(context);
  const runtime = captured.binding.playback?.runtime;
  // Frame 7's Recently played mark belongs to the queue session this snapshot actually
  // describes: the current entry is the queue's own currentEntryId, and the published
  // presentation must be that same session and generation, still playing and not
  // finished, with the runtime healthy and this screen still current. A local, channel
  // or other session's playback is never substituted for it.
  const playbackSnapshot = useSyncExternalStore<WebPlaybackRuntimeSnapshot | undefined>(runtime?.subscribe ?? noPlaybackSubscribe,
    runtime?.getSnapshot ?? noPlaybackSnapshot, runtime?.getSnapshot ?? noPlaybackSnapshot);
  const nowPlayingAlbumId = useMemo(() => {
    const snapshot = playbackSnapshot;
    if (!snapshot || snapshot.disposed || snapshot.problem || !context.isCurrent()) return undefined;
    // A retained dock snapshot must not mark a remote (Cast/Play on) session as local playback.
    if (snapshot.dock.remote?.phase === 'playing') return undefined;
    const view = snapshot.queueView, current = view?.current, presentation = snapshot.presentation;
    if (!view || !current || !presentation || presentation.fence.kind !== 'queue') return undefined;
    if (presentation.fence.sessionId !== current.id || presentation.fence.generation !== current.generation) return undefined;
    if (!presentation.playing || presentation.finished || snapshot.playback.phase !== 'ready') return undefined;
    const entryId = view.queue.currentEntryId;
    if (!entryId || presentation.fence.entryId !== entryId) return undefined;
    return view.items.find(item => item.entryId === entryId)?.albumId;
  }, [playbackSnapshot, context]);
  type SelectionLease = Readonly<{resources: BrowseLease; controller: BrowseController; runtime: typeof runtime; queryRevision: number; current: () => boolean; playSelection: ReturnType<typeof createLibraryPlaySelection>; selection: ReturnType<typeof createLibrarySelection>}>;
  const [selectionLease, publishSelection] = useState<SelectionLease>();
  // The grid reports its visible range only when it changes; a selection built afterwards (another
  // grid, a new page revision) is given the range already on screen.
  const visibleRange = useRef<Readonly<{first: number; last: number}>>(undefined);
  const [queryBridge] = useState(() => createSelectionActionHostBridge<BrowseSelectionContext>());
  const gridName = frame.gridKey ? JSON.stringify(frame.gridKey) : null;
  const [queryOwner, publishQueryOwner] = useState<Readonly<{resources: BrowseLease; controller?: BrowseController; actions: BrowserQueryActionHost<BrowseSelectionContext>}>>();
  useLayoutEffect(() => {
    const life = new AbortController(), signal = resources.life.signal(life.signal);
    const session = captured.context.session(), actor = session.viewer;
    const scopeKey = () => {
      const current = captured.context.session(), viewer = current.viewer, query = controller?.getSnapshot() ?? noGrid;
      return JSON.stringify([resources.routeKey, gridName, current.sessionFamilyId, current.deviceId,
        viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, viewer.role, captured.context.viewerFence,
        query.predicates, query.sort]);
    };
    const actions = createBrowserQueryActions(queryBridge, {api: {...resources.life.api, mediaUrl: context.api.mediaUrl.bind(context.api)},
      requestId: async () => randomId(), deviceId: session.deviceId ?? '', owner: actor.role === 'owner' && actor.authority === 'local',
      signal, scopeKey, isCurrent: () => resources.life.current() && context.isCurrent() && libraryAddressKey(router.state.location) === href,
      subscribe: listener => {const a = controller?.subscribe(listener), b = subscribeFacts(captured.context, listener), c = router.subscribe('onResolved', listener); return () => {a?.();b();c();};}});
    publishQueryOwner({resources, controller, actions});
    return () => {life.abort();actions.dispose();queryBridge.dispose();};
  }, [resources, controller, gridName, context, captured.context, queryBridge, href]);
  const queryActions = queryOwner?.resources === resources && queryOwner.controller === controller ? queryOwner.actions : undefined;
  // Selection and Play selection belong to the grid shown: another query's grid gets its own.
  useLayoutEffect(() => {
    if (!controller) {publishSelection(undefined); return;}
    const command = new AbortController(), signal = resources.life.signal(command.signal), queryRevision = state.queryRevision;
    const current = () => !signal.aborted && resources.life.current() && !controller.isDisposed() && controller.getSnapshot().queryRevision === queryRevision;
    const api = {async request<T>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> {
      if (!current()) throw new DOMException('Browse selection retired', 'AbortError');
      const value = await resources.life.api.request<T>(path, method, body, input ? AbortSignal.any([signal, input]) : signal);
      if (!current()) throw new DOMException('Browse selection retired', 'AbortError');return value;
    }};
    const viewer = captured.context.session().viewer;
    if (viewer.authority !== 'local' && viewer.authority !== 'hosted') throw new DOMException('Screen authority unavailable', 'AbortError');
    const playSelection = createLibraryPlaySelection({source: controller, queries: context.queries, api, scope: context.scope,
      principal: {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId},
      requestId: async () => randomId(), signal,
      isCurrent: current,
      // The bar's job executor is the same captured route lifetime; these are real
      // executors, and each title's own Detail actions still decide its grants.
      deletion: context.identity?.viewer.role === 'owner' && context.identity.viewer.authority === 'local',
      editor: context.identity?.viewer.role === 'owner' && context.identity.viewer.authority === 'local',
      destinations: true,
      personal: {myList: true, favorite: true, watched: true},
      queue: runtime?.catalogueQueue(),
      playback: runtime?.cataloguePlayback()});
    const selection = createLibrarySelection({
      playSelection, queryActions,
      pickers: {api, queries: context.queries, serverId: context.scope.serverId, viewerId: context.scope.viewerId, requestId: async () => randomId(),
        signal, isCurrent: current,
        canCreateCollection: context.identity?.viewer.role === 'owner',
        pickerIdentity: {authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId, events: context.events}},
      ...(runtime ? {enqueue: playSelection.enqueue, enqueueQuery: playSelection.enqueueQuery} : {}),
      owner: context.identity?.viewer.role === 'owner' && context.identity.viewer.authority === 'local',
      descriptor: () => controller.wholeSelection(),
      wholeSelectionUnavailable: {subscribe: controller.subscribe, getSnapshot: () => controller.wholeSelectionUnavailable()},
      ...(context.identity?.viewer.role === 'owner' && context.identity.viewer.authority === 'local' ? {deletion: {api, queries: context.queries, serverId: context.scope.serverId, viewerId: context.scope.viewerId,
        requestId: async () => randomId(), signal, isCurrent: current}, editor: {
        api, events: context.events, requestId: async () => randomId(), serverId: context.scope.serverId, viewerId: context.scope.viewerId,
        artwork: context.artwork, signal, isCurrent: current,
      }} : {}),
      actions: {api, events: context.events, requestId: async () => randomId(),
        signal, isCurrent: current, reportError: context.effects.reportError, ...(context.effects.notify ? {notify: context.effects.notify} : {})},
      reportError: context.effects.reportError,
    });
    queryBridge.attach(playSelection);
    publishSelection({resources, controller, runtime, queryRevision, current, playSelection, selection});
    playSelection.start();
    if (visibleRange.current) selection.registerWindow(visibleRange.current.first, visibleRange.current.last);
    return () => {command.abort();queryBridge.attach();selection.dispose?.();playSelection.dispose();};
    // A page revision belongs to its selection model: the hub reading the grid again in place starts a new one.
  }, [captured.context, context, resources, controller, runtime, state.queryRevision, state.revision, queryActions, queryBridge]);
  const selectedLease = selectionLease?.resources === resources && selectionLease.controller === controller && selectionLease.runtime === runtime
    && selectionLease.queryRevision === state.queryRevision && selectionLease.current() ? selectionLease : undefined;
  const playSelection = selectedLease?.playSelection, selection = selectedLease?.selection;
  const registerVisible = useCallback((first: number, last: number) => {visibleRange.current = {first, last};selection?.registerWindow(first, last);}, [selection]);
  /** The selection toolbar is a real surface only while the viewer has an active selection;
   * an empty one must not displace the library's own toolbar. The fact comes from the
   * selection model's own snapshot, never a local counter. */
  const selectionActive = useSyncExternalStore(playSelection?.subscribe ?? noSelectionSubscribe,
    () => playSelection?.getSnapshot().selection?.active === true, () => false);
  // While a selection is open the grid's pages stay as they are; the hub's reading waits for it to end.
  useLayoutEffect(() => selectionActive && controller ? controller.hold() : undefined, [selectionActive, controller]);
  const [saving, setSaving] = useState<SavedDefinition>();
  const navigate = (value: LibraryScreenSearch, id = library.id) => {if (resources.life.current() && context.isCurrent()) void router.navigate({to: '/library/$libraryId', params: {libraryId: id}, search: value}).catch(context.effects.reportError);};
  const actions = (entry: ContentEntry, witness = () => !browse || !!controller?.contains(entry)) => {
    const current = () => resources.life.current() && witness();
    /** P2-74 E-web-02: a press that crossed a re-read of the list (a stale selection query
     * re-browses it) waits once for the fresh list and acts on the same title there; if the title
     * is gone it says so. It never does nothing. */
    const act = (run: (target: ContentEntry, isCurrent: () => boolean) => void | Promise<unknown>) => {
      if (!resources.life.current()) return;
      if (current()) {void Promise.resolve(run(entry, current)).catch(context.effects.reportError); return;}
      if (!browse || !controller) return;
      void (async () => {
        await controller.settled(resources.life.signal(), 5000);
        const fresh = controller.resident(entry);
        if (!resources.life.current()) return;
        if (!fresh) throw Object.assign(new Error('library_entry_changed'), {code: 'operation_unavailable'});
        await run(fresh, () => resources.life.current() && controller.contains(fresh));
      })().catch(context.effects.reportError);
    };
    return {onOpen: () => act(target => context.effects.openEntry(target)),
      onPlay: entry.playback && entry.libraryId && entry.available !== false && !entry.missing ? () => act((target, isCurrent) => target.playback
        ? context.effects.play({libraryId: target.libraryId!, itemId: target.playback.itemId, startSeconds: target.playback.startSeconds ?? 0, display: entryStartDisplay(target)}, resources.life.signal(), isCurrent)
        : Promise.reject(Object.assign(new Error('library_entry_changed'), {code: 'operation_unavailable'}))) : undefined};
  };
  const discoveryActions = (section: ContentSection, entry: ContentEntry) => {
    const projection = resources.service.getSnapshot().projection, seed = entry.radioSeed, playRadio = context.effects.playRadio;
    const current = () => resources.life.current() && context.isCurrent() && libraryAddressKey(router.state.location) === href && resources.playbackOwner === runtime
      && resources.service.getSnapshot().phase === 'ready' && resources.service.getSnapshot().projection === projection
      && discoverySections(resources.service.getSnapshot()).some(row => row === section && row.entries.includes(entry));
    if (entry.kind !== 'radio') return actions(entry, current);
    const play = () => {
      if (!seed || !runtime || !playRadio || !current()) return;
      void playRadio(seed.id, resources.life.signal(), current).catch(context.effects.reportError);
    };
    // A recent radio is its exact admitted seed, never an item or a library route.
    return {onOpen: play, ...(seed && runtime && playRadio ? {onPlay: play} : {})};
  };
  // Frame #grid: seven fluid columns at 1440 on Medium. 170 is the Medium card size the
  // drawn column paints; the screen fills each column to the gutter.
  // phone-app 5: a phone's poster grid is three across (about 100px cards at 375–390); square grids (albums,
  // audiobooks) stay two across as the phone frames draw them.
  const phoneCard = library.kind === 'music' || library.kind === 'audiobook' ? 112 : 88;
  const appearanceBase = {view: appearance.libraryView, cardWidth: (width < 600 ? phoneCard : 170) * (size === 'small' ? .8 : size === 'large' ? 1.2 : 1), backdrops: appearance.showBackdrops, cardSize: size};
  /** A Reading library draws the same wash and count line on its Books pivot, which builds its own header. */
  /** The owner's Scan row is offered only when this library's own management read says the
   *  server allows scanning it. Each setup gets a fresh lease: its own service, the captured
   *  context and library it answers for, and a signal that retires it synchronously. */
  const scan = useLibraryScan(context, library, resources.life);
  const readingHeader = useReadLibrarySummary({context: captured.context, libraryId: library.id, artwork: context.artwork,
    api: resources.life.api, signal: resources.life.signal, isCurrent: resources.life.current, reportError: context.effects.reportError, enabled: isReadingLibrary(library.kind)});
  const libraryChoices = (context.identity?.libraries ?? []).map(choice => ({...choice, count: readingHeader.libraryCounts.find(row => row.id === choice.id)?.text}));
  /** The drawn view summary is the presentation this screen is showing, not the stored default. */
  const shownView = choice?.defaultView === appearance.libraryView ? choice.view : appearance.libraryView;
  const save = () => {
    // A retained menu callback can outlive its route: check the original lifetime first.
    if (!controller || !resources.life.current() || !context.isCurrent() || controller.getSnapshot().queryRevision !== state.queryRevision
      || controller.getSnapshot().revision !== state.revision || !controller.canSaveView()) return;
    const query = controller.query();
    setSaving({libraryId: library.id, pivot: controller.source.pivot, query: query.query ?? null,
      sort: query.sort ?? [], presentation: shownView});
  };
  const musicCounts = musicSummary?.summary.counts.map(count => ({id: count.kind, role: 'context' as const, text: libraryCountText(count)}));
  // desktop-listen #books: an Audiobooks library's header carries its counts too ("8 books · 7 authors").
  const audiobookCounts = audiobookSummary?.summary.counts.map(count => ({id: count.kind, role: 'context' as const, text: libraryCountText(count)}));
  // desktop-listen #music: Music's Discover header offers Shuffle all, the library's songs in a shuffled queue.
  const playRuntime = captured.binding.playback?.runtime;
  const shuffleAll = library.kind === 'music' && playRuntime ? <Button variant="glass" size="small" icon="i-shuffle" onClick={() => {
    if (!resources.life.current() || !context.isCurrent()) return;
    void playRuntime.playFromQueue({query: {libraryId: library.id, pivot: 'songs'}}, {signal: resources.life.signal(), isCurrent: () => resources.life.current() && context.isCurrent()}, {shuffle: true}).catch(context.effects.reportError);
  }}>{t('next.browse.shuffleAll')}</Button> : undefined;
  const header = {library: musicSummary ? {...library, name: musicSummary.name, latestArtwork: musicSummary.summary.latestArtwork} : library,
    libraries: musicLibraries?.map(value => ({id: value.id, name: value.name})) ?? libraryChoices, pivot: requested?.id ?? 'discover', tabs: tabs.map(tab => ({value: tab.id, label: t(tab.label)})),
    wash: readingHeader.wash, counts: library.kind === 'music' ? musicCounts : library.kind === 'audiobook' ? audiobookCounts : readingHeader.counts,
    appearance: appearanceBase, preferences: appearance, libraryViewOverride: choice?.defaultView === appearance.libraryView ? choice.view : undefined,
    artwork: context.artwork,
    // A Reading library's ⋯ draws only the rows this surface can really perform: saving the
    // current view, the owner's Unmatched entry, the owner's Library settings page, and Scan
    // from its own lease, plus the ready original-profile Mark all read action.
    libraryMenu: <>{pins.element}{withLinkToast(libraryMenu({life: resources.life, controller}, context, library, onlineAttention, save, scan, isReadingLibrary(library.kind) ? readingMarkAll : undefined, copyLibraryLink, pins.open), linkToast, () => setLinkToast(null))}</>,
    onLibrary: (id: string) => navigate({}, id), onLibraries: () => {if (resources.life.current()) void router.navigate({to: '/libraries'}).catch(context.effects.reportError);},
    onPivot: (id: string) => {if (!resources.life.current()) return;const tab = tabs.find(tab => tab.id === id);if (!tab) return;
      if (tab.shows === 'periodicals') {navigate({view: 'series', pivot: tab.id});return;}
      if (tab.shows === 'unmatched') {navigate({unmatched: true});return;}
      if (tab.shows === 'playlists') {void router.navigate({to: '/saved', search: {view: 'playlists'}}).catch(context.effects.reportError);return;}
      navigate(libraryTabAddress(tabs, tab));}};
  const empty = <LibraryEmpty api={resources.life.api} queries={context.queries} scope={context.scope} libraryId={library.id} libraryName={library.name}
    owner={context.identity?.viewer.role === 'owner'} onActivity={() => {if (context.isCurrent()) void router.navigate({to: '/dashboard'}).catch(context.effects.reportError);}}/>;
  const bindings = {...header, controller, menuContext: context,
    unmatchedHelp: context.effects.editMetadata ? t('metadata.attention.browseHelp') : undefined,
    env: {t, locale: defaultI18n.region.formatLocale}, reducedMotion: appearance.reducedMotion, owner: context.identity?.viewer.role === 'owner',
    reportError: context.effects.reportError, empty, canSaveView: !!controller?.canSaveView(), onSaveView: save,
    selection: {toolbar: selectionActive ? selection?.toolbar : undefined, actions: (member: BrowseMember, index: number) => ({...actions(member.entry), selection: selection?.entry(member, index)}),
      registerWindow: registerVisible, playSelection},
    onAppearanceChange: (change: Parameters<LibraryBindings['onAppearanceChange']>[0]) => {if (!resources.life.current()) return;if (change.view) setChoice({defaultView: appearance.libraryView, view: change.view});if (change.cardSize) setSize(change.cardSize);}};
  const contentRoute: ContentRoute = {libraryId: library.id, view, ...(search.entityId ? {entityId: search.entityId} : {}), ...(search.category ? {category: search.category} : {}), ...(search.q ? {q: search.q} : {})};
  const unmatched = requested?.shows === 'unmatched';
  const editAttention = (entry: AttentionEntry, anchor: HTMLElement, matching = false) => {
    if (!resources.life.current() || entry.libraryId !== library.id) return;
    const edit = context.effects.editMetadata;
    if (!edit) return;
    if (matching && entry.kind === 'song' && entry.parentId) {
      void edit({libraryId: library.id, entityId: entry.parentId, entityKind: 'album', title: entry.parentTitle ?? entry.title, initialTab: 'matching'}, anchor).catch(context.effects.reportError);
      return;
    }
    if (entry.kind === 'show' || entry.kind === 'album' || entry.kind === 'book') {
      void edit({libraryId: library.id, entityId: entry.entityId, entityKind: entry.kind, title: entry.title, initialTab: matching ? 'matching' : 'general'}, anchor).catch(context.effects.reportError);
      return;
    }
    if (entry.kind !== 'item' && entry.kind !== 'song') return;
    const kind = entry.kind === 'song' ? 'song' : entry.itemKind;
    if (kind !== 'movie' && kind !== 'episode' && kind !== 'song' && kind !== 'audiobook_file') return;
    void edit({libraryId: library.id, itemId: entry.entityId, kind, title: entry.title}, anchor).catch(context.effects.reportError);
  };
  const openAttention = (entry: {kind: string; itemKind: string; entityId: string; parentId: string}) => {
    if (entry.kind === 'show' || entry.itemKind === 'episode') navigate({view: 'show', entityId: entry.kind === 'show' ? entry.entityId : entry.parentId});
    else if (entry.kind === 'album') navigate({view: 'album', entityId: entry.entityId});
    else if (entry.kind === 'song') navigate({view: 'album', entityId: entry.parentId});
    else if (entry.kind === 'book') navigate({view: 'book', entityId: entry.entityId});
    else if (entry.entityId) navigate({view: 'browse', entityId: entry.entityId});
  };
  if (library.kind === 'audiobook' && (view === 'book' || view === 'author' || view === 'book_series') && search.entityId)
    return <AudiobookEntry context={context} target={{libraryId: library.id, view, entityId: search.entityId}}/>;
  return <>{queryActions?.surface}{unmatched ? <><LibraryIdentity {...header}/><NeedsAttention artwork={context.artwork} libraryId={library.id} libraryName={library.name} api={resources.life.api} onOpen={openAttention} onEdit={context.effects.editMetadata ? editAttention : undefined}/></>
    : view === 'show' || view === 'season' ? <LibraryShow captured={captured} libraryId={library.id} search={search}/>
    : view === 'artist' && search.entityId ? <ArtistScreen context={context} libraryId={library.id} artistId={search.entityId}
      onBack={() => {const back = context.effects.back; if (back) {back(); return;} void router.navigate({to: '/library/$libraryId', params: {libraryId: library.id}, search: {}}).catch(context.effects.reportError);}} />
    : view === 'album' && search.entityId ? <AlbumScreen context={context} libraryId={library.id} albumId={search.entityId} actions={actions}
      onArtist={artistId => navigate({view: 'artist', entityId: artistId})}
      onBack={() => {const back = context.effects.back; if (back) {back(); return;} void router.navigate({to: '/library/$libraryId', params: {libraryId: library.id}, search: {}}).catch(context.effects.reportError);}} />
    : browse ? <LibraryScreen {...bindings}/> : <LibraryDiscoveryScreen menuContext={context} service={resources.service} route={contentRoute} musicMixes={musicMixes} mixesTarget={search.section === 'mixes'} reportError={context.effects.reportError} header={<LibraryIdentity {...header} action={shuffleAll}/>} empty={empty}
      appearance={appearanceBase} preferences={appearance} reducedMotion={appearance.reducedMotion} artwork={context.artwork} env={bindings.env}
      actions={discoveryActions} nowPlayingAlbumId={nowPlayingAlbumId} onSeeAll={section => {if (!resources.life.current() || resources.service.getSnapshot().phase !== 'ready' || !discoverySections(resources.service.getSnapshot()).includes(section)) return;const target = section.seeAll;if (target) navigate({pivot: target.pivot, query: target.query, sort: target.sort});}}
      selectionInput={context.identity && (context.identity.viewer.authority === 'local' || context.identity.viewer.authority === 'hosted') ? {context: resources.screen, playback: runtime,
        principal: {serverId: context.identity.viewer.serverId, authority: context.identity.viewer.authority, accountId: context.identity.viewer.accountId, profileId: context.identity.viewer.profileId}} : undefined}/>}
    {saving && <SaveView context={context} definition={saving} onClose={() => setSaving(undefined)}/>}</>;
}
function LibraryRouteScreen() {
  const [readingGeneration, setReadingGeneration] = useState(0);
  const protectedContext = useProtectedWebScreenContext(), {libraryId} = useParams({from: '/library/$libraryId'});
  // The toolbar's filters and order live in the address too (LB-1); changing them is the same opening.
  const addressKey = useLocation({select: libraryAddressKey});
  const opening = useMemo(() => ({addressKey}), [addressKey]);
  const guard = useLibraryRouteGuard(protectedContext?.context, opening, () => libraryAddressKey(router.state.location) === addressKey, {libraryId}, error => protectedContext?.binding.home.effects.reportError(error));
  const context = useGuardedLibraryContext(protectedContext?.context, guard);
  const captured = protectedContext && context ? {...protectedContext, context} : undefined;
  const facts = useShellFacts(captured?.context);
  const search = parseLibraryScreenSearch(useSearch({from: '/library/$libraryId'}));
  const destination = useMusicLibrarySummary(captured, libraryId, search.section === 'mixes');
  const routeLibrary = useRouteLibrary(captured?.context, libraryId);
  const library = search.section === 'mixes' ? destination.library && {...destination.library, ...(routeLibrary ? {name: routeLibrary.name, kind: routeLibrary.kind} : {})} : routeLibrary;
  if (captured && search.section === 'mixes' && destination.phase === 'loading') return <Skeleton height={320}/>;
  if (captured && search.section === 'mixes' && destination.phase === 'error') return <StateView title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: destination.retry}}/>;
  return !captured?.context.isCurrent() ? null : library ? isReadingLibrary(library.kind) ? <ReadingLibrary key={`${libraryId}:${readingGeneration}`} captured={captured} library={library} search={search} onMarked={() => setReadingGeneration(value => value + 1)}/>
    : <BoundLibrary key={libraryId} captured={captured} library={library} search={search} musicLookup={search.section === 'mixes' ? destination : undefined}/>
    // P2-74 E-web-16: a library directory that did not answer (a 503) is not a missing library.
    // It is the library's retry state, laid out as every page state on the standard backdrop.
    : facts?.identity.librariesUnavailable ? <StateView variant="page" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
      primary={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => {if (captured.context.isCurrent()) captured.context.retryLibraries?.();}}}/>
    : <StateView variant="page" title={t('next.states.notFound')} body={t('next.states.notFoundBody')}/>;
}
export default defineWebScreen({id: 'library', component: LibraryRouteScreen, validateSearch: parseLibraryScreenSearch});
