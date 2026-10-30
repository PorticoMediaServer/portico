import React, {useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry} from '../../../../../packages/client-core/src/search';
import {SelectionModel, BrowseSelectionModel, selectionKey, browseSelectionKey, selectionFourthAction, readCatalogueSelectionItem, playCatalogueSelection, enqueueCatalogueSelection, createCataloguePersonalSelection,
  type SelectionContext, type BrowseSelectionContext, type SelectionSnapshot, type SelectionWindow, type SelectionItem} from '../../../../../packages/client-core/src/selection-model';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {mediaSelectionKey} from '../../../../../packages/client-core/src/media-selection';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {selectedItemsLabel} from '../../shell/selection-display';
import {BulkEditor, DeleteMediaDialog, SelectionBar, SelectionPhoneActionBar, type SelectionActionOffer, type SelectionBarProps} from '../../kit/selection';
import type {CardSelection} from '../../kit/cards';
import {useTheme} from '../../kit/theme';
import {Dialog} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeScreenServices} from '../native-screen-services';
import {NativeLoadedDestinations} from '../search/loaded-destinations';
import {NativeDestinationPicker} from '../destinations/DestinationPicker';
import {NativeBulkMedia} from '../destinations/bulk-media';

const leaf = (entry: SearchEntry): entry is ContentEntry => ['movie', 'episode', 'song', 'audiobook_file'].includes(entry.kind);
const aggregate = (entry: SearchEntry): entry is ContentEntry => 'libraryId' in entry && !!entry.libraryId
  && ['show', 'season', 'album', 'artist', 'book', 'collection'].includes(entry.kind);
const entityKey = (entry: ContentEntry) => JSON.stringify([entry.libraryId, entry.kind, entry.id]);
const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
const unavailable = () => Object.assign(new Error('catalogue-selection-unavailable'), {code: 'catalogue-selection-unavailable'});

/** One loaded group of a page: a search group, a shelf, a list section or a credit window.
 * `positions` gives sparse absolute positions; otherwise items start at `offset`. */
export type NativeSelectionGroup = Readonly<{id: string; status?: 'success' | 'error'; offset?: number; positions?: readonly number[]; items: readonly SearchEntry[]}>;
export type NativeSelectionSnapshot = Readonly<{
  /** Exact source/query identity. Appending a page does not change it. */
  key?: string;
  /** What a drawn Select control was rendered from; `start` refuses a stale one. */
  witness?: unknown;
  context?: SelectionContext | BrowseSelectionContext | null;
  groups: readonly NativeSelectionGroup[];
}>;
/** A route supplies its actual producer context and loaded membership. Detail supplies
 * grants for explicit leaves; it never substitutes a different screen's source fence. */
export type NativeSelectionSource = Readonly<{
  subscribe: (listener: () => void) => () => void;
  /** The page is the visible, current one. */
  current: () => boolean;
  getSnapshot: () => NativeSelectionSnapshot;
}>;
export type NativeSelectionOptions = Readonly<{
  /** Error copy and the dialogs' test IDs. */
  area: 'search' | 'saved';
  /** Offer Select all over the loaded leaves. */
  selectAll?: boolean;
  /** Shows, seasons, albums, artists, books and collections may be picked too. */
  aggregates?: boolean;
  /** A bounded window keeps explicit picks when their rows are evicted. Without it,
   * selection retires when a selected row leaves the page. */
  preserveEvicted?: boolean;
}>;
export type NativeSelection = Readonly<{
  /** Enter the page's real current selection context without selecting an item. */
  start?: (rendered?: unknown) => void;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => number;
  /** Reconciles with the source now; a screen may pass what it drew, which the source already owns. */
  register: (...drawn: readonly unknown[]) => () => void;
  /** The card's selection binding. `group` names the drawn group; without it, the first loaded one. */
  item: (entry: SearchEntry, group?: string) => CardSelection | undefined;
  render: (artwork?: (path?: string) => React.ReactNode) => React.ReactNode;
  /** False while a result, picker or admin dialog still owns the selection. */
  clear: () => boolean;
  dispose: () => void;
}>;

/** Section-shaped catalogue pages (Saved, collections, phone music): their own
 * activation port decides visibility, and every section is loaded. */
export type NativeCatalogueSelectionSnapshot = Readonly<{
  key: string;
  witness?: unknown;
  context: SelectionContext | null;
  /** `positions` gives each entry's absolute index in a sparse window; otherwise entries start at `start`. */
  sections: readonly Readonly<{id: string; entries: readonly ContentEntry[]; start?: number; positions?: readonly number[]}>[];
}>;
export function catalogueSelectionSource(destination: Readonly<{subscribe: (listener: () => void) => () => void; getSnapshot: () => NativeCatalogueSelectionSnapshot}>,
  activation: NativeDestinationActivationPort): NativeSelectionSource {
  return {
    subscribe: listener => {const stops = [destination.subscribe(listener), activation.subscribe(listener)]; return () => stops.forEach(stop => stop());},
    current: () => activation.getSnapshot().visible,
    getSnapshot: () => {
      const snapshot = destination.getSnapshot();
      return {key: snapshot.key, witness: snapshot.witness, context: snapshot.context,
        groups: snapshot.sections.map(section => ({id: section.id, offset: section.start, ...(section.positions ? {positions: section.positions} : {}), items: section.entries}))};
    },
  };
}

/** Preserve each producer's context domain; Browse revisions have no privacy fence. */
function pageSelectionModel(context: SelectionContext | BrowseSelectionContext) {
  const model = 'privacyFence' in context.scope
    ? new SelectionModel(context as SelectionContext, {maxWindows: 256, maxRegisteredItems: 256})
    : new BrowseSelectionModel(context as BrowseSelectionContext, {maxWindows: 256, maxRegisteredItems: 256});
  return {
    subscribe: (listener: () => void) => model.subscribe(listener), getSnapshot: () => model.getSnapshot(),
    registerWindow: (window: SelectionWindow) => model instanceof BrowseSelectionModel
      ? model.registerWindow(model.captureContext(), window) : model.registerWindow(model.captureContext(), window),
    key: (item: SelectionItem) => model instanceof BrowseSelectionModel
      ? browseSelectionKey(model.getSnapshot().context.scope, item) : selectionKey(model.getSnapshot().context.scope, item),
    toggle: (window: string, key: string) => model.toggle(window, key), clear: () => model.clear(), dispose: () => model.dispose(),
    captureOperation: () => {
      if (model instanceof BrowseSelectionModel) {const operation = model.captureOperation(); return operation && {target: operation.target, current: () => model.isOperationCurrent(operation)};}
      const operation = model.captureOperation(); return operation && {target: operation.target, current: () => model.isOperationCurrent(operation)};
    },
  };
}
type Snapshot = SelectionSnapshot<SelectionContext | BrowseSelectionContext>;

/** The one native multi-select owner for Search, Saved, collections, music and Person.
 * A page's actual fence, query and loaded objects own selection; it never borrows a
 * Home/Search scope or derives grants from an item's shape. */
export function createNativeSelection(context: NativeShellBootstrapContext, source: NativeSelectionSource, options: NativeSelectionOptions): NativeSelection {
  const authority = nativeDestinationLifetime(context), feedback = nativeScreenServices(context), area = options.area;
  const listeners = new Set<() => void>(), windows = new Map<string, {key: string; windowId: string; item: SelectionItem; entry: ContentEntry; group: string; unregister: () => void}>();
  let model: ReturnType<typeof pageSelectionModel> | undefined, stopModel: (() => void) | undefined, token = '', revision = 0, windowSequence = 0, disposed = false, started = false;
  let pending: AbortController | undefined, batchGeneration = 0;
  let stopPlayback: (() => void) | undefined;
  let personal: ReturnType<typeof createCataloguePersonalSelection> | undefined, stopPersonal: (() => void) | undefined;
  let personalController: AbortController | undefined, personalCurrent: (() => boolean) | undefined;
  let destinations: NativeLoadedDestinations | undefined, destinationKind: 'playlist' | 'collection' | undefined;
  let destinationController: AbortController | undefined, stopDestinations: (() => void) | undefined;
  let bulk: NativeBulkMedia | undefined, bulkController: AbortController | undefined, stopBulk: (() => void) | undefined;
  const closePersonal = () => {personalController?.abort(); personalController = undefined; stopPersonal?.(); stopPersonal = undefined; personal?.dispose(); personal = undefined; personalCurrent = undefined;};
  /** Only the dialog that still owns the surface closes; reset may force-close. */
  const closeBulk = (expected?: NativeBulkMedia) => {
    if (expected && bulk !== expected) return;
    bulkController?.abort(); bulkController = undefined; stopBulk?.(); stopBulk = undefined; bulk?.dispose(); bulk = undefined;};
  const closeDestinations = () => {destinationController?.abort(); destinationController = undefined; stopDestinations?.(); stopDestinations = undefined;
    destinations?.dispose(); destinations = undefined; destinationKind = undefined;};
  const emit = () => {revision++; for (const listener of [...listeners]) listener();};
  const keyFor = (snapshot: NativeSelectionSnapshot) => snapshot.context ? JSON.stringify([snapshot.key ?? null, snapshot.context]) : '';
  const visible = () => !disposed && authority.current() && source.current();
  const selectable = (entry: SearchEntry): entry is ContentEntry => leaf(entry) || options.aggregates === true && aggregate(entry);
  const queueControl = () => {
    const role = context.playback.getSnapshot().session?.role;
    return role === undefined || role === 'local';
  };
  const reset = () => {
    batchGeneration++; closePersonal(); closeDestinations(); closeBulk();
    stopPlayback?.(); stopPlayback = undefined;
    pending?.abort(); pending = undefined; stopModel?.(); stopModel = undefined;
    model?.dispose(); model = undefined; windows.clear(); token = ''; started = false; emit();
  };
  const loadedIn = (group: NativeSelectionGroup, entry: ContentEntry) => group.status !== 'error' && group.items.includes(entry);
  const reconcile = () => {
    const snapshot = source.getSnapshot(), next = visible() ? keyFor(snapshot) : '';
    if (next === token) {
      const missing = [...windows].filter(([, window]) => !snapshot.groups.some(group => group.id === window.group && loadedIn(group, window.entry)));
      if (!missing.length || options.preserveEvicted) {
        // Only declared bounded producers retain explicit picks across ordinary
        // window eviction. Other pages retire selection on membership loss.
        const selected = model?.getSnapshot().selectedKeys ?? [];
        for (const [id, window] of missing) if (!selected.includes(window.key)) {
          window.unregister(); windows.delete(id);
        }
        return;
      }
    }
    reset();
    if (!next || !snapshot.context) return;
    const viewer = context.session()?.viewer;
    if (!viewer || viewer.authority !== 'local' && viewer.authority !== 'hosted') return;
    const captured = snapshot.context;
    if (captured.scope.serverId !== viewer.serverId || captured.scope.authority !== viewer.authority || captured.scope.accountId !== viewer.accountId
      || captured.scope.profileId !== viewer.profileId || captured.scope.viewId.length > 4096) return;
    model = pageSelectionModel(captured); token = next;
    stopModel = model.subscribe(emit); emit();
  };
  /** The drawn group that holds this exact object: the named one, or the first loaded one. */
  const groupOf = (entry: ContentEntry, group?: string) => !visible() ? undefined
    : source.getSnapshot().groups.find(value => (group === undefined || value.id === group) && loadedIn(value, entry))?.id;
  const selectedLeaves = (keys: readonly string[]) => {
    const byKey = new Map([...windows.values()].map(window => [window.key, window.entry] as const));
    return keys.flatMap(key => {const entry = byKey.get(key); return entry && leaf(entry) ? [entry] : [];});
  };
  const start = (rendered?: unknown) => {
    if (!visible() || rendered !== undefined && source.getSnapshot().witness !== rendered) return;
    reconcile();
    if (!model) return;
    started = true; emit();
  };
  const pick = async (entry: ContentEntry, drawn?: string, batch?: number) => {
    if (batch === undefined) batchGeneration++;
    else if (batch !== batchGeneration) return;
    reconcile();
    const currentModel = model, group = groupOf(entry, drawn);
    if (!currentModel || pending || personal || destinations || bulk || !selectable(entry) || group === undefined) return;
    const id = entityKey(entry), registered = windows.get(id);
    if (registered) {currentModel.toggle(registered.windowId, registered.key); return;}
    if (windows.size >= 256) {feedback.reportError(unavailable(), area); return;}
    const controller = new AbortController(), captured = token, route = context.navigation.getSnapshot().revision;
    pending = controller; emit();
    const current = () => !controller.signal.aborted && visible() && model === currentModel && token === captured
      && keyFor(source.getSnapshot()) === captured && context.navigation.getSnapshot().revision === route && groupOf(entry, group) === group;
    try {
      observePlayback();
      const port = context.playback.cataloguePlayback?.();
      const item = await readCatalogueSelectionItem({api: context.api, scope: authority.scope, requestId: feedback.requestId,
        signal: controller.signal, isCurrent: current}, entry, {play: !!port, queue: !!port && queueControl(),
          ...(leaf(entry) ? {myList: true, favorite: true, watched: true, edit: true, delete: true, playlist: true, collection: true} : {})});
      if (!current()) throw cancelled();
      const groups = source.getSnapshot().groups, sectionOrder = groups.findIndex(value => value.id === group);
      const section = groups[sectionOrder]!, index = section.items.indexOf(entry);
      const position = section.positions ? section.positions[index] : (section.offset ?? 0) + index;
      if (typeof position !== 'number' || !Number.isSafeInteger(position) || position < 0) throw unavailable();
      const windowId = `${area}-${++windowSequence}`;
      const registration = currentModel.registerWindow({id: windowId, sectionId: group, sectionOrder, start: position, items: [item]});
      if (!registration.ok) throw unavailable();
      const key = currentModel.key(item);
      windows.set(id, {key, windowId, item, entry, group, unregister: registration.unregister});
      const selected = currentModel.toggle(windowId, key);
      if (!selected.ok) throw unavailable();
    } catch (error) {if (current()) feedback.reportError(error, area);}
    finally {if (pending === controller) {pending = undefined; emit();}}
  };
  const distinctLeaves = () => {
    const rows = source.getSnapshot().groups.filter(group => group.status !== 'error').flatMap(group =>
      group.items.filter(leaf).map(entry => ({group: group.id, entry})));
    return [...new Map(rows.map(row => [entityKey(row.entry), row])).values()];
  };
  const selectLoaded = async () => {
    if (personal || destinations || bulk) return;
    reconcile();
    const batch = ++batchGeneration;
    pending?.abort(); pending = undefined;
    if (!model || !visible()) return;
    const captured = token, currentModel = model, route = context.navigation.getSnapshot().revision;
    const current = () => batchGeneration === batch && visible() && token === captured && model === currentModel
      && context.navigation.getSnapshot().revision === route && keyFor(source.getSnapshot()) === captured;
    const unique = distinctLeaves();
    if (unique.length > 256) {feedback.reportError(unavailable(), area); return;}
    for (const row of unique) {
      if (!current()) return;
      const registered = windows.get(entityKey(row.entry));
      if (registered && currentModel.getSnapshot().selectedKeys.includes(registered.key)) continue;
      await pick(row.entry, row.group, batch);
      if (!current()) return;
    }
  };
  /** The captured explicit operation, still current and still over registered rows. */
  const operationCurrent = (operation: NonNullable<ReturnType<ReturnType<typeof pageSelectionModel>['captureOperation']>>) =>
    operation.current() && operation.target.mode === 'explicit'
      && operation.target.items.every(chosen => [...windows.values()].some(window => window.key === chosen.key));
  const execute = async (action: 'play' | 'playNext' | 'playLater') => {
    batchGeneration++;
    reconcile();
    const currentModel = model, operation = currentModel?.captureOperation();
    if (!currentModel || !operation || operation.target.mode !== 'explicit' || pending || personal || bulk || !visible()) return;
    const controller = new AbortController(), captured = token, route = context.navigation.getSnapshot().revision;
    const queue = action !== 'play';
    const current = () => !controller.signal.aborted && visible() && model === currentModel && token === captured
      && keyFor(source.getSnapshot()) === captured && context.navigation.getSnapshot().revision === route
      && operationCurrent(operation) && (!queue || queueControl());
    pending = controller; emit();
    try {
      const port = context.playback.cataloguePlayback?.();
      if (!port || !current()) throw unavailable();
      const target = operation.target;
      const items = (queue ? target.items : target.displayItems).map(value => value.item);
      if (queue) await enqueueCatalogueSelection(port, items, action === 'playNext' ? 'next' : 'end', {signal: controller.signal, isCurrent: current});
      else await playCatalogueSelection(port, items, {signal: controller.signal, isCurrent: current});
      if (current()) currentModel.clear();
    } catch (error) {if (current()) feedback.reportError(error, area);}
    finally {if (pending === controller) {pending = undefined; emit();}}
  };
  const applyPersonal = async (action: 'myList' | 'removeMyList' | 'favorite' | 'watched' | 'unwatched') => {
    batchGeneration++; reconcile();
    const currentModel = model, operation = currentModel?.captureOperation();
    if (!currentModel || !operation || operation.target.mode !== 'explicit' || pending || personal || bulk || !visible()) return;
    const capability = action === 'myList' || action === 'removeMyList' ? 'myList' : action === 'favorite' ? 'favorite' : 'watched';
    if (!operation.target.items.every(chosen => chosen.item.capabilities[capability])) return;
    const captured = token, route = context.navigation.getSnapshot().revision, controller = new AbortController();
    const current = () => !controller.signal.aborted && visible() && model === currentModel && token === captured
      && keyFor(source.getSnapshot()) === captured && context.navigation.getSnapshot().revision === route && operationCurrent(operation);
    const entries = selectedLeaves(operation.target.items.map(chosen => chosen.key));
    if (entries.length !== operation.target.items.length || !current()) return;
    personalController = controller; personalCurrent = current;
    personal = createCataloguePersonalSelection({api: context.api, scope: authority.scope, requestId: feedback.requestId,
      signal: controller.signal, isCurrent: current});
    stopPersonal = personal.service.subscribe(emit);
    personal.service.setEntries(entries); personal.service.begin();
    for (const entry of entries) personal.service.toggle(mediaSelectionKey(entry));
    emit();
    try {await personal.service.apply(capability === 'myList' ? 'watchlist' : capability, action !== 'removeMyList' && action !== 'unwatched');}
    catch (error) {if (current()) feedback.reportError(error, area);}
    finally {emit();}
  };
  const retryPersonal = async () => {
    if (!personal || !personalCurrent?.() || personal.service.getSnapshot().busy) return;
    try {await personal.service.retry();}
    catch (error) {if (personalCurrent?.()) feedback.reportError(error, area);}
  };
  /** The bounded direct Saved destination picker for exactly the selected leaves. */
  const openDestination = (kind: 'playlist' | 'collection') => {
    if (!model || !visible() || pending || personal || destinations || bulk) return;
    const state = model.getSnapshot();
    if (!state.active || !state.selectedKeys.length) return;
    const selected = selectedLeaves(state.selectedKeys);
    if (selected.length !== state.selectedKeys.length) return;
    const captured = token, currentModel = model, route = context.navigation.getSnapshot().revision;
    const controller = new AbortController();
    const current = () => !controller.signal.aborted && visible() && model === currentModel && token === captured
      && context.navigation.getSnapshot().revision === route && keyFor(source.getSnapshot()) === captured
      && currentModel.getSnapshot().active;
    destinationController = controller;
    destinations = new NativeLoadedDestinations({api: context.api, scope: authority.scope, requestId: feedback.requestId, viewerFence: context.authority().viewerFence,
      signal: controller.signal, isCurrent: current}, selected);
    destinationKind = kind;
    stopDestinations = destinations.subscribe(emit);
    void destinations.load(kind).catch(error => {if (current()) feedback.reportError(error, area);});
    emit();
  };
  // The owner's real bulk editor and delete dialog, over exactly the selected AV leaves
  // the canonical Detail read granted edit/delete. The bar only offers these when every
  // selected item carries the grant; this owner rechecks the captured explicit operation.
  const openAdmin = (kind: 'edit' | 'delete', view: Snapshot) => {
    const currentModel = model;
    if (!currentModel || view !== currentModel.getSnapshot() || !visible() || pending || personal || destinations || bulk) return;
    const state = currentModel.getSnapshot();
    if (!state.active || !state.selectedKeys.length || !state.summary.every[kind]) return;
    const operation = currentModel.captureOperation();
    if (!operation || operation.target.mode !== 'explicit') return;
    const selected = selectedLeaves(state.selectedKeys);
    if (selected.length !== state.selectedKeys.length) return;
    const captured = token, route = context.navigation.getSnapshot().revision, controller = new AbortController();
    const current = () => !controller.signal.aborted && visible() && model === currentModel && token === captured
      && context.navigation.getSnapshot().revision === route && operation.current();
    bulkController = controller;
    bulk = new NativeBulkMedia({api: context.api, scope: authority.scope, requestId: feedback.requestId, signal: controller.signal, isCurrent: current}, selected);
    stopBulk = bulk.subscribe(emit);
    void (kind === 'edit' ? bulk.openEditor() : bulk.openDelete()).catch(error => {if (current()) feedback.reportError(error, 'detail');});
    emit();
  };
  let lastQueueControl: boolean | undefined;
  const queueChanged = () => {
    const next = queueControl();
    if (next === lastQueueControl) return;
    lastQueueControl = next;
    pending?.abort(); emit();
  };
  const observePlayback = () => {
    if (stopPlayback) return;
    lastQueueControl = queueControl();
    stopPlayback = context.playback.subscribe(queueChanged);
  };
  const off = [source.subscribe(reconcile), context.subscribeAuthority(reconcile),
    context.navigation.subscribe(() => {batchGeneration++; pending?.abort(); reconcile();})];
  reconcile();
  const clear = () => {
    if (personal?.service.getSnapshot().busy || destinations?.snapshot().busy || bulk) return false;
    batchGeneration++; pending?.abort(); closePersonal(); closeDestinations(); model?.clear(); started = false; emit(); return true;
  };
  const store: Store = {selectAll: options.selectAll === true, area,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => revision, loadedCount: () => distinctLeaves().length, snapshot: () => model?.getSnapshot(), personal: () => personal?.service.getSnapshot(),
    busy: () => !!pending || !!personal?.service.getSnapshot().busy || !!destinations?.snapshot().busy || !!bulk, queueControl, started: () => started,
    clear, selectLoaded, execute, applyPersonal, retryPersonal, openDestination, closeDestinations, destination: () => destinations, destinationKind: () => destinationKind,
    // An open bulk dialog owns the current operation: the bar must not offer another.
    openAdmin, closeBulk, bulk: () => bulk,
    entries: () => {const state = model?.getSnapshot(); return state ? selectedLeaves(state.selectedKeys) : [];}};
  return Object.freeze({
    subscribe: store.subscribe, getSnapshot: store.getSnapshot, start, clear,
    register: () => {reconcile(); return () => {};},
    item: (entry: SearchEntry, group?: string) => {
      if (!model || !selectable(entry)) return undefined;
      const drawn = groupOf(entry, group);
      if (drawn === undefined) return undefined;
      const drawnModel = model, drawnToken = token, snapshot = model.getSnapshot(), registered = windows.get(entityKey(entry));
      const toggle = () => {if (model === drawnModel && token === drawnToken && keyFor(source.getSnapshot()) === drawnToken && groupOf(entry, drawn) === drawn) void pick(entry, drawn);};
      return {active: snapshot.active || started && visible(), selected: !!registered && snapshot.selectedKeys.includes(registered.key), onEnter: toggle, onToggle: toggle};
    },
    render: artwork => visible() ? <NativeSelectionBars store={store} artwork={artwork ?? (() => null)}/> : null,
    dispose: () => {if (disposed) return; disposed = true; off.forEach(stop => stop()); reset(); listeners.clear();},
  });
}

type Store = {selectAll: boolean; area: NativeSelectionOptions['area']; subscribe(listener: () => void): () => void; getSnapshot(): number; snapshot(): Snapshot | undefined;
  personal(): ReturnType<ReturnType<typeof createCataloguePersonalSelection>['service']['getSnapshot']> | undefined;
  applyPersonal(action: 'myList' | 'removeMyList' | 'favorite' | 'watched' | 'unwatched'): Promise<void>; retryPersonal(): Promise<void>;
  loadedCount(): number; busy(): boolean; queueControl(): boolean; started(): boolean; clear(): boolean; selectLoaded(): Promise<void>; execute(action: 'play' | 'playNext' | 'playLater'): Promise<void>;
  openDestination(kind: 'playlist' | 'collection'): void; closeDestinations(): void; destination(): NativeLoadedDestinations | undefined; destinationKind(): 'playlist' | 'collection' | undefined;
  openAdmin(kind: 'edit' | 'delete', view: Snapshot): void; closeBulk(expected?: NativeBulkMedia): void; bulk(): NativeBulkMedia | undefined; entries(): readonly ContentEntry[]};

function NativeSelectionBars({store, artwork}: {store: Store; artwork: (path?: string) => React.ReactNode}) {
  useSyncExternalStore(store.subscribe, store.getSnapshot);
  const theme = useTheme(), insets = useSafeAreaInsets(), [menu, setMenu] = useState<SelectionBarProps['menu']>(null);
  const state = store.snapshot();
  if (!state || !state.active && !store.started()) return null;
  const actions: SelectionActionOffer[] = [];
  if (state.summary.count > 0) {
    if (state.summary.every.play) actions.push({id: 'play', disabled: store.busy()});
    if (state.summary.every.queue && store.queueControl()) actions.push({id: 'queue', disabled: store.busy()},
      {id: 'playNext', disabled: store.busy()}, {id: 'playLater', disabled: store.busy()});
    if (state.summary.every.myList) actions.push({id: 'myList', disabled: store.busy()}, {id: 'removeMyList', disabled: store.busy()});
    if (state.summary.every.favorite) actions.push({id: 'favorite', disabled: store.busy()});
    // The bounded direct Saved pickers are offered for an explicit leaf selection; the
    // controller re-reads every leaf's own Detail grant before it writes anything.
    if (state.summary.every.playlist) actions.push({id: 'playlist', disabled: store.busy()});
    if (state.summary.every.collection) actions.push({id: 'collection', disabled: store.busy()});
    if (state.summary.every.edit) actions.push({id: 'edit', disabled: store.busy()});
    if (state.summary.every.delete) actions.push({id: 'delete', disabled: store.busy()});
    if (state.summary.every.watched) actions.push({id: 'watched', disabled: store.busy()}, {id: 'unwatched', disabled: store.busy()});
    if (actions.some(action => ['removeMyList', 'favorite', 'watched', 'unwatched', 'edit', 'delete'].includes(action.id))) actions.push({id: 'more', disabled: store.busy()});
  }
  const personal = store.personal();
  const props: SelectionBarProps = {count: state.summary.count, selectionLabel: selectedItemsLabel(state.summary),
    ...(store.selectAll ? {selectAllLabel: defaultI18n.t('next.selection.selectAll', {count: store.loadedCount()}), onSelectAll: () => {void store.selectLoaded();}} : {}), onCancel: () => {setMenu(null); store.clear();},
    actions, menu, onMenuChange: setMenu, topInset: insets.top, fourthSlot: selectionFourthAction(state.summary) === 'mark-watched' ? 'watched' : 'myList',
    onAction: action => {if (action === 'play' || action === 'playNext' || action === 'playLater') {setMenu(null); void store.execute(action);}
      else if (action === 'myList' || action === 'removeMyList' || action === 'favorite' || action === 'watched' || action === 'unwatched') {setMenu(null); void store.applyPersonal(action);}
      else if (action === 'playlist' || action === 'collection') {setMenu(null); store.openDestination(action);}
      else if (action === 'edit' || action === 'delete') {setMenu(null); store.openAdmin(action, state);}}};
  const destination = store.destination(), destinationKind = store.destinationKind();
  const bulk = store.bulk(), bulkState = bulk?.getSnapshot();
  const bulkSummary: readonly {id: string; text: string}[] = bulkState ? Object.entries(bulkState.changes).flatMap(([field, change]) => {
    const spec = bulkState.preview?.schema.find(candidate => candidate.field === field);
    if (!spec) return [];
    if ('value' in change) return [{id: field, text: `${spec.label}: ${change.value || defaultI18n.t('next.selection.native.notSet')}`}];
    const parts: string[] = [];
    if (change.add.length) parts.push(`+${defaultI18n.list(change.add)}`);
    if (change.remove.length) parts.push(`−${defaultI18n.list(change.remove)}`);
    return parts.length ? [{id: field, text: `${spec.label}: ${parts.join(' · ')}`}] : [];
  }) : [];
  return <><View pointerEvents="box-none" style={{position: 'absolute', inset: 0, zIndex: theme.layers.navigation + 1}}>
    <View style={theme.isTV ? {position: 'absolute', left: 200, right: 96, top: 58} : undefined}><SelectionBar {...props}/></View>
    {!theme.isTV && <View style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><SelectionPhoneActionBar {...props} bottomInset={Math.max(34, insets.bottom)}/></View>}
  </View>
    {personal && <Dialog testID={`${store.area}-personal-results`} open title={defaultI18n.t('next.selection.selectedItems', {items: selectedItemsLabel(state.summary)})}
      onOpenChange={open => {if (!open && !personal.busy) store.clear();}}
      footer={<><Button focusId="done" disabled={personal.busy} onPress={store.clear}>{defaultI18n.t('next.selection.done')}</Button>
        {personal.results.some(result => result.status === 'failed') && <Button focusId="retry" disabled={personal.busy} onPress={() => {void store.retryPersonal();}}>{defaultI18n.t('next.selection.retry')}</Button>}</>}>
      {personal.results.map(result => <View key={result.key} style={{gap: 4, marginBottom: 12}}>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{result.title}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{result.status === 'saved' ? defaultI18n.t('next.selection.saved')
          : result.status === 'waiting' || result.status === 'working' ? defaultI18n.t('next.states.loading')
            : presentError({code: result.status === 'conflict' ? 'metadata_conflict' : result.status === 'unavailable' ? 'access_refused' : 'unavailable'}, 'detail', {operation: 'action'}).body}</Text>
      </View>)}
    </Dialog>}
    {destination && destinationKind && <NativeDestinationPicker controller={destination} kind={destinationKind} open
      selectionLabel={selectedItemsLabel(state.summary)} count={state.summary.count} artwork={artwork}
      onOpenChange={open => {if (!open && !destination.snapshot().busy) store.closeDestinations();}}/>}
    {bulk && bulkState && (bulkState.deletion
      ? <DeleteMediaDialog open owner ownerHeader={<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{selectedItemsLabel(state.summary)}</Text>}
          selectionCount={state.summary.count} preview={bulkState.deletion.preview ?? null} loading={bulkState.deletion.phase === 'loading'} error={bulkState.deletion.error}
          result={bulkState.deletion.result} confirmation={bulkState.deletion.confirmation} onConfirmationChange={value => bulk.setDeletion({confirmation: value})}
          removeFiles={bulkState.deletion.removeFiles} onRemoveFilesChange={value => bulk.setDeletion({removeFiles: value})}
          onDelete={() => {void bulk.deleteNow();}} pending={bulkState.deletion.phase === 'saving'} renderArtwork={() => undefined}
          onOpenChange={open => {if (!open) store.closeBulk(bulk);}} testID={`${store.area}-delete-media`}/>
      : <BulkEditor open title={defaultI18n.t('next.selection.native.action.edit')} count={state.summary.count} saveLabel={defaultI18n.t('action.save')}
          schema={bulkState.preview?.schema ?? []} values={bulkState.values} changes={bulkState.changes} onChangesChange={bulk.setChanges}
          lockEdited={bulkState.lockEdited} onLockEditedChange={bulk.setLockEdited} onSave={() => {void bulk.save();}}
          pending={bulkState.phase === 'saving'} error={bulkState.error} result={bulkState.result}
          resultEntries={store.entries().map(entry => ({id: entry.id, title: entry.displayTitle ?? entry.title, artwork: artwork(entry.posterUrl)}))}
          onRetry={ids => {void bulk.retry(ids);}} singleItemFields={bulkState.singleItemFields}
          fieldLabel={field => field.label} summary={bulkSummary}
          ownerHeader={<Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{selectedItemsLabel(state.summary)}</Text>}
          onOpenChange={open => {if (!open) store.closeBulk(bulk);}} testID={`${store.area}-bulk-editor`}/>)}
  </>;
}
