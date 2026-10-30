import type {ReactNode} from 'react';
import {subscribeFacts} from './fact-source';
import {createBrowserQueryActions} from '../screens/browse/selection-action-host';
import {defaultI18n} from '@i18n';
import {HomeSelectionController, homeSelectionContext, type HomeSelectionPlaybackPort} from '@core/home-selection/index.ts';
import {homeChannelSelection, homeCollectionSelection, homeEntryCapabilities, homeEntrySelection, type HomeActionPlatform} from '@core/home-selection.ts';
import {entryActions} from '@core/presentation/entry-actions.ts';
import type {HomeDocument, HomeRow, HomeSpotlight} from '@core/home.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {SelectionContext, SelectionEnvironment, SelectionIdentity} from '@core/selection-model/index.ts';
import {SelectionCircle} from '../kit/selection';
import type {CardSelection} from '../kit/cards';
import {router} from '../router';
import {navigateArtworkEntry} from './artwork-navigation';
import type {HomeSelection} from '../screens/home/Home';
import {currentHomeLive} from '../screens/home/model';
import type {ShellAssemblyContext} from './bootstrap';
import {entryDestination, seeAllDestination} from './navigation';
import {SelectionSurfaces, SelectionPhoneBar, type SelectionRendererSource} from './SelectionSurfaces';
import {selectionEditorAudience} from './selection-presentation';
import {shellSelectionScope} from './selection-scope';
import {createCardInteractionOwner} from '../kit/cards/interaction-admission';

export function createShellSelection(context: ShellAssemblyContext, playback: HomeSelectionPlaybackPort, reportError: (error: unknown) => void, dockVisible: () => boolean) {
  const interactions = createCardInteractionOwner(), interactionRows = new WeakMap<HomeRow, Map<string, object>>();
  const listeners = new Set<() => void>();
  const platform: HomeActionPlatform = {platform: 'web', playbackAvailable: true, queueAvailable: true};
  const originalSession = context.session(), viewer = originalSession.viewer;
  const artworkScope = JSON.stringify([viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId, originalSession.sessionFamilyId, context.epoch]);
  const scope = () => shellSelectionScope(viewer, router.state.location.href);
  const document = (): HomeDocument => { const value = context.home.getSnapshot().document; if (!value) throw new Error('home-document-required'); return value; };
  const currentContext = () => homeSelectionContext(scope(), document());
  const environment = (): SelectionEnvironment => ({platform: window.innerWidth >= 1024 ? 'desktop-web' : 'mobile-web', isOwner: viewer.role === 'owner',
    canEditMetadata: document().rows.some(row => row.entries.some(entry => homeEntryCapabilities(entry, platform).editMetadata === true))});
  let retired = false;
  const isCurrent = (value: SelectionContext) => {
    const current = context.home.getSnapshot().document;
    return !retired && context.isCurrent() && !!current && router.state.location.pathname === '/' && JSON.stringify(value) === JSON.stringify(homeSelectionContext(scope(), current));
  };
  const controller = new HomeSelectionController({api: context.api, events: context.events, home: context.home,
    context: currentContext(), environment: environment(), isCurrent, requestId: async () => crypto.randomUUID(), playback,
    routes: {openEntry: entry => {const destination = entryDestination(entry);return navigateArtworkEntry(entry, destination, artworkScope, () => isCurrent(controller.getSnapshot().context), () => !retired && context.isCurrent(), manual => router.navigate({...destination, ...(manual ? {viewTransition: false} : {})}));}, openCollection: collection => router.navigate(seeAllDestination({type: 'collection', resourceId: collection.collection.id, libraryId: collection.collection.libraryId})),
      openDestination: destination => router.navigate({to: '/saved', search: {resourceId: destination.id}})}, reportError});
  const queryLife = new AbortController();
  const queryActions = createBrowserQueryActions({subscribe: controller.selection.subscribe, captureOperation: () => controller.selection.captureOperation(),
    isOperationCurrent: operation => controller.selection.isOperationCurrent(operation)}, {api: context.api, requestId: async () => crypto.randomUUID(),
    deviceId: originalSession.deviceId ?? '', sourceIdentity: () => context.home.getSnapshot().document,
    owner: viewer.role === 'owner' && viewer.authority === 'local', signal: queryLife.signal,
    scopeKey: () => {const session = context.session(), actor = session.viewer; return JSON.stringify([router.state.location.href,
      session.sessionFamilyId, session.deviceId, actor.serverId, actor.authority, actor.accountId, actor.profileId, actor.role, context.viewerFence]);},
    isCurrent: () => !retired && context.isCurrent() && router.state.location.pathname === '/',
    subscribe: listener => {const a = context.home.subscribe(listener), b = subscribeFacts(context, listener), c = router.subscribe('onResolved', listener); return () => {a();b();c();};}});
  const anchor: {current: HTMLElement | null} = {current: null};
  const source: SelectionRendererSource = {controller, queryActions,
    metadata: {api: {request: context.api.request.bind(context.api), requestMetadataUpload: context.api.requestMetadataUpload.bind(context.api)}, scope: {serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])},
      current: snapshot => viewer.role === 'owner' && isCurrent(snapshot.context) && snapshot.editor?.preview?.viewerFence === context.viewerFence}, anchor, artwork: context.artwork, reportError, owner: viewer.role === 'owner', editorAudience: selectionEditorAudience(viewer.role), dockVisible};
  let acceptedContext = JSON.stringify(currentContext()), acceptedEnvironment = JSON.stringify(environment());
  const sync = () => {
    if (retired || !context.isCurrent()) {interactions.retireRows();return;}
    const nextDocument = context.home.getSnapshot().document;
    if (!nextDocument) { interactions.retireRows();controller.clear(); return; }
    const next = homeSelectionContext(scope(), nextDocument), nextEnvironment = environment();
    if (JSON.stringify(next) !== acceptedContext || JSON.stringify(nextEnvironment) !== acceptedEnvironment) {
      acceptedContext = JSON.stringify(next); acceptedEnvironment = JSON.stringify(nextEnvironment); controller.setContext(next, nextEnvironment);
    }
    interactions.prune();
  };
  let preview: {row: HomeRow; windowId: string; target: string; keys: readonly string[]} | undefined;
  const emit = () => {if (!retired) for (const listener of listeners) listener();};
  const clearPreview = () => {interactions.resetPreview();if (!preview) return;preview = undefined;emit();};
  const releasePreview = (event: KeyboardEvent) => {if (event.key === 'Shift') clearPreview();};
  window.addEventListener('keyup', releasePreview);window.addEventListener('blur', clearPreview);
  const off = [controller.subscribe(() => {interactions.resetPreview();preview = undefined;emit();}), context.home.subscribe(sync), router.subscribe('onResolved', sync)];
  window.addEventListener('resize', sync);
  const perform = (work: () => void) => { try { work(); } catch (error) { reportError(error); } };
  const windowId = (row: HomeRow) => 'home:' + row.id;
  const spotlightId = (spotlight: HomeSpotlight) => 'home:spotlight:' + document().spotlight.indexOf(spotlight);
  const selected = (key: string) => controller.getSnapshot().selection.selectedKeys.includes(key);
  const currentRow = (row: HomeRow) => {
    const shown = context.home.getSnapshot().document;
    return isCurrent(controller.getSnapshot().context) && !!shown && row.viewerFence === shown.viewerFence && shown.rows.some(value => value === row || value.children?.includes(row));
  };
  const liveCurrent = (row: HomeRow) => !row.live || !!currentHomeLive(row.live, Date.now());
  const clearExpiredLive = (row: HomeRow) => {
    if (!retired && row.live && !liveCurrent(row) && row.live.channels.some(item => selected(controller.key(homeChannelSelection(item, platform))))) controller.clear();
  };
  function descriptor(row: HomeRow, item: SelectionIdentity | undefined, title: string): CardSelection | undefined {
    if (!item || !currentRow(row) || !liveCurrent(row)) return undefined;
    const key = controller.key(item), snapshot = controller.getSnapshot().selection;
    let rows = interactionRows.get(row);
    if (!rows) {rows = new Map();interactionRows.set(row, rows);}
    let original = rows.get(key);
    if (!original) {original = Object.freeze({row, key});rows.set(key, original);}
    const setPreview = (enabled: boolean) => {
      const clearOwn = () => {if (preview?.row === row && preview.target === key) clearPreview();};
      if (!enabled || !controller.getSnapshot().selection.active || !currentRow(row) || !liveCurrent(row)) {clearOwn();return;}
      if (preview?.row === row && preview.target === key) return;
      const value = range();if (!value?.ok) {clearOwn();return;}
      preview = {row, windowId: windowId(row), target: key, keys: value.keys};emit();
    };
    const interaction = interactions.source(original, {current: () => currentRow(row) && liveCurrent(row), preview: setPreview});
    const toggle = (range: boolean) => perform(() => {
      if (!currentRow(row) || !liveCurrent(row)) { clearExpiredLive(row); return; }
      const change = controller.toggle(windowId(row), key, range);
      if (!change.ok) throw Object.assign(new Error(change.reason), {code: change.reason});
    });
    const range = () => {try {return currentRow(row) && liveCurrent(row) ? controller.previewRange(windowId(row), key) : undefined;} catch {return undefined;}};
    const hintRange = snapshot.active && !selected(key) ? range() : undefined;
    const named = hintRange?.ok ? hintRange.keys.map(value => {
      const entry = row.entries.find(entry => {const identity = homeEntrySelection(entry, platform);return identity && controller.key(identity) === value;});
      return entry?.title;
    }) : [];
    const hint = hintRange?.ok && named.length && named.every((value): value is string => typeof value === 'string')
      ? {text: defaultI18n.t('next.selection.rangeTitles', {first: named[0]!, last: named.at(-1)!, count: hintRange.keys.length}), shortcut: defaultI18n.t('next.selection.shift')} : undefined;
    return {active: snapshot.active, checked: selected(key), interaction, ...(hint ? {hint} : {}),
      preview: snapshot.active && preview?.windowId === windowId(row) && preview.keys.includes(key),
      onPreview: setPreview,
      onToggle: intent => toggle(intent.range), onExit: () => controller.clear(),
      control: <SelectionCircle selected={selected(key)} selecting={snapshot.active} accessibilityLabel={defaultI18n.t('next.home.select', {title})}
        interactive={false} />};
  }
  const selection: HomeSelection = {
    toolbar: <SelectionSurfaces source={source} />,
    card: (row, entry) => descriptor(row, homeEntrySelection(entry, platform), entry.title),
    channel: (row, item) => row.live?.channels.includes(item) ? descriptor(row, homeChannelSelection(item, platform), item.channel.name) : undefined,
    collection: row => row.collection ? descriptor(row, homeCollectionSelection(row.collection, platform), row.collection.collection.name) : undefined,
    registerWindow: (row, sectionOrder, first, last) => {
      if (!isCurrent(controller.getSnapshot().context) || first < 0 || last < first) return undefined;
      if (!currentRow(row) || !liveCurrent(row)) { clearExpiredLive(row); return undefined; }
      // The server's returned row is capped at 100 and is already mounted by Shelf.
      // Keeping this loaded page allows Shift ranges after horizontal scrolling, without reading a query.
      let registered: ReturnType<HomeSelectionController['registerWindow']>;
      try { registered = controller.registerWindow({id: windowId(row), row, sectionOrder, start: row.start}); }
      catch (error) {
        // The real clock can cross expiry between the caller and controller
        // checks. That specific refusal creates no window or action offer.
        if ((error as {code?: unknown})?.code !== 'stale-home-live-window' || liveCurrent(row)) throw error;
        clearExpiredLive(row); return undefined;
      }
      if (!registered.ok && registered.reason === 'stale-home-live-window') { clearExpiredLive(row); return undefined; }
      if (!registered.ok) { reportError(Object.assign(new Error(registered.reason), {code: registered.reason})); return undefined; }
      return () => { registered.unregister(); clearExpiredLive(row); };
    },
    registerSpotlight: spotlight => {
      if (!isCurrent(controller.getSnapshot().context)) return undefined;
      const registered = controller.registerSpotlight({id: spotlightId(spotlight), spotlight});
      if (!registered.ok) { reportError(Object.assign(new Error(registered.reason), {code: registered.reason})); return undefined; }
      return registered.unregister;
    },
    moreAvailable: entry => entryActions(homeEntryCapabilities(entry, platform)).some(group => group.actions.length > 0),
    moreExpanded: (row, entry) => { const item = homeEntrySelection(entry, platform); return !!item && controller.getSnapshot().menu?.key === controller.key(item) && controller.getSnapshot().menu?.windowId === windowId(row); },
  };
  return {selection, controller,
    setSpotlightMyList: async (spotlight: HomeSpotlight) => {
      const item = homeEntrySelection(spotlight.entry, platform); if (!item) throw new Error('spotlight-identity-required');
      controller.openMenu(spotlightId(spotlight), controller.key(item)); await controller.executeMenu('watchlist');
    },
    openMore: (row: HomeRow, entry: ContentEntry, element: HTMLElement) => perform(() => {
      const item = homeEntrySelection(entry, platform); if (!item) throw new Error('home-library-identity-required');
      anchor.current = element; controller.openMenu(windowId(row), controller.key(item));
    }),
    phoneSelection: (): ReactNode => controller.getSnapshot().selection.active ? <SelectionPhoneBar source={source} /> : undefined,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    dispose: () => { retired = true;queryLife.abort();queryActions.dispose(); interactions.dispose();controller.dispose(); for (const cleanup of off) cleanup(); window.removeEventListener('resize', sync); window.removeEventListener('keyup', releasePreview);window.removeEventListener('blur', clearPreview);preview = undefined;anchor.current = null; listeners.clear(); },
  };
}
