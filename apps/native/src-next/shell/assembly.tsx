import {NativeTVReceiver} from '../screens/settings-live/NativeTVReceiver';
import {createNativeListeningPlaylist} from './listening-playlist';
import {createNativeListeningQueuePolicy} from './listening-queue-policy';
import {createNativeListeningSongDetail} from './listening-song-detail';
import {NativeHomeEmpty} from '../screens/home/HomeEmpty';
import React, {type RefObject} from 'react';
import {BackHandler, Platform, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {UserMutationRecoveryModel, type UserMutationRecoveryDestination} from '../../../../packages/client-core/src/user-mutation-recovery';
import {NativePendingChanges} from './pending-changes';
import {PendingChanges} from './PendingChanges';
import {HomeSelectionController, homeSelectionContext} from '../../../../packages/client-core/src/home-selection/index';
import {homeChannelSelection, homeCollectionSelection, homeEntryCapabilities, homeEntrySelection} from '../../../../packages/client-core/src/home-selection';
import type {HomeRow, HomeSpotlight} from '../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {SelectionContext} from '../../../../packages/client-core/src/selection-model/index';
import {LibraryManagementService} from '../../../../packages/client-core/src/library-management';
import {homeSpotlightModels} from '../../../../packages/client-core/src/presentation/home-spotlight';
import {selectionHaptic} from '../platform/haptics';
import {homeActionDiagnostics} from '../platform/home-action-diagnostics';
import {nativeScreenServices, NativeScreenFeedbackHost} from '../screens/native-screen-services';
import {OwnerTag} from '../kit/shell';
import {HomeSelectionBars} from './HomeSelectionBars';
import {HomeSelectionOverlays} from './HomeSelectionOverlays';
import {nativeAccountMenu, nativeMenuRouteAvailable} from './account';
import {libraryScanReadiness} from './library-scan';
import {NativeDownloadSheet} from '../screens/downloads/DownloadSheet';
import {createNativeHomeSelectionAction, nativeSelectionActionInput, type NativeHomeQueryActions, type NativeSelectionActionPresentation} from '../screens/library/selection-actions';
import {askNativeDownload, closeNativeDownload, getNativeDownloadSnapshot} from '../screens/downloads/request';
import {offlineDownloads} from '../platform/offline-downloads';
import {selectedItemsLabel} from './selection-display';
import {nativeHomeSelectionScope} from './selection-scope';
import {nativeEntryRoute, nativeEntryRouteSupported, nativeRailDestination, type NativeShellRoute} from './routes';
import {nativeShellRoutes} from './route-registry';
import {sameAuthority, nativeServerHost, type ShellDestination} from './model';
import {createNativeMusicDiscoverNavigation} from './music-discover-navigation';
import {homePlayTarget} from '../screens/home/model';
import {planHomeWindows} from './home-windows';
import {isReadingLibraryKind} from '../screens/read/platform';
import {openNativeReadingBookEntry} from '../screens/read/reading-books-open';
import {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure} from '../screens/home/entry-lifetime';
import type {NativeShellBindings, NativeShellBootstrapContext} from './bootstrap';
import type {NativeShellRuntime, NativeTouchStacksPort} from './runtime';
import {createNativeMusicMixes, type NativeMusicMixesCaller} from '../screens/destinations/music/mixes';

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const failure = (code: string) => Object.assign(new Error(code), {code});
type Registration = {row: HomeRow; order: number; context: string; coordinate: number; first: number; last: number};
type Window = {registration?: Registration; row: HomeRow; order: number; first: number; last: number; cleanup?: () => void; keys: Map<string, string>; memberWindows: Map<string, string>; physicalIds?: readonly string[]; retainedIds?: readonly string[]; visible: boolean; visibleIds?: readonly string[]};

/** Genuine default assembly. The controller and playback runtime retain all business state. */
export function createNativeShellBindings(context: NativeShellBootstrapContext): NativeShellBindings {
  const initial = context.home.getSnapshot().document, session = context.session();
  if (!initial || !session || !context.current()) throw failure('native_home_authority_required');
  const routes = nativeShellRoutes.bind(context);
  const feedback = nativeScreenServices(context);
  const scope = nativeHomeSelectionScope(session.viewer);
  let alive = true, runtime: NativeShellRuntime | undefined;
  const tvPageBack = new Map<string, {handler: () => boolean}[]>();
  let homeTopPending = false;
  let tvRailBack: (() => boolean) | undefined;
  const listeners = new Set<() => void>(), windows = new Map<string, Window>(), spotlights = new Map<ContentEntry, {id: string; key: string; cleanup: () => void; spotlight: HomeSpotlight; index: number; context: string}>(), scroll = new Map<string, () => void>();
  const operations = new Set<AbortController>();
  const publish = () => {if (alive) for (const listener of [...listeners]) listener();};
  const currentHome = () => alive && context.current() && context.navigation.getSnapshot().route.kind === 'shell' && context.navigation.getSnapshot().destination.kind === 'home';
  const isCurrent = (candidate: SelectionContext) => {
    const document = context.home.getSnapshot().document;
    return currentHome() && !!document && same(candidate, homeSelectionContext(scope, document));
  };
  const reportError = (error: unknown, origin: 'assembly-error' | 'home-registration' | 'artwork-error' = 'assembly-error') => {
    homeActionDiagnostics.error(origin, error);
    if (!alive || !context.current()) return;
    feedback.reportError(error, 'home');
  };
  const open = (route: NativeShellRoute) => {if (alive && context.current()) context.navigation.open(route, context.authority());};
  const mutationCoordinator = context.api.getUserMutationCoordinator();
  const mutationScope = () => alive && context.current() ? context.api.getUserMutationScope() : undefined;
  const pendingChanges = mutationCoordinator ? new NativePendingChanges(new UserMutationRecoveryModel({
    api: context.api, coordinator: mutationCoordinator, scope: mutationScope,
  }), mutationScope, context.subscribeAuthority) : undefined;
  const reviewDestination = (destination: UserMutationRecoveryDestination) => {
    if (!pendingChanges?.current()) return;
    if (destination.kind === 'saved') open({kind: 'shell', destination: {kind: 'saved'}});
    else if (destination.kind === 'settings' && destination.server) {
      const page = destination.section === 'metadata' ? 'metadata' : destination.section === 'storage' ? 'storage' : destination.section === 'live' ? 'live' : undefined;
      open({kind: 'server-settings', ...(page ? {page} : {})});
    }
    else if (destination.kind === 'playback') {
      const playback = context.playback.getSnapshot();
      if (playback.fence && (!destination.sessionId || destination.sessionId === playback.fence.sessionId)) context.playback.open(playback.fence);
      else open({kind: 'shell', destination: {kind: 'settings'}});
    } else open({kind: 'shell', destination: {kind: 'settings'}});
  };
  const platform = {platform: Platform.isTV ? 'tv' as const : 'phone-app' as const, playbackAvailable: true, queueAvailable: true};
  const environment = () => {
    const document = context.home.getSnapshot().document;
    const canEditMetadata = document?.spotlight.some(value => value.entry.homeActions?.editMetadata || value.entry.homeActions?.selection.edit)
      || document?.rows.some(row => row.entries.some(entry => entry.homeActions?.editMetadata || entry.homeActions?.selection.edit)
        || row.collection?.actions?.editMetadata || row.collection?.actions?.selection.edit);
    return {platform: Platform.isTV ? 'tv' as const : 'phone-app' as const, isOwner: context.account.account.role === 'owner', canEditMetadata: canEditMetadata === true};
  };
  const playback = context.playback.selectionPlayback(isCurrent);
  const controller = new HomeSelectionController({api: context.api, events: context.events, home: context.home, context: homeSelectionContext(scope, initial), environment: environment(), isCurrent,
    requestId: feedback.requestId, playback, reportError,
    routes: {openEntry: entry => {if (!nativeEntryRouteSupported(entry)) reportError(failure('unavailable')); else open(nativeEntryRoute(entry));}, openCollection: collection => open({kind: 'collection', libraryId: collection.collection.libraryId, collectionId: collection.collection.id, title: collection.collection.name}),
      openDestination: destination => open({kind: 'saved-destination', destination})}});
  let queryAction: NativeSelectionActionPresentation | undefined, stopQueryAction: (() => void) | undefined;
  const queryListeners = new Set<() => void>();
  const publishQuery = () => {for (const listener of [...queryListeners]) listener(); publish();};
  const closeQueryAction = (expected?: NativeSelectionActionPresentation) => {
    if (expected && queryAction !== expected) return;
    const closing = queryAction; queryAction = undefined;
    stopQueryAction?.(); stopQueryAction = undefined;
    if (closing) {
      const request = getNativeDownloadSnapshot();
      if (request?.queryAction === closing) closeNativeDownload(request);
      closing.dispose(); publishQuery();
    }
  };
  const queryActions: NativeHomeQueryActions = Object.freeze({
    subscribe: listener => {queryListeners.add(listener); return () => {queryListeners.delete(listener);};},
    getSnapshot: () => queryAction,
    offer: (kind, view) => {
      if (!currentHome() || view !== controller.selection.getSnapshot() || view.target?.mode !== 'query') return false;
      try {nativeSelectionActionInput(view.target, kind, context); return true;} catch {return false;}
    },
    open: (kind, view) => {
      if (queryAction || !queryActions.offer(kind, view)) return;
      const state = controller.getSnapshot();
      if (state.deletion || state.editor || state.picker || state.rating || state.mixedEdit || state.action.phase !== 'idle') return;
      try {
        const action = createNativeHomeSelectionAction(context, controller, undefined, {
          follow: (operationId, deviceId) => offlineDownloads.followSelectionAction(operationId, deviceId),
        });
        if (view !== controller.selection.getSnapshot() || action.target !== view.target) {action.dispose(); return;}
        queryAction = action;
        stopQueryAction = action.subscribe(() => {if (!action.current()) closeQueryAction(action); else publishQuery();});
        if (kind === 'delete') void action.openDelete().catch(error => {if (queryAction === action && action.current()) reportError(error);});
        else askNativeDownload({title: selectedItemsLabel(view.summary), queryAction: action,
          lifetime: {signal: action.source.signal!, isCurrent: action.current}});
        publishQuery();
      } catch (error) {reportError(error);}
    },
    close: closeQueryAction,
  });
  let capturedMenu: {menu: NonNullable<ReturnType<HomeSelectionController['getSnapshot']>['menu']>; anchorRef?: RefObject<View | null>; owner: object} | undefined;
  const windowId = (rowId: string) => `home-row:${rowId}`;
  const members = new Map<string, Registration & {cleanup: () => void}>();
  let coordinateDocument = initial;
  const coordinates = new Map<HomeRow, number>();
  const registrationContext = () => {
    const document = context.home.getSnapshot().document!;
    if (coordinateDocument !== document || !coordinates.size) {
      coordinateDocument = document; coordinates.clear();
      const visit = (row: HomeRow) => {coordinates.set(row, coordinates.size); for (const child of row.children ?? []) visit(child);};
      for (const row of document.rows) visit(row);
    }
    return JSON.stringify(homeSelectionContext(scope, document));
  };
  const sameRegistration = (before: Registration | undefined, next: Registration) => !!before && before.row === next.row && before.order === next.order
    && before.context === next.context && before.coordinate === next.coordinate && before.first === next.first && before.last === next.last;
  // Cleanup never throws (P2-74 E-tv-30): a throw from an unmount or deregistration reaches the app boundary.
  const release = (cleanup?: () => void) => {try {cleanup?.();} catch (error) {reportError(error, 'home-registration');}};
  // An expired Live row's window is dropped, never thrown: the next Home read brings a current one.
  const dropWindow = (window: Window) => {
    const cleanup = window.cleanup, prefix = `home-row-item:${JSON.stringify([window.row.id]).slice(0, -1)},`;
    window.cleanup = undefined; window.registration = undefined; window.keys.clear(); window.memberWindows.clear();
    for (const [id, member] of members) if (id.startsWith(prefix)) {release(member.cleanup); members.delete(id);}
    release(cleanup);
  };
  const register = (window: Window, ids?: readonly string[]) => {
    const row = window.row, stamp = registrationContext(), coordinate = coordinates.get(row) ?? -1;
    const count = row.live?.channels.length ?? row.entries.length;
    const first = Math.min(Math.max(0, window.first - 2), Math.max(0, count - 1)), last = Math.min(Math.max(first, window.last + 2), Math.max(0, count - 1));
    const registration = {row, order: window.order, context: stamp, coordinate, first, last};
    if (!Platform.isTV || row.collection) {if (window.cleanup && sameRegistration(window.registration, registration)) return;}
    window.keys.clear(); window.memberWindows.clear();
    // TV presentation may keep a different order while focused. Admission always
    // uses genuine latest document objects in bounded, contiguous one-item windows.
    if (Platform.isTV && window.physicalIds && !row.collection) {
      for (const id of ids ?? []) {
        const index = row.live ? row.live.channels.findIndex(value => JSON.stringify([value.channel.sourceId, value.channel.id]) === id)
          : row.entries.findIndex(value => value.id === id);
        if (index < 0) continue;
        const memberWindow = `home-row-item:${JSON.stringify([row.id, id])}`;
        const entry = row.entries[index], channel = row.live?.channels[index];
        const memberRegistration = {...registration, first: index, last: index};
        const previous = members.get(memberWindow);
        const result = previous && sameRegistration(previous, memberRegistration) ? {ok: true as const, unregister: previous.cleanup} : controller.registerWindow({id: memberWindow, row, sectionOrder: window.order, start: row.start + index,
          ...(channel ? {channels: [channel]} : {entries: [entry]})});
        if (!result.ok && result.reason === 'stale-home-live-window') {dropWindow(window); return;}
        if (!result.ok) throw failure(result.reason);
        members.set(memberWindow, {...memberRegistration, cleanup: result.unregister});
        const identity = channel ? homeChannelSelection(channel, platform) : homeEntrySelection(entry, platform);
        if (identity) {window.keys.set(id, controller.key(identity)); window.memberWindows.set(id, memberWindow);}
      }
      return;
    }
    const entries = row.entries.slice(first, last + 1), channels = row.live?.channels.slice(first, last + 1);
    const result = controller.registerWindow({id: windowId(row.id), row, sectionOrder: window.order, start: row.start + first,
      ...(channels ? {channels} : row.collection ? {} : {entries})});
    if (!result.ok && result.reason === 'stale-home-live-window') {dropWindow(window); return;}
    if (!result.ok) throw failure(result.reason);
    const previousCleanup = window.cleanup; window.cleanup = result.unregister; window.registration = registration; previousCleanup?.();
    for (const entry of entries) {const identity = homeEntrySelection(entry, platform); if (identity) window.keys.set(entry.id, controller.key(identity));}
    for (const channel of channels ?? []) window.keys.set(JSON.stringify([channel.channel.sourceId, channel.channel.id]), controller.key(homeChannelSelection(channel, platform)));
    if (row.collection) window.keys.set(row.collection.collection.id, controller.key(homeCollectionSelection(row.collection, platform)));
    for (const id of window.keys.keys()) window.memberWindows.set(id, windowId(row.id));
  };
  const reconcileWindows = (spotlightCount = spotlights.size) => {
    if (!Platform.isTV) {for (const window of windows.values()) register(window); return;}
    const populations = [...windows.values()].map(window => ({id: window.row.id, visible: window.visible, first: window.first, last: window.last,
      physicalIds: window.row.collection ? [window.row.collection.collection.id] : window.physicalIds ?? [], retainedIds: window.retainedIds, visibleIds: window.visibleIds}));
    const plan = planHomeWindows(populations, spotlightCount);
    const desired = new Set<string>();
    for (const window of windows.values()) {
      if (window.row.collection) continue;
      for (const id of plan.get(window.row.id) ?? []) desired.add(`home-row-item:${JSON.stringify([window.row.id, id])}`);
    }
    for (const [id, member] of members) if (!desired.has(id)) {member.cleanup(); members.delete(id);}
    for (const window of windows.values()) {
      if (window.row.collection && !(plan.get(window.row.id)?.length)) {window.cleanup?.(); window.cleanup = undefined; window.keys.clear(); window.memberWindows.clear();}
      else register(window, plan.get(window.row.id));
    }
  };
  const registerSpotlights = (values: readonly HomeSpotlight[]) => {
    const before = new Map([...spotlights.values()].map(value => [value.id, value]));
    spotlights.clear();
    const offeredIds = new Set(values.map(value => {const identity = homeEntrySelection(value.entry, platform);
      return identity ? `home-spotlight:${JSON.stringify([identity.kind, 'libraryId' in identity ? identity.libraryId : '', 'id' in identity ? identity.id : ''])}` : undefined;}));
    for (const [id, value] of before) if (!offeredIds.has(id)) {value.cleanup(); before.delete(id);}
    reconcileWindows(values.length);
    for (const [index, spotlight] of values.entries()) {
      const identity = homeEntrySelection(spotlight.entry, platform); if (!identity) continue;
      const id = `home-spotlight:${JSON.stringify([identity.kind, 'libraryId' in identity ? identity.libraryId : '', 'id' in identity ? identity.id : ''])}`;
      const previous = before.get(id), stamp = registrationContext();
      if (previous?.spotlight === spotlight && previous.index === index && previous.context === stamp) {spotlights.set(spotlight.entry, previous); before.delete(id); continue;}
      const result = controller.registerSpotlight({id, spotlight});
      if (!result.ok) throw failure(result.reason);
      spotlights.set(spotlight.entry, {id, key: controller.key(identity), cleanup: result.unregister, spotlight, index, context: stamp});
      before.get(id)?.cleanup(); before.delete(id);
    }
    for (const value of before.values()) value.cleanup();
  };
  registerSpotlights(initial.spotlight);
  const find = (entry: ContentEntry, rowId?: string) => {
    const spotlight = spotlights.get(entry) ?? [...spotlights].find(([value]) => value.id === entry.id && value.libraryId === entry.libraryId && value.kind === entry.kind)?.[1];
    if (!rowId && spotlight) return {id: spotlight.id, key: spotlight.key, owner: spotlight};
    const window = rowId ? windows.get(rowId) : [...windows.values()].find(value => value.row.entries.includes(entry));
    const key = window?.keys.get(entry.id); if (!window || !key) throw failure('unregistered_home_entry');
    const id = window.memberWindows.get(entry.id)!;
    return {id, key, owner: members.get(id) ?? window.registration};
  };
  const menuOwner = (id: string): object | undefined => members.get(id) ?? [...spotlights.values()].find(value => value.id === id)
    ?? [...windows.values()].find(window => [...window.memberWindows.values()].includes(id))?.registration;
  // Only the native anchor/expanded binding follows this capture. The canonical
  // controller continues to own permission checks and accepted command lifetimes.
  const currentMenuCapture = () => {
    const menu = controller.getSnapshot().menu;
    if (!currentHome() || !capturedMenu || !menu || capturedMenu.menu !== menu || menuOwner(menu.windowId) !== capturedMenu.owner) return undefined;
    return capturedMenu;
  };
  const openMenu = (windowId: string, key: string, anchorRef?: RefObject<View | null>) => {
    if (!currentHome()) return;
    const owner = menuOwner(windowId); if (!owner) return;
    capturedMenu = undefined;
    controller.openMenu(windowId, key);
    const menu = controller.getSnapshot().menu;
    capturedMenu = menu && menu.windowId === windowId && menu.key === key ? {menu, anchorRef, owner} : undefined;
    publish();
  };
  const menu = (entry: ContentEntry, rowId?: string, anchorRef?: RefObject<View | null>) => {homeActionDiagnostics.action('home-assembly', 'menu'); if (!currentHome()) return; const member = find(entry, rowId); openMenu(member.id, member.key, anchorRef);};
  const onDocument = () => {
    if (!alive || !context.current()) return;
    const document = context.home.getSnapshot().document;
    if (!document || document.serverId !== scope.serverId || document.viewerFence !== context.authority().viewerFence) return;
    const next = homeSelectionContext(scope, document);
    if (!same(next, controller.getSnapshot().context)) {controller.setContext(next, environment());}
    if (!currentHome()) return;
    for (const [id, window] of windows) {
      const row = document.rows.find(value => value.id === id);
      if (!row) {release(window.cleanup); windows.delete(id);} else window.row = row;
    }
    // A Home re-read never throws into the Home service (E-tv-30): a window that can't register is reported.
    try {registerSpotlights(document.spotlight); reconcileWindows();} catch (error) {reportError(error, 'home-registration');}
    publish();
  };
  const scopeForServices = {serverId: scope.serverId, viewerId: JSON.stringify([scope.authority, scope.accountId, scope.profileId])};
  const management = context.account.account.role === 'owner' ? new LibraryManagementService({api: context.api, scope: scopeForServices}) : undefined;
  const cleanups = [context.home.subscribe(onDocument), controller.subscribe(() => {if (capturedMenu?.menu !== controller.getSnapshot().menu) capturedMenu = undefined; publish();}), context.navigation.subscribe(() => {
    closeQueryAction(); controller.clear();
    if (!currentHome()) homeTopPending = false;
    if (currentHome()) onDocument();
    publish();
  }), nativeShellRoutes.subscribe(publish)];
  const back = BackHandler.addEventListener('hardwareBackPress', () => {
    if (!alive || !context.current()) return false;
    const state = controller.getSnapshot();
    if (queryAction) closeQueryAction(queryAction);
    else if (state.rating) controller.closeRating();
    else if (state.deletion) controller.closeDelete();
    else if (state.editor || state.mixedEdit) controller.closeEditor();
    else if (state.picker) controller.closePicker();
    else if (state.menu) controller.closeMenu();
    else if (state.selection.active) controller.clear();
    else {
      if (routes.back()) return true;
      if (context.playback.getSnapshot().dock.local?.expanded) return false;
      // Menus/selection own Back first, then the player, then the page.
      if (Platform.isTV && tvRailBack?.()) return true;
      const navigation = context.navigation.getSnapshot();
      if (Platform.isTV && navigation.route.kind === 'shell') {
        const pageBack = tvPageBack.get(JSON.stringify(navigation.destination));
        if (pageBack && [...pageBack].reverse().some(registration => registration.handler())) return true;
        // Rail-page Back is a ladder, independent of the order pages were visited.
        if (navigation.destination.kind === 'home') return false;
        homeTopPending = true;
        open({kind: 'shell', destination: {kind: 'home'}});
        const homeTop = scroll.get(JSON.stringify({kind: 'home'}));
        if (homeTop && currentHome()) {homeTopPending = false; homeTop();}
        return true;
      }
      return context.navigation.back(context.authority());
    }
    return true;
  });
  cleanups.push(() => back.remove());
  const run = (work: (signal: AbortSignal) => Promise<void>) => {
    const operation = new AbortController(); operations.add(operation);
    void work(operation.signal).catch(reportError).finally(() => operations.delete(operation));
  };
  const render = (route: NativeShellRoute) => runtime && context.current() ? routes.render(route, runtime) : null;
  const scrollToTop = (destination: ShellDestination) => {if (context.current()) {const handler = scroll.get(JSON.stringify(destination)); if (handler) handler(); else {const route = context.navigation.getSnapshot().route; routes.scrollToTop(route);}}};
  // motion.html 2.9-2.11: touch hosts every tab's stack natively. Television keeps one route layer.
  const stacks: NativeTouchStacksPort | undefined = Platform.isTV ? undefined : Object.freeze({
    subscribe: context.navigation.subscribe,
    getSnapshot: () => context.navigation.tabStacks(),
    renderRoute: (route: NativeShellRoute) => route.kind === 'shell' ? null : render(route),
    commitNativePop: (occurrence: string) => alive && context.current() && context.navigation.commitNativePop(context.navigation.getSnapshot(), occurrence),
    selectTab: (destination: ShellDestination, anyRoot?: boolean) => {if (alive && context.current()) context.navigation.selectTab(destination, context.authority(), anyRoot);},
    reselectTab: (destination: ShellDestination) => {if (alive && context.current() && !context.navigation.popToRoot(context.authority())) scrollToTop(destination);},
  });
  const ownerTag = <OwnerTag audience="owner" variant="badge"/>;
  // music-plus 16/18: the Mixes row belongs to the shell session that made it. The principal
  // and lease are captured once; a later authority or route never supplies either.
  const musicScope = context.authority(), viewerAuthority = session.viewer.authority;
  if (viewerAuthority !== 'local' && viewerAuthority !== 'hosted') throw failure('native_music_authority_required');
  const musicPrincipal = Object.freeze({serverId: musicScope.serverId, authority: viewerAuthority, accountId: musicScope.accountId, profileId: musicScope.profileId});
  const mixesLife = new AbortController();
  const mixesRoute = (route: NativeShellRoute) => {
    if (route.kind !== 'shell') return false;
    const destination = route.destination;
    return destination.kind === 'home' || destination.kind === 'library' && context.libraries().some(library => library.id === destination.libraryId && library.kind === 'music');
  };
  const captureMixesCaller = (): NativeMusicMixesCaller | undefined => {
    const navigation = context.navigation.getSnapshot(), authority = navigation.authority, revision = navigation.revision;
    if (!mixesRoute(navigation.route)) return undefined;
    const lease = new AbortController();
    return Object.freeze({lifetime: Object.freeze({signal: lease.signal,
      isCurrent: () => {
        const now = context.navigation.getSnapshot();
        return alive && context.current() && now.revision === revision && mixesRoute(now.route) && sameAuthority(now.authority, authority);
      }}), dispose: () => lease.abort()});
  };
  const mixes = createNativeMusicMixes({api: context.api, principal: musicPrincipal, requestId: feedback.requestId, signal: mixesLife.signal,
    isCurrent: () => alive && context.current(), captureCaller: captureMixesCaller, subscribeRevision: context.navigation.subscribe,
    revision: () => context.navigation.getSnapshot().revision, playback: () => runtime?.playback, t: defaultI18n.t});
  const musicNavigation = createNativeMusicDiscoverNavigation(context, () => runtime);
  const listeningPlaylist = Platform.isTV ? createNativeListeningPlaylist(context) : undefined;
  const listeningQueuePolicy = createNativeListeningQueuePolicy(context);
  const listeningSongDetail = createNativeListeningSongDetail(context);
  const playHome: NativeShellBindings['actions']['play'] = (entry, target) => {
        homeActionDiagnostics.action('home-assembly', 'primary-play');
        if (!currentHome() || !homeEntryCapabilities(entry, platform).playable) {homeActionDiagnostics.action('home-assembly', 'primary-play', 'guard-refused'); return;}
        const document = context.home.getSnapshot().document!;
        const offered = homeSpotlightModels(document).some(value => value.entry === entry && same(value.primary?.entry.playback, target)) || document.rows.some(row => row.entries.includes(entry) && same(homePlayTarget(row, entry)?.playback, target));
        if (!offered) {homeActionDiagnostics.action('home-assembly', 'primary-play', 'unoffered'); reportError(failure('unoffered_home_playback')); return;}
        const selected = (() => {try {return find(entry);} catch (error) {reportError(error); return undefined;}})();
        if (!selected) return;
        const signal = new AbortController(); operations.add(signal);
        // E-tv-36: checked current just above; once dispatched the start belongs to the player.
        const operation = nativeDispatchedPlayLifetime(() => alive && context.current(), context.subscribeAuthority, signal.signal);
        homeActionDiagnostics.action('home-assembly', 'primary-play', 'dispatch');
        void context.playback.start({entry, target, ...(entry.kind === 'chapter' && same(entry.playback, target) && target.startSeconds !== undefined ? {startIntent: 'explicit' as const} : {}), lifetime: operation.lifetime})
          .catch(error => reportDispatchedPlayFailure(error, () => alive && context.current(), reportError))
          .finally(() => {operation.dispose(); operations.delete(signal);});
  };
  return {get account() {
    const account = nativeAccountMenu(context.account, open, Platform.isTV, nativeMenuRouteAvailable);
    return pendingChanges ? {...account, actions: [...account.actions, {id: 'pending-changes', label: defaultI18n.t('recovery.title'), icon: 'i-clock' as const, onPress: pendingChanges.open}]} : account;
  }, attach: value => {runtime = value;},
    renderPendingChanges: () => pendingChanges && runtime ? <PendingChanges owner={pendingChanges} runtime={runtime} openDestination={reviewDestination}/> : null,
    getState: () => {const state = controller.getSnapshot(), navigation = context.navigation.getSnapshot(); return {destination: navigation.destination,
      railDestination: nativeRailDestination(navigation, context.libraries()), contentVisible: navigation.route.kind === 'shell', selectionActive: state.selection.active,
      homeVisible: currentHome(), homeObscured: !!(state.menu || state.picker || state.editor || state.deletion || state.rating || state.mixedEdit)};},
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    listeningPlaylist,
    listeningQueuePolicy,
    listeningSongDetail,
    actions: {
      navigate: entry => {
        if (!currentHome()) return;
        if (entry.kind === 'song' || entry.kind === 'audiobook_file') {
          if (entry.playback) playHome(entry, entry.playback); else reportError(failure('unavailable'));
          return;
        }
        if (entry.kind === 'ebook' || entry.kind === 'comic') {
          const libraryId = entry.libraryId, kind = entry.kind === 'ebook' ? 'book' : 'comic';
          const navigation = context.navigation.getSnapshot(), document = context.home.getSnapshot().document;
          const selected = (() => {try {return find(entry);} catch {return undefined;}})();
          if (!libraryId || !selected || !document || !context.libraries().some(library => library.id === libraryId && library.kind === kind)) {reportError(failure('unavailable')); return;}
          const signal = new AbortController(); operations.add(signal);
          const operation = nativeHomeEntryLifetime(() => {
            if (!currentHome() || context.navigation.getSnapshot() !== navigation || context.home.getSnapshot().document !== document
              || !context.libraries().some(library => library.id === libraryId && library.kind === kind)) return false;
            try {const member = find(entry); return member.id === selected.id && member.key === selected.key && member.owner === selected.owner;} catch {return false;}
          }, [context.navigation.subscribe, context.home.subscribe, controller.subscribe, context.subscribeAuthority], signal.signal);
          void openNativeReadingBookEntry(context, {libraryId, entry, origin: {route: navigation.route, revision: navigation.revision}, resident: operation.lifetime.isCurrent, signal: operation.lifetime.signal})
            .then(opened => {if (!opened && operation.lifetime.isCurrent()) reportError(failure('unavailable'));})
            .catch(error => {if (operation.lifetime.isCurrent()) reportError(error);})
            .finally(() => {operation.dispose(); operations.delete(signal);});
          return;
        }
        if (!nativeEntryRouteSupported(entry)) {reportError(failure('unavailable')); return;}
        open(nativeEntryRoute(entry));
      },
      play: playHome,
      more: menu,
      myList: async (entry, selected) => {
        if (!currentHome()) throw failure('stale_home_context');
        const offered = homeEntryCapabilities(entry, platform);
        if (offered.watchlisted === undefined) throw failure('action_not_offered');
        if (offered.watchlisted === selected) return;
        const member = find(entry); controller.openMenu(member.id, member.key);
        await controller.executeMenu('watchlist');
        const action = controller.getSnapshot().action;
        if (action.error) throw failure(action.error.code);
      },
      channel: channel => run(async signal => {if (currentHome()) await playback.playChannel(channel, controller.getSnapshot().context, signal);}),
      channelMore: (channel, anchorRef) => {
        const window = [...windows.values()].find(value => value.row.live?.channels.includes(channel));
        const key = window?.keys.get(JSON.stringify([channel.channel.sourceId, channel.channel.id]));
        if (!window || !key || !currentHome()) return;
        openMenu(window.memberWindows.get(JSON.stringify([channel.channel.sourceId, channel.channel.id]))!, key, anchorRef);
      },
      collectionMore: (collection, rowId, anchorRef) => {const window = windows.get(rowId), key = window?.keys.get(collection.collection.id); if (window && key && currentHome()) openMenu(window.memberWindows.get(collection.collection.id)!, key, anchorRef);},
      seeAll: destination => open({kind: 'home-see-all', destination}), customize: () => open({kind: 'home-customize'}),
      openCollection: collection => open({kind: 'collection', libraryId: collection.collection.libraryId, collectionId: collection.collection.id, title: collection.collection.name}),
    },
    selection: rowId => ({active: controller.getSnapshot().selection.active,
      isMenuOpen: id => {const capture = currentMenuCapture(), window = windows.get(rowId);
        return !!capture && !!window && window.keys.get(id) === capture.menu.key && window.memberWindows.get(id) === capture.menu.windowId;},
      isSelected: id => {const key = windows.get(rowId)?.keys.get(id); return !!key && controller.selection.isSelected(key);},
      onToggle: id => {const key = windows.get(rowId)?.keys.get(id); if (!queryAction && key && currentHome()) controller.toggle(windows.get(rowId)!.memberWindows.get(id)!, key);},
      onEnter: id => {
        const key = windows.get(rowId)?.keys.get(id); if (queryAction || !key || !currentHome()) return;
        const before = controller.getSnapshot().selection.active, changed = controller.toggle(windows.get(rowId)!.memberWindows.get(id)!, key);
        if (changed.ok && !before && controller.getSnapshot().selection.active) void selectionHaptic().catch(reportError);
      }}),
    setHomeWindow: (row, order, first, last, visible, physicalIds, retainedIds, visibleIds) => {
      const before = windows.get(row.id);
      if ((!visible && !retainedIds?.length) || !currentHome()) {
        if (!before) return;
        windows.delete(row.id); release(before.cleanup);
        try {reconcileWindows();} catch (error) {reportError(error, 'home-registration');}
        return;
      }
      const latest = context.home.getSnapshot().document?.rows.find(value => value.id === row.id);
      if (!latest) return;
      if (before?.row === latest && before.order === order && before.first === first && before.last === last && before.visible === visible && same(before.physicalIds, physicalIds) && same(before.retainedIds, retainedIds) && same(before.visibleIds, visibleIds)) return;
      const window = before ?? {row: latest, order, first, last, visible, keys: new Map(), memberWindows: new Map()};
      Object.assign(window, {row: latest, order, first, last, visible, physicalIds, retainedIds, visibleIds}); windows.set(row.id, window);
      try {reconcileWindows();} catch (error) {reportError(error, 'home-registration');}
    },
    homeMenuRow: id => [...windows.values()].find(window => [...window.memberWindows.values()].includes(id))?.row,
    renderSelection: () => <><HomeSelectionBars runtime={runtime} controller={controller} queryActions={queryActions} ownerTag={ownerTag} reportError={reportError}/><HomeSelectionOverlays queryActions={queryActions} anchorRef={currentMenuCapture()?.anchorRef} controller={controller} runtime={runtime} ownerTag={ownerTag} reportError={reportError}/><NativeScreenFeedbackHost service={feedback} bottomOffset={84}/></>,
    renderRoutes: () => {
      const route = context.navigation.getSnapshot().route;
      // Instantiate the current module before asking it for retained native views.
      // A native stack draws each page in its own screen; here it only needs every module to exist.
      if (stacks) for (const presentation of context.navigation.presentationStack()) routes.prepare(presentation.route);
      const active = route.kind === 'shell' || stacks ? null : render(route);
      return runtime && context.current() ? <View pointerEvents="box-none" style={{position: 'absolute', top: 0, right: 0, bottom: 0, left: 0}}>
        {Platform.isTV && <NativeTVReceiver context={context} runtime={runtime}/>}
        {(routes.renderRetained(runtime) ?? []).map((node, index) => <React.Fragment key={index}>{node}</React.Fragment>)}
        {active}
        {musicNavigation.render()}
        {(routes.renderOverlays(runtime) ?? []).map((node, index) => <React.Fragment key={index}>{node}</React.Fragment>)}
      </View> : null;
    },
    renderDestination: destination => render({kind: 'shell', destination}),
    renderWatch: () => runtime ? routes.renderWatch(runtime) : null,
    renderHomeEmpty: () => <NativeHomeEmpty owner={context.account.account.role === 'owner'} serverName={context.serverName}
      hasLibraries={context.libraries().length > 0} onSwitchServer={nativeMenuRouteAvailable({kind: 'servers'}) ? () => open({kind: 'servers'}) : undefined}/>,
    renderDownloads: () => <NativeDownloadSheet context={context}/>,
    openServers: nativeMenuRouteAvailable({kind: 'servers'}) ? initialPanel => open({kind: 'servers', ...(initialPanel ? {initialPanel} : {})}) : undefined,
    connectionFacts: {serverName: context.serverName, connectionAddress: nativeServerHost(context.api.baseUrl)},
    libraryMenu: library => {
      homeActionDiagnostics.libraryMenu('producer', runtime);
      const common = {read: isReadingLibraryKind(library.kind),
        onEditPins: () => open({kind: 'library-edit-pins', libraryId: library.id})};
      return management ? {...common, owner: true as const, ownerTag, onLibrarySettings: () => open({kind: 'library-settings', libraryId: library.id}), onScan: () => run(async () => {
        if (!context.current()) return; await management.selectLibrary(library.id);
        if (!context.current()) return;
        const read = libraryScanReadiness(management.getSnapshot().selected, library.id);
        if (!read.ok) throw failure(read.code);
        await management.scan(); const result = management.getSnapshot().mutation;
        if (result.error) throw failure(result.error.code);
      })} : {...common, owner: false as const};
    },
    error: (error, retry, context = 'generic') => feedback.error(error, retry, context),
    reportError, notice: feedback.notice, onDestination: destination => open({kind: 'shell', destination}),
    scrollToTop,
    stacks,
    registerScrollToTop: (destination, handler) => {const key = JSON.stringify(destination); scroll.set(key, handler);
      if (homeTopPending && destination.kind === 'home' && currentHome()) {homeTopPending = false; handler();}
      return () => {if (scroll.get(key) === handler) scroll.delete(key);};},
    registerTVRailBack: handler => {tvRailBack = handler; return () => {if (tvRailBack === handler) tvRailBack = undefined;};},
    registerTVPageBack: (handler, destination = {kind: 'home'}) => {
      const key = JSON.stringify(destination), registration = {handler};
      const stack = tvPageBack.get(key) ?? []; stack.push(registration); tvPageBack.set(key, stack);
      return () => {const index = stack.indexOf(registration); if (index < 0) return; stack.splice(index, 1);
        if (!stack.length && tvPageBack.get(key) === stack) tvPageBack.delete(key);};
    },
    mixes,
    musicDiscover: musicNavigation.open,
    onArtworkError: error => reportError(error, 'artwork-error'),
    dispose: () => {if (!alive) return; closeQueryAction(); queryListeners.clear(); alive = false; pendingChanges?.dispose(); capturedMenu = undefined; listeningPlaylist?.dispose(); listeningQueuePolicy?.dispose(); listeningSongDetail?.dispose(); musicNavigation.dispose(); mixesLife.abort(); mixes.dispose(); routes.dispose(); for (const operation of operations) operation.abort(); operations.clear(); for (const cleanup of cleanups) cleanup(); for (const member of members.values()) member.cleanup(); members.clear(); for (const window of windows.values()) window.cleanup?.(); for (const spotlight of spotlights.values()) spotlight.cleanup(); windows.clear(); spotlights.clear(); controller.dispose(); management?.dispose(); scroll.clear(); tvPageBack.clear(); runtime = undefined; listeners.clear();},
  };
}
