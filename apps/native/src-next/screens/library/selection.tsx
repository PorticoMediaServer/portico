import {nativeSelectionActionUnavailable, nativeSelectionUnavailableActions} from './selection-unavailable-actions';
import {nativeWholeQueryUnavailableActions} from './whole-query-unavailable';
import {createNativeLibrarySelectionAction, nativeSelectionActionInput, type NativeLibrarySelectionAction} from './selection-actions';
import {askNativeDownload, closeNativeDownload, getNativeDownloadSnapshot} from '../downloads/request';
import {offlineDownloads} from '../../platform/offline-downloads';
import React, {useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {BrowseWholeSelectionUnavailable} from '../../../../../packages/client-core/src/browse';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {catalogueQuerySelectionSegments, enqueueCatalogueQuerySelection} from '../../../../../packages/client-core/src/catalogue-query-selection';
import type {BulkMetadataPreview} from '../../../../../packages/client-core/src/home-selection/metadata-preview';
import {createCataloguePersonalSelection, enqueueCatalogueSelection, selectionFourthAction,
  type BrowseSelectionContext, type QuerySelectionTarget, type SelectionSnapshot} from '../../../../../packages/client-core/src/selection-model';
import {mediaSelectionKey} from '../../../../../packages/client-core/src/media-selection';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {selectedItemsLabel} from '../../shell/selection-display';
import {BulkEditor, DeleteMediaDialog, SelectionBar, SelectionPhoneActionBar, type SelectionActionOffer, type SelectionBarProps} from '../../kit/selection';
import type {LayoutSelection} from '../../kit/layout';
import {useTheme} from '../../kit/theme';
import {Dialog, SidePanel} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeScreenServices} from '../native-screen-services';
import {NativeDestinationPicker} from '../destinations/DestinationPicker';
import {NativeLoadedDestinations} from '../search/loaded-destinations';
import {NativeBulkMedia} from '../destinations/bulk-media';
import {nativeLibraryEntryKey, type NativeLibraryDestination} from './model';
import type {NativeLibrarySelectionPort} from './LibraryScreen';
import {createNativeLibraryPlaySelection} from './play-selection';

type Producer = ReturnType<typeof createNativeLibraryPlaySelection>;
const queryLeaves: ReadonlySet<string> = new Set(['movie', 'episode', 'song', 'audiobook_file']);
const leaf = (entry: ContentEntry) => queryLeaves.has(entry.kind);

/** The canonical segment builder is a pure validator with no transport: if it
 * refuses the target, the query executor refuses it too. Asking it keeps the offer
 * and the dispatch aligned with the one canonical rule, including its filter parse. */
function queryExecutable(target: QuerySelectionTarget<BrowseSelectionContext>, capability: 'play' | 'queue'): boolean {
  try {catalogueQuerySelectionSegments(target, capability); return true;} catch {return false;}
}
type RegisteredWindow = Readonly<{id: string; first: number; entries: readonly ContentEntry[]; keys: ReadonlyMap<ContentEntry, string>; toggle: (entry: ContentEntry) => void; dispose: () => void}>;

/** Real selection over the library's own applied Browse view.
 *
 * Toggling works on the genuine entries the grid registers, and every action the
 * bar offers is one the canonical Detail projection enables for *every* pick:
 * Play, Add to queue, My List, Favorites and Watched, the bounded playlist and
 * collection picker, and the owner's bulk edit and delete. Download has no
 * executor on this surface, so that capability stays false.
 *
 * Whole-view Select all needs a server descriptor. An explicit refusal stays
 * visible with its reason; the loaded page remains a separate selection. */
export function createNativeLibrarySelection(context: NativeShellBootstrapContext, source: NativeLibraryDestination, activation: NativeDestinationActivationPort) {
  const lifetime = nativeDestinationLifetime(context), services = nativeScreenServices(context);
  const listeners = new Set<() => void>(), windows = new Map<string, RegisteredWindow>();
  const pendingWindows = new Map<string, {first: number; entries: readonly ContentEntry[]; gone: boolean; reading: boolean; window?: RegisteredWindow}>();
  let disposed = false, revision = 0, entering = false, token = '';
  let producer: Producer | undefined, stopModel: (() => void) | undefined;
  let pending: AbortController | undefined, personalController: AbortController | undefined, queued: string | undefined;
  let destinations: NativeLoadedDestinations | undefined, destinationKind: 'playlist' | 'collection' | undefined;
  let destinationController: AbortController | undefined, stopDestinations: (() => void) | undefined;
  let bulk: NativeBulkMedia | undefined, bulkController: AbortController | undefined, stopBulk: (() => void) | undefined;
  let queryAction: NativeLibrarySelectionAction | undefined, stopQueryAction: (() => void) | undefined;
  let personal: ReturnType<typeof createCataloguePersonalSelection> | undefined, personalCurrent: (() => boolean) | undefined, personalToken = 0;
  const emit = () => {revision++; for (const listener of [...listeners]) listener();};
  const visible = () => !disposed && lifetime.current() && activation.getSnapshot().visible;
  const model = () => producer?.model;
  const snapshot = (): SelectionSnapshot<BrowseSelectionContext> | undefined => model()?.getSnapshot();
  const sourceToken = () => {
    const now = source.getSnapshot();
    return now.phase === 'ready' && now.collection ? JSON.stringify([now.library.id, now.tab.id, now.revision, now.queryRevision]) : '';
  };
  const closeDestinations = (expected?: NativeLoadedDestinations) => {
    // A delayed close from A must never close B; reset may force-close.
    if (expected && destinations !== expected) return;
    destinationController?.abort(); destinationController = undefined; stopDestinations?.(); stopDestinations = undefined;
    destinations?.dispose(); destinations = undefined; destinationKind = undefined;};
  const closeBulk = (expected?: NativeBulkMedia) => {
    // The same rule as the picker: only the dialog that still owns the surface closes.
    if (expected && bulk !== expected) return;
    bulkController?.abort(); bulkController = undefined; stopBulk?.(); stopBulk = undefined;
    bulk?.dispose(); bulk = undefined;};
  const closeQueryAction = (expected?: NativeLibrarySelectionAction) => {
    if (expected && queryAction !== expected) return;
    const closing = queryAction; queryAction = undefined;
    stopQueryAction?.(); stopQueryAction = undefined;
    if (closing) {
      const request = getNativeDownloadSnapshot();
      if (request?.queryAction === closing) closeNativeDownload(request);
      closing.dispose(); emit();
    }
  };
  const reset = (preserveQueryAction = false) => {
    if (!preserveQueryAction) closeQueryAction();
    closeDestinations();
    closeBulk();
    pending?.abort(); pending = undefined; queued = undefined;
    personalController?.abort(); personalController = undefined; personalCurrent = undefined; personalToken++;
    if (personal) {personal.dispose(); personal = undefined;}
    stopModel?.(); stopModel = undefined;
    producer?.dispose(); producer = undefined;
    // Every read still in flight is told so before anything is cleared, so its
    // completion can never install a window into the next query.
    for (const entry of pendingWindows.values()) entry.gone = true;
    pendingWindows.clear();
    for (const window of windows.values()) window.dispose();
    windows.clear(); token = ''; entering = false;
  };
  // The producer disposes itself when the applied view changes; rebuild it there
  // so every registration belongs to exactly one real query.
  const ensure = (): Producer | undefined => {
    if (queryAction?.current()) return producer;
    const next = sourceToken();
    if (!next || !visible()) {if (token) reset(); return undefined;}
    if (producer && next === token) return producer;
    reset();
    try {
      producer = createNativeLibraryPlaySelection(context, source);
      stopModel = producer.model.subscribe(() => {if (producer?.model.getSnapshot().active) entering = false; emit();});
      token = next; emit();
      return producer;
    } catch {return undefined;}
  };
  // The grid addresses entries by its own stable key; the selection model has its
  // own key per entry. Every lookup here translates between the two.
  const keyOf = (entry: ContentEntry) => [...windows.values()].find(window => window.keys.has(entry))?.keys.get(entry);
  const windowForEntry = (entry: ContentEntry) => [...windows.values()].find(window => window.keys.has(entry));
  const entryForKey = (key: string) => {
    for (const window of windows.values()) for (const entry of window.keys.keys()) if (nativeLibraryEntryKey(entry) === key) return entry;
    for (const pending of pendingWindows.values()) {
      const entry = pending.entries.find(value => nativeLibraryEntryKey(value) === key);
      if (entry) return entry;
    }
    return undefined;
  };
  // A window is only read when selection actually starts: registering every
  // visible range would otherwise fetch a Detail page per card on plain scrolling.
  const prepare = (id: string) => {
    const current = ensure(), entry = pendingWindows.get(id);
    if (!current || !entry || entry.gone || pendingWindows.get(id) !== entry || windows.has(id) || entry.reading) return;
    const readToken = token;
    entry.reading = true;
    void current.prepareWindow(id, entry.first, entry.entries).then(window => {
      // The completion belongs only to this producer, token, pending record and
      // query; a reset, Back or newer query discards it instead of installing it.
      const stillCurrent = !entry.gone && visible() && producer === current && token === readToken
        && pendingWindows.get(id) === entry && !!sourceToken() && sourceToken() === token;
      if (!stillCurrent) {window.dispose(); return;}
      const next: RegisteredWindow = {id, first: entry.first, entries: entry.entries, keys: window.keys, toggle: window.toggle, dispose: window.dispose};
      entry.window = next; windows.set(id, next);
      if (current.model.getSnapshot().active) entering = false;
      emit();
      // The pick that asked for this window is applied once, now that the model
      // holds its real registered key.
      if (queued) {const key = queued; queued = undefined; toggle(key);}
    }).catch(() => {}).finally(() => {entry.reading = false;});
  };
  const prepareAll = () => {for (const id of pendingWindows.keys()) prepare(id);};
  /** Exactly these keys, in the model's own order. Overlapping windows can hold
   * the same key twice; the first captured entry wins and nothing is repeated. */
  const entriesFor = (keys: readonly string[]) => {
    const byKey = new Map<string, ContentEntry>();
    for (const window of windows.values()) for (const [entry, key] of window.keys) if (!byKey.has(key)) byKey.set(key, entry);
    const picked: ContentEntry[] = [];
    for (const key of keys) {const entry = byKey.get(key); if (entry) picked.push(entry);}
    return picked;
  };
  const selectedKeys = () => snapshot()?.selectedKeys ?? [];
  const toggle = (key: string) => {
    if (queryAction) return;
    const entry = entryForKey(key);
    if (!entry) return;
    const window = windowForEntry(entry);
    if (!window) {
      // The first pick reads its own visible window, then applies the pick to the
      // real model; nothing is marked selected before the model accepts it.
      const holder = [...pendingWindows.entries()].find(([, pending]) => pending.entries.includes(entry));
      entering = true; queued = key; emit();
      if (holder) prepare(holder[0]);
      return;
    }
    try {window.toggle(entry); entering = true; emit();} catch (error) {if (visible()) services.reportError(error, 'library');}
  };
  const clear = () => {closeQueryAction(); entering = false; model()?.clear(); emit();};
  const loaded = (): ContentEntry[] => [...windows.values()].flatMap(window => [...window.entries]);
  const queueControl = () => {
    const role = context.playback.getSnapshot().session?.role;
    return role === undefined || role === 'local';
  };
  const execute = async (action: 'play' | 'playNext' | 'playLater', view: SelectionSnapshot<BrowseSelectionContext>) => {
    // A retained menu acts on the selection it was drawn for, never on a successor.
    if (view !== snapshot()) return;
    const current = producer, op = current?.model.captureOperation();
    if (!current || !op || !visible() || pending || destinations || bulk || queryAction || personal?.service.getSnapshot().busy
      || !!personal?.service.getSnapshot().results.length) return;
    const query = op.target.mode === 'query' ? op.target : undefined;
    if (nativeSelectionActionUnavailable(view.summary, action) || query && !queryExecutable(query, action === 'play' ? 'play' : 'queue')) return;
    const controller = new AbortController(), live = () => !controller.signal.aborted && visible() && producer === current && current.model.isOperationCurrent(op);
    pending = controller; emit();
    try {
      if (action === 'play') await current.play();
      else {
        const port = context.playback.cataloguePlayback?.();
        if (!port || !queueControl()) throw Object.assign(new Error('native_library_queue_unavailable'), {code: 'unavailable'});
        await (op.target.mode === 'query'
          ? enqueueCatalogueQuerySelection(port, op.target, action === 'playNext' ? 'next' : 'end', {signal: controller.signal, isCurrent: live})
          : enqueueCatalogueSelection(port, op.target.items.map(value => value.item), action === 'playNext' ? 'next' : 'end', {signal: controller.signal, isCurrent: live}));
      }
      if (live()) clear();
    } catch (error) {if (live()) services.reportError(error, 'library');}
    finally {if (pending === controller) {pending = undefined; emit();}}
  };
  const applyPersonal = async (action: 'myList' | 'removeMyList' | 'favorite' | 'watched' | 'unwatched', view: SelectionSnapshot<BrowseSelectionContext>) => {
    // The same identity rule as Play: the rendered snapshot must still be the
    // live one before any operation or transport is captured.
    if (view !== snapshot()) return;
    const current = producer, op = current?.model.captureOperation();
    if (!current || !op || op.target.mode !== 'explicit' || !visible() || pending || destinations || bulk
      || !!personal?.service.getSnapshot().results.length || personal?.service.getSnapshot().busy) return;
    const capability = action === 'myList' || action === 'removeMyList' ? 'myList' : action === 'favorite' ? 'favorite' : 'watched';
    if (!view.summary.every[capability] || !op.target.items.every(value => value.item.capabilities[capability])) return;
    const entries = entriesFor(op.target.items.map(value => value.key));
    if (entries.length !== op.target.items.length) return;
    const controller = new AbortController(), live = () => !controller.signal.aborted && visible() && producer === current && current.model.isOperationCurrent(op);
    personalController = controller; personalCurrent = live;
    personalToken++;
    personal = createCataloguePersonalSelection({api: context.api, scope: lifetime.scope, requestId: services.requestId, signal: controller.signal, isCurrent: live});
    personal.service.setEntries(entries); personal.service.begin();
    for (const entry of entries) personal.service.toggle(mediaSelectionKey(entry));
    emit();
    try {await personal.service.apply(capability === 'myList' ? 'watchlist' : capability, action !== 'removeMyList' && action !== 'unwatched');}
    catch (error) {if (live()) services.reportError(error, 'library');}
    finally {if (personalController === controller) personalController = undefined; emit();}
  };
  // The drawn picker (#25/#26) is the same bounded destination controller Saved
  // and Search mount: it owns eligibility, the chosen destination, the completed
  // prefix, its retry and each receipt.
  const openDestination = (kind: 'playlist' | 'collection', view: SelectionSnapshot<BrowseSelectionContext>) => {
    // The rendered snapshot must still be the live one before anything is captured.
    if (view !== snapshot() || kind === 'playlist' && nativeSelectionActionUnavailable(view.summary, 'playlist')) return;
    const current = producer, model = current?.model;
    // A personal operation still in flight, its result set, or a picker already
    // open owns the surface: a retained callback never opens behind it.
    if (!current || !model || !view.active || !view.selectedKeys.length || destinations || pending || personalController
      || !!personal?.service.getSnapshot().results.length || personal?.service.getSnapshot().busy || !visible()) return;
    const op = model.captureOperation();
    if (!op || op.target.mode !== 'explicit') return;
    const pickedEntries = entriesFor(view.selectedKeys);
    if (pickedEntries.length !== view.selectedKeys.length) return;
    const routeRevision = context.navigation.getSnapshot().revision;
    const controller = new AbortController(), live = () => !controller.signal.aborted && visible() && producer === current
      && context.navigation.getSnapshot().revision === routeRevision && model.isOperationCurrent(op);
    destinationController = controller;
    destinations = new NativeLoadedDestinations({api: context.api, scope: lifetime.scope, requestId: services.requestId, signal: controller.signal, isCurrent: live,
      viewerFence: context.authority().viewerFence}, pickedEntries);
    destinationKind = kind;
    stopDestinations = destinations.subscribe(emit);
    void destinations.load(kind).catch(error => {if (live()) services.reportError(error, 'library');});
    emit();
  };
  // select 21/22: the owner's real bulk editor and delete dialog, over exactly
  // the selected leaves and the captured view. No grant, no row.
  const openAdmin = (kind: 'edit' | 'delete', view: SelectionSnapshot<BrowseSelectionContext>) => {
    if (view !== snapshot() || kind === 'delete' && nativeSelectionActionUnavailable(view.summary, 'delete')) return;
    const current = producer, model = current?.model;
    if (!current || !model || !view.active || !view.selectedKeys.length || destinations || bulk || queryAction || pending || personalController
      || !!personal?.service.getSnapshot().results.length || personal?.service.getSnapshot().busy || !visible()) return;
    const op = model.captureOperation();
    if (!op || op.target.mode !== 'explicit') return;
    const pickedEntries = entriesFor(view.selectedKeys);
    if (pickedEntries.length !== view.selectedKeys.length) return;
    const routeRevision = context.navigation.getSnapshot().revision;
    const controller = new AbortController(), live = () => !controller.signal.aborted && visible() && producer === current
      && context.navigation.getSnapshot().revision === routeRevision && model.isOperationCurrent(op);
    bulkController = controller;
    bulk = new NativeBulkMedia({api: context.api, scope: lifetime.scope, requestId: services.requestId, signal: controller.signal, isCurrent: live}, pickedEntries);
    stopBulk = bulk.subscribe(emit);
    void (kind === 'edit' ? bulk.openEditor() : bulk.openDelete()).catch(error => {if (live()) services.reportError(error, 'detail');});
    emit();
  };
  const queryOffer = (kind: 'delete' | 'download', view: SelectionSnapshot<BrowseSelectionContext> | undefined) => {
    if (!view || view !== snapshot() || view.target?.mode !== 'query' || !visible()) return false;
    try {nativeSelectionActionInput(view.target, kind, context); return true;} catch {return false;}
  };
  const openQueryAction = (kind: 'delete' | 'download', view: SelectionSnapshot<BrowseSelectionContext>) => {
    if (!queryOffer(kind, view) || !producer || queryAction || bulk || destinations || pending || personalController
      || personal?.service.getSnapshot().busy || personal?.service.getSnapshot().results.length) return;
    try {
      const action = createNativeLibrarySelectionAction(context, source, activation, producer.model, undefined, {
        follow: (operationId, deviceId) => offlineDownloads.followSelectionAction(operationId, deviceId),
      });
      if (snapshot() !== view || action.target !== view.target) {action.dispose(); return;}
      queryAction = action;
      stopQueryAction = action.subscribe(() => {if (!action.current()) closeQueryAction(action); else emit();});
      if (kind === 'delete') void action.openDelete().catch(error => {if (queryAction === action && action.current()) services.reportError(error, 'detail');});
      else askNativeDownload({title: selectedItemsLabel(view.summary), queryAction: action,
        lifetime: {signal: action.source.signal!, isCurrent: action.current}});
      emit();
    } catch (error) {services.reportError(error, 'detail');}
  };
  const retryPersonal = async () => {
    if (!personal || !personalCurrent?.() || personal.service.getSnapshot().busy) return;
    try {await personal.service.retry();} catch (error) {if (personalCurrent?.()) services.reportError(error, 'library');}
  };
  const store = {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => revision, snapshot, loadedCount: () => loaded().length, personal: () => personal?.service.getSnapshot(),
    personalBusy: () => !!personal?.service.getSnapshot().busy, resultsToken: () => personalToken,
    // An open bulk dialog owns the current operation: the bar must not offer another.
    busy: () => !!pending || !!personal?.service.getSnapshot().busy || !!destinations?.snapshot().busy || !!bulk || !!queryAction, active: () => entering || !!snapshot()?.active || !!queryAction, queueControl, clear, execute, applyPersonal, retryPersonal,
    destination: () => destinations, destinationKind: () => destinationKind, openDestination, closeDestinations,
    // Only an active captured view with a validated descriptor can offer Select all.
    /** Bar Done/Cancel/Back: only the view it was drawn for may force-clear, and a
     * current operation can still be cancelled. */
    cancel: (view: SelectionSnapshot<BrowseSelectionContext>) => {
      if (view !== snapshot() || !visible()) return;
      clear();
    },
    whole: () => snapshot()?.active && visible() ? producer?.whole() : undefined,
    wholeUnavailable: () => snapshot()?.active && visible() && source.getSnapshot().phase === 'ready' ? source.getSnapshot().wholeSelectionUnavailable : undefined,
    /** The bar's own view and descriptor id: a retained menu never selects a successor. */
    selectWhole: (view: SelectionSnapshot<BrowseSelectionContext>, id: string) => {
      if (!producer || view !== snapshot() || producer.whole()?.id !== id || !visible() || pending || destinations
        || personalController || !!personal?.service.getSnapshot().results.length || personal?.service.getSnapshot().busy
        || !!bulk || !!queryAction) return;
      if (producer.selectWhole(id)) emit();
    },
    queryAction: () => queryAction, queryOffer, openQueryAction, closeQueryAction,
    bulk: () => bulk, openAdmin, closeBulk, entries: () => entriesFor(selectedKeys()).filter(leaf)};
  // useSyncExternalStore compares snapshots by identity: one layout object per
  // revision, never a fresh object on every read.
  let layoutVersion = -1, layoutSnapshot: LayoutSelection | undefined;
  const layout = (): LayoutSelection => {
    if (layoutSnapshot && layoutVersion === revision) return layoutSnapshot;
    layoutVersion = revision;
    // Both callbacks belong to the producer and query token this snapshot was
    // drawn from; a successor view never receives a stale key press.
    const owner = producer, ownerToken = token, live = () => owner === producer && ownerToken === token && !!owner;
    layoutSnapshot = Object.freeze({active: entering || !!snapshot()?.active,
      isSelected: (key: string) => live() && !!entryForKey(key) && selectedKeys().includes(keyOf(entryForKey(key)!) ?? ''),
      onToggle: (key: string) => {if (live()) toggle(key);}, onEnter: (key: string) => {if (live()) toggle(key);}});
    return layoutSnapshot;
  };
  const port: NativeLibrarySelectionPort = {
    start: () => {ensure(); entering = true; emit(); prepareAll();},
    subscribe: store.subscribe,
    getSnapshot: layout,
    register: (_source, entries, first) => {
      const current = ensure();
      if (!current || !entries.length) return () => {};
      const id = `library-window-${first}-${entries.length}`;
      const entry = {first, entries, gone: false, reading: false} as {first: number; entries: readonly ContentEntry[]; gone: boolean; reading: boolean; window?: RegisteredWindow};
      pendingWindows.set(id, entry);
      if (entering || current.model.getSnapshot().active) prepare(id);
      return () => {
        entry.gone = true;
        // Only the exact record and window this registration created may be
        // removed; a later registration can reuse the same first/length id.
        if (pendingWindows.get(id) === entry) pendingWindows.delete(id);
        const held = windows.get(id);
        if (held && entry.window === held) {windows.delete(id); held.dispose(); emit();}
      };
    },
    item: entry => {
      const owner = producer, ownerToken = token, key = nativeLibraryEntryKey(entry), live = () => owner === producer && ownerToken === token && !!owner;
      return {active: entering || !!snapshot()?.active,
        selected: live() && !!keyOf(entry) && selectedKeys().includes(keyOf(entry)!),
        onToggle: () => {if (live()) toggle(key);}, onEnter: () => {if (live()) toggle(key);}};
    },
    render: (artwork?: (path?: string) => React.ReactNode) => <LibrarySelectionBars store={store} artwork={artwork ?? (() => null)}/>,
  };
  const off = [source.subscribe(() => {if (producer && sourceToken() !== token) {const state = queryAction?.getSnapshot(); reset(!!queryAction?.current() && (!!state?.job || state?.phase === 'submitting' || state?.phase === 'uncertain'));} emit();}), activation.subscribe(() => {if (!visible()) reset(); emit();}), context.subscribeAuthority(() => {reset(); emit();})];
  return Object.freeze({...port, dispose: () => {if (disposed) return; disposed = true; off.forEach(stop => stop()); reset(); listeners.clear();}});
}

type Store = {subscribe(listener: () => void): () => void; getSnapshot(): number; snapshot(): SelectionSnapshot<BrowseSelectionContext> | undefined;
  personal(): ReturnType<ReturnType<typeof createCataloguePersonalSelection>['service']['getSnapshot']> | undefined;
  personalBusy(): boolean; resultsToken(): number;
  applyPersonal(action: 'myList' | 'removeMyList' | 'favorite' | 'watched' | 'unwatched', view: SelectionSnapshot<BrowseSelectionContext>): Promise<void>; retryPersonal(): Promise<void>;
  loadedCount(): number; busy(): boolean; active(): boolean; queueControl(): boolean; clear(): void;
  execute(action: 'play' | 'playNext' | 'playLater', view: SelectionSnapshot<BrowseSelectionContext>): Promise<void>;
  cancel(view: SelectionSnapshot<BrowseSelectionContext>): void;
  whole(): Readonly<{id: string; total: number}> | undefined;
  wholeUnavailable(): BrowseWholeSelectionUnavailable | undefined;
  selectWhole(view: SelectionSnapshot<BrowseSelectionContext>, id: string): void;
  destination(): NativeLoadedDestinations | undefined; destinationKind(): 'playlist' | 'collection' | undefined;
  openDestination(kind: 'playlist' | 'collection', view: SelectionSnapshot<BrowseSelectionContext>): void; closeDestinations(expected?: NativeLoadedDestinations): void;
  bulk(): NativeBulkMedia | undefined; openAdmin(kind: 'edit' | 'delete', view: SelectionSnapshot<BrowseSelectionContext>): void; closeBulk(expected?: NativeBulkMedia): void;
  queryAction(): NativeLibrarySelectionAction | undefined; queryOffer(kind: 'delete' | 'download', view: SelectionSnapshot<BrowseSelectionContext> | undefined): boolean;
  openQueryAction(kind: 'delete' | 'download', view: SelectionSnapshot<BrowseSelectionContext>): void; closeQueryAction(expected?: NativeLibrarySelectionAction): void;
  entries(): readonly ContentEntry[]};

function LibrarySelectionBars({store, artwork}: {store: Store; artwork: (path?: string) => React.ReactNode}) {
  useSyncExternalStore(store.subscribe, store.getSnapshot);
  const theme = useTheme(), insets = useSafeAreaInsets(), [menu, setMenu] = useState<SelectionBarProps['menu']>(null);
  // Hooks stay above the inactive return so a bar that leaves the tree cannot change hook order.
  const doneRef = useRef<View>(null), retryRef = useRef<View>(null);
  const state = store.snapshot();
  if (!store.active()) return null;
  const queryAction = store.queryAction(), summary = state?.summary ?? queryAction?.target.summary;
  let actions: SelectionActionOffer[] = [];
  // Whole views have Play/Queue executors. Large-query Delete/Download retain
  // their unavailable controls and reasons without inventing a producer.
  const explicit = !!state && state.target?.mode !== 'query';
  const queryTarget = state?.target?.mode === 'query' ? state.target : undefined;
  // Unchecking one title refines the whole-view target, and the query executor refuses
  // that refinement: the control is hidden rather than offered and guaranteed to fail.
  const playable = !!state && !!summary?.every.play && (!queryTarget || queryExecutable(queryTarget, 'play'));
  const queueable = !!state && !!summary?.every.queue && store.queueControl() && (!queryTarget || queryExecutable(queryTarget, 'queue'));
  if (playable) actions.push({id: 'play', disabled: store.busy()});
  if (queueable) actions.push({id: 'queue', disabled: store.busy()}, {id: 'playNext', disabled: store.busy()}, {id: 'playLater', disabled: store.busy()});
  if (explicit && summary?.every.myList) actions.push({id: 'myList', disabled: store.busy()}, {id: 'removeMyList', disabled: store.busy()});
  if (explicit && summary?.every.favorite) actions.push({id: 'favorite', disabled: store.busy()});
  if (explicit && summary?.every.playlist) actions.push({id: 'playlist', disabled: store.busy()});
  if (explicit && summary?.every.collection) actions.push({id: 'collection', disabled: store.busy()});
  if (explicit && summary?.every.edit) actions.push({id: 'edit', disabled: store.busy()});
  if (explicit && summary?.every.delete) actions.push({id: 'delete', disabled: store.busy()});
  if (explicit && summary?.every.watched) actions.push({id: 'watched', disabled: store.busy()}, {id: 'unwatched', disabled: store.busy()});
  const unavailable = nativeWholeQueryUnavailableActions(state); actions.push(...unavailable);
  actions = nativeSelectionUnavailableActions(actions, summary);
  // Only the exact A1 producer replaces its own 47b unavailable offer.
  for (const kind of ['delete', 'download'] as const) if (store.queryOffer(kind, state)) {
    actions = actions.filter(offer => offer.id !== kind);
    actions.push({id: kind, disabled: store.busy()});
  }
  if ((explicit || queryTarget || unavailable.length > 0) && actions.some(action => ['removeMyList', 'favorite', 'watched', 'unwatched', 'edit', 'delete', 'download'].includes(action.id))) actions.push({id: 'more', disabled: store.busy()});
  const personal = store.personal(), destination = store.destination(), destinationKind = store.destinationKind(), whole = store.whole(), wholeUnavailable = store.wholeUnavailable();
  const bulk = store.bulk(), bulkState = bulk?.getSnapshot(), preview: BulkMetadataPreview | undefined = bulkState?.preview;
  const bulkSummary: readonly {id: string; text: string}[] = bulkState ? Object.entries(bulkState.changes).flatMap(([field, change]) => {
    const spec = preview?.schema.find(candidate => candidate.field === field);
    if (!spec) return [];
    if ('value' in change) return [{id: field, text: `${spec.label}: ${change.value || defaultI18n.t('next.selection.native.notSet')}`}];
    const parts: string[] = [];
    if (change.add.length) parts.push(`+${defaultI18n.list(change.add)}`);
    if (change.remove.length) parts.push(`−${defaultI18n.list(change.remove)}`);
    return parts.length ? [{id: field, text: `${spec.label}: ${parts.join(' · ')}`}] : [];
  }) : [];
  const props: SelectionBarProps = {count: summary?.count ?? 0, selectionLabel: summary ? selectedItemsLabel(summary) : undefined,
    onCancel: () => {setMenu(null); if (queryAction) store.closeQueryAction(queryAction); else store.cancel(state!);}, actions, menu, onMenuChange: setMenu,
    ...(wholeUnavailable ? {selectAllUnavailable: {label: defaultI18n.t('next.selection.selectAllUnavailableLabel'), reason: defaultI18n.t('next.selection.selectAllSearchNotBounded')}} : whole ? {selectAllLabel: defaultI18n.t('next.selection.selectAll', {count: whole.total}), onSelectAll: () => store.selectWhole(state!, whole.id)} : {}),
    fourthSlot: summary && selectionFourthAction(summary) === 'mark-watched' ? 'watched' : 'myList',
    onAction: action => {
      if ((action === 'delete' || action === 'download') && state?.target?.mode === 'query') {setMenu(null); store.openQueryAction(action, state);}
      else if (action === 'play' || action === 'playNext' || action === 'playLater') {setMenu(null); void store.execute(action, state!);}
      else if (action === 'myList' || action === 'removeMyList' || action === 'favorite' || action === 'watched' || action === 'unwatched') {setMenu(null); void store.applyPersonal(action, state!);}
      else if (action === 'playlist' || action === 'collection') {setMenu(null); store.openDestination(action, state!);}
      else if (action === 'edit' || action === 'delete') {setMenu(null); store.openAdmin(action, state!);}
    }};
  // These handlers run later, so they re-read the live operation instead of the
  // render's own: a retired result set must never clear a successor's selection.
  const token = store.resultsToken(), failed = !!personal?.results.some(result => result.status === 'failed');
  const current = () => !store.personalBusy() && store.resultsToken() === token;
  const done = () => {if (current()) store.clear();};
  const retry = () => {if (current()) void store.retryPersonal();};
  const closeResults = (open: boolean) => {if (!open && current()) store.clear();};
  const title = summary ? defaultI18n.t('next.selection.selectedItems', {items: selectedItemsLabel(summary)}) : '';
  const rows = personal?.results.map(result => <View key={result.key} style={{gap: 4, marginBottom: 12}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{result.title}</Text>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{result.status === 'saved' ? defaultI18n.t('next.selection.saved') : presentError({code: 'unavailable'}, 'detail', {operation: 'action'}).body}</Text>
  </View>);
  // select 26: Try again holds focus when a title failed, Done otherwise.
  const footer = personal && <><Button ref={doneRef} focusId="done" disabled={store.personalBusy()} onPress={done}>{defaultI18n.t('next.selection.done')}</Button>
    {failed && <Button ref={retryRef} focusId="retry" disabled={store.personalBusy()} onPress={retry}>{defaultI18n.t('next.selection.retry')}</Button>}</>;
  return <>
    <View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.navigation + 1}}>
      {!theme.isTV && <View style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><SelectionPhoneActionBar {...props} bottomInset={Math.max(34, insets.bottom)}/></View>}
    </View>
    {theme.isTV ? <View style={{marginLeft: 200, marginRight: 96}}><SelectionBar {...props}/></View> : <SelectionBar {...props}/>}
    {personal && summary && (theme.isTV
      ? <SidePanel testID="library-selection-results" open onOpenChange={closeResults} showClose={false} dismissOnBack={!store.personalBusy()}
        title={title} width={640} topInset={0} initialFocusRef={failed ? retryRef : doneRef} footer={footer}>{rows}</SidePanel>
      : <Dialog open title={title} onOpenChange={closeResults} footer={footer}>{rows}</Dialog>)}
    {destination && destinationKind && <NativeDestinationPicker controller={destination} kind={destinationKind} open
      selectionLabel={summary ? selectedItemsLabel(summary) : ''} count={summary?.count ?? 0} artwork={artwork}
      onOpenChange={open => {if (!open && !destination.snapshot().busy) store.closeDestinations(destination);}}/>}
    {queryAction && queryAction.controller.getSnapshot().input?.action === 'delete' && <DeleteMediaDialog queryAction={queryAction}
      ownerHeader={summary ? selectedItemsLabel(summary) : undefined} onClose={() => store.closeQueryAction(queryAction)}/>}
    {bulk && bulkState && (bulkState.deletion
      ? <DeleteMediaDialog open owner ownerHeader={<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{summary ? selectedItemsLabel(summary) : ''}</Text>}
          selectionCount={summary?.count ?? 0} preview={bulkState.deletion.preview ?? null} loading={bulkState.deletion.phase === 'loading'} error={bulkState.deletion.error}
          result={bulkState.deletion.result} confirmation={bulkState.deletion.confirmation} onConfirmationChange={value => bulk.setDeletion({confirmation: value})}
          removeFiles={bulkState.deletion.removeFiles} onRemoveFilesChange={value => bulk.setDeletion({removeFiles: value})}
          onDelete={() => {void bulk.deleteNow();}} pending={bulkState.deletion.phase === 'saving'} renderArtwork={() => undefined}
          onOpenChange={open => {if (!open) store.closeBulk(bulk);}} testID="library-delete-media"/>
      : <BulkEditor open title={defaultI18n.t('next.selection.native.action.edit')} count={summary?.count ?? 0} saveLabel={defaultI18n.t('action.save')}
          schema={bulkState.preview?.schema ?? []} values={bulkState.values} changes={bulkState.changes} onChangesChange={bulk.setChanges}
          lockEdited={bulkState.lockEdited} onLockEditedChange={bulk.setLockEdited} onSave={() => {void bulk.save();}}
          pending={bulkState.phase === 'saving'} error={bulkState.error} result={bulkState.result}
          resultEntries={store.entries().map(entry => ({id: entry.id, title: entry.displayTitle ?? entry.title, artwork: artwork(entry.posterUrl)}))}
          onRetry={ids => {void bulk.retry(ids);}} singleItemFields={bulkState.singleItemFields}
          fieldLabel={field => field.label} summary={bulkSummary}
          ownerHeader={<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{summary ? selectedItemsLabel(summary) : ''}</Text>}
          onOpenChange={open => {if (!open) store.closeBulk(bulk);}} testID="library-bulk-editor"/>)}
  </>;
}
