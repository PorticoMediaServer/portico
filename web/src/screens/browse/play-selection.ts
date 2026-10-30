import {playCatalogueQuerySelection, enqueueCatalogueQuerySelection} from '@core/catalogue-query-selection.ts';
import {BrowseSelectionModel, browseSelectionKey, readCatalogueSelectionItem, playCatalogueSelection, enqueueCatalogueSelection, selectionCapabilities, selectableSelectionKind,
  type CatalogueSelectionQueuePort, type CatalogueSelectionIdentity,
  type BrowseSelectionScope, type BrowseSelectionContext, type SelectionItem, type SelectionOperation, type SelectionQuery,
  type SelectionSnapshot} from '@core/selection-model/index.ts';
import {selectionItem} from '@core/media-selection.ts';
import type {ContentEntry} from '@core/library-content.ts';
import type {DetailApi, DetailScope} from '@core/detail.ts';
import type {V1QueuePlayer} from '@core/playback-v1/v1-queue-player.ts';
import type {BrowseWholeSelection} from '@core/browse.ts';
import type {BrowseController} from './controller';
import {wholeViewQuery} from './selection-query';
import {createCardInteractionOwner} from '../../kit/cards/interaction-admission';

export type LibraryPlayRow = Readonly<{entry: ContentEntry; position: number; key: string; capabilities: SelectionItem['capabilities']}>;
export type LibraryPlaySelectionSnapshot = Readonly<{
  phase: 'waiting' | 'ready' | 'retired' | 'error';
  rows: readonly LibraryPlayRow[];
  selection?: SelectionSnapshot<BrowseSelectionContext>;
  resolving: boolean;
  playing: boolean;
  canPlay: boolean;
  canQueue?: boolean;
  error?: unknown;
}>;
/** Explicit catalogue consumers retain their own opaque source continuity. They
 * never register a Browse whole-query descriptor or reuse a Search fence as one. */
export type ExplicitCatalogueSelectionSource = Readonly<{
  viewIdentity(): string;
  getSnapshot(): Readonly<{phase: string; revision?: string; queryRevision: number}>;
  subscribe(listener: () => void): () => void;
  contains(entry: ContentEntry): boolean;
  collection: Readonly<{subscribe(listener: () => void): () => void; itemAt(index: number): Readonly<{entry: ContentEntry}> | undefined}>;
}>;
export type LibraryPlaySelectionOptions = Readonly<{
  source: BrowseController | ExplicitCatalogueSelectionSource;
  api: DetailApi;
  scope: DetailScope;
  principal: Omit<BrowseSelectionScope, 'viewId'>;
  requestId: () => Promise<string>;
  /** The original route/principal lease. isCurrent includes family/role retirement. */
  signal: AbortSignal;
  isCurrent: () => boolean;
  playback?: Pick<V1QueuePlayer, 'playSegments'>;
  /** The caller's real personal-state executors. Each port is checked against
   * the title's own Detail actions; absent means the capability is not offered. */
  editor?: boolean;
  deletion?: boolean;
  destinations?: boolean;
  queue?: CatalogueSelectionQueuePort;
  personal?: Readonly<{myList?: boolean; favorite?: boolean; watched?: boolean}>;
}>;
type Grant = {entry: ContentEntry; pending: boolean; controller: AbortController; promise: Promise<SelectionItem>; resolve: (item: SelectionItem) => void; reject: (error: unknown) => void};
type Window = {first: number; last: number; entries: readonly Readonly<{entry: ContentEntry; position: number}>[]; controller: AbortController; promise?: Promise<readonly LibraryPlayRow[]>; registrations: (() => void)[]};
const cancelled = () => new DOMException('Library selection retired', 'AbortError');
const unavailable = () => Object.assign(new Error('library-play-selection-unavailable'), {code: 'library-play-selection-unavailable'});
const terminal = (error: unknown) => typeof error === 'object' && error !== null
  && ('status' in error && (error.status === 401 || error.status === 403)
    || 'code' in error && typeof error.code === 'string' && ['unauthorized', 'forbidden', 'detail_scope_changed'].includes(error.code));

/** Headless, captured-parent adapter. No import/constructor reads, Home context,
 * aggregate expansion or role-derived grants. Only mounted resident windows
 * are registered; explicit model picks survive viewport eviction. */
export function createLibraryPlaySelection(input: LibraryPlaySelectionOptions) {
  const interactions = createCardInteractionOwner();
  const {source, api: originalApi, requestId, signal, isCurrent} = input;
  const principal = Object.freeze({serverId: input.principal.serverId, authority: input.principal.authority,
    accountId: input.principal.accountId, profileId: input.principal.profileId});
  const scope = Object.freeze({serverId: input.scope.serverId, viewerId: input.scope.viewerId});
  if (scope.serverId !== principal.serverId) throw unavailable();
  const playback = input.playback && Object.freeze({playSegments: input.playback.playSegments.bind(input.playback)});
  const queue = input.queue && Object.freeze({enqueueSegments: input.queue.enqueueSegments.bind(input.queue)});
  const personal = Object.freeze({...(input.personal ?? {})});
  const ports = Object.freeze({...(input.destinations ? {playlist: true as const, collection: true as const} : {}), ...(input.deletion ? {delete: true as const} : {}), ...(input.editor ? {edit: true as const} : {}),...(playback ? {play: true as const} : {}), ...(queue ? {queue: true as const} : {}),
    ...(personal.myList ? {myList: true as const} : {}), ...(personal.favorite ? {favorite: true as const} : {}),
    ...(personal.watched ? {watched: true as const} : {})});
  const executable = !!(input.destinations || input.deletion || input.editor || queue || playback || personal.myList || personal.favorite || personal.watched);
  const life = new AbortController(), listeners = new Set<() => void>(), grants = new Map<ContentEntry, Grant>(), queued: Grant[] = [];
  let active = 0, closed = false, started = false, model: BrowseSelectionModel | undefined, modelOff: (() => void) | undefined;
  let sourceOff = () => {}, collectionOff = () => {}, expected: Readonly<{queryRevision: number; revision: string}> | undefined;
  let window: Window | undefined, handles = new WeakMap<LibraryPlayRow, Readonly<{window: Window; run: string}>>();
  let whole: Readonly<{query: SelectionQuery; unregister: () => void}> | undefined;
  let playing: Readonly<{operation: SelectionOperation<BrowseSelectionContext>; controller: AbortController}> | undefined;
  let rangeTarget: LibraryPlayRow | undefined, rangeKeys = new Set<string>();
  let error: unknown;
  let state: LibraryPlaySelectionSnapshot = Object.freeze({phase: 'waiting', rows: Object.freeze([]), resolving: false, playing: false, canPlay: false});
  const notify = () => {for (const listener of [...listeners]) listener();};
  const publish = (patch: Partial<LibraryPlaySelectionSnapshot> = {}) => {
    if (closed) return;
    const selection = model?.getSnapshot();
    state = Object.freeze({...state, phase: model ? 'ready' : 'waiting', ...patch,
      ...(selection ? {selection} : {}), playing: !!playing, canPlay: !!playback && !!selection?.summary.every.play, canQueue: !!queue && !!selection?.summary.every.queue,
      ...(error === undefined ? {} : {error})});notify();
  };
  const clearPreview = () => {interactions.resetPreview();if (!rangeTarget) return;rangeTarget = undefined;rangeKeys.clear();publish();};
  const clearWindow = () => {
    interactions.resetPreview();rangeTarget = undefined;rangeKeys.clear();
    const previous = window;window = undefined;
    interactions.retireRows();
    previous?.controller.abort();for (const unregister of previous?.registrations ?? []) unregister();
    handles = new WeakMap();
  };
  const retire = (reason?: unknown, silent = false) => {
    if (closed) return;
    closed = true;interactions.dispose();sourceOff();collectionOff();modelOff?.();signal.removeEventListener('abort', dispose);
    life.abort();playing?.controller.abort();playing = undefined;clearWindow();
    whole?.unregister();whole = undefined;model?.dispose();
    for (const grant of grants.values()) {grant.controller.abort();grant.reject(cancelled());}
    grants.clear();queued.length = 0;
    state = Object.freeze({phase: reason === undefined ? 'retired' : 'error', rows: Object.freeze([]), resolving: false, playing: false, canPlay: false,
      ...(reason === undefined ? {} : {error: reason})});
    try {if (!silent) notify();} finally {listeners.clear();}
  };
  const current = () => {
    if (closed) return false;
    if (signal.aborted || !isCurrent()) {retire();return false;}
    const snapshot = source.getSnapshot();
    if (expected && (snapshot.queryRevision !== expected.queryRevision || snapshot.revision !== expected.revision)) {retire();return false;}
    return true;
  };
  const api: DetailApi = {request: async<T>(path: string, method?: string, body?: unknown, requestSignal?: AbortSignal): Promise<T> => {
    if (!current()) throw cancelled();
    try {const value = await originalApi.request<T>(path, method, body, requestSignal);if (!current()) throw cancelled();return value;}
    catch (failure) {if (terminal(failure)) retire(failure);throw failure;}
  }};
  const pump = () => {
    while (!closed && active < 4 && queued.length) {
      const grant = queued.shift()!;
      if (grant.controller.signal.aborted || grants.get(grant.entry) !== grant) continue;
      active++;
      const lifetime = AbortSignal.any([signal, life.signal, grant.controller.signal]);
      void readCatalogueSelectionItem({api, scope, requestId, signal: lifetime,
        isCurrent: () => current() && source.contains(grant.entry)}, grant.entry, ports).then(item => {grant.pending = false;grant.resolve(item);}, failure => {
          if (grants.get(grant.entry) === grant) grants.delete(grant.entry);
          if (terminal(failure)) retire(failure);grant.reject(failure);
        }).finally(() => {active--;pump();});
    }
  };
  const read = (entry: ContentEntry): Promise<SelectionItem> => {
    if (!entry.libraryId) return Promise.reject(unavailable());
    const leaf = selectionItem(entry);
    // A missing executor is not a grant: personal Detail reads do not depend on
    // holding a playback port, and every reported capability comes from Detail.
    if (!executable || (!leaf && !['show', 'season', 'album', 'artist', 'book', 'collection'].includes(entry.kind)) || leaf && leaf.itemId !== entry.id) return Promise.resolve(Object.freeze({kind: entry.kind, id: entry.id,
      libraryId: entry.libraryId, capabilities: selectionCapabilities(entry.kind, {})}));
    const cached = grants.get(entry);if (cached) return cached.promise;
    let resolve!: Grant['resolve'], reject!: Grant['reject'];
    const promise = new Promise<SelectionItem>((done, failed) => {resolve = done;reject = failed;});
    const grant: Grant = {entry, pending: true, controller: new AbortController(), promise, resolve, reject};
    grants.set(entry, grant);queued.push(grant);pump();return promise;
  };
  const prune = () => {
    if (!current()) return;
    for (const [entry, grant] of grants) if (!source.contains(entry)) {grants.delete(entry);grant.controller.abort();grant.reject(cancelled());}
    if (window?.entries.some(({entry}) => !source.contains(entry))) {clearWindow();publish({rows: Object.freeze([]), resolving: false});}
  };
  const sync = () => {
    if (!current()) return;
    const snapshot = source.getSnapshot();
    if (!model && snapshot.phase === 'ready' && snapshot.revision) {
      try {
        const viewId = 'viewIdentity' in source ? source.viewIdentity() : (() => {const query = source.query();
          return JSON.stringify({libraryId: source.source.libraryId, pivot: source.source.pivot, query: query.query ?? null, sort: query.sort ?? []});})();
        model = new BrowseSelectionModel({scope: {...principal, viewId}, revision: {pageRevision: snapshot.revision}});
        expected = Object.freeze({queryRevision: snapshot.queryRevision, revision: snapshot.revision});
        modelOff = model.subscribe(() => {
          if (closed) return;
          interactions.resetPreview();rangeTarget = undefined;rangeKeys.clear();
          if (playing && !model!.isOperationCurrent(playing.operation)) playing.controller.abort();
          publish();
        });publish();
      } catch (failure) {retire(failure);}
    }
    prune();
  };
  function dispose() {retire();}
  const start = () => {
    if (closed || started) return;
    started = true;
    if (!current()) return;
    signal.addEventListener('abort', dispose, {once: true});
    sourceOff = source.subscribe(sync);collectionOff = source.collection.subscribe(prune);sync();
  };
  const registerWindow = (first: number, last: number): Promise<readonly LibraryPlayRow[]> => {
    start();
    if (!current()) return Promise.reject(cancelled());
    // A visible range can arrive before the first accepted page revision. It
    // grants nothing while waiting; only an actual action can be unavailable.
    if (!model || !expected) return Promise.resolve(Object.freeze([]));
    if (!Number.isSafeInteger(first) || !Number.isSafeInteger(last) || first < 0 || last < first || last - first >= 60) return Promise.reject(unavailable());
    prune();
    const entries = Array.from({length: last - first + 1}, (_, offset) => ({position: first + offset, entry: source.collection.itemAt(first + offset)?.entry}))
      .flatMap(({entry, position}) => entry?.libraryId && source.contains(entry) && selectableSelectionKind(entry.kind, 'catalogue') ? [Object.freeze({entry, position})] : []);
    if (window?.first === first && window.last === last && window.entries.length === entries.length && window.entries.every((value, index) => value.entry === entries[index].entry)) return window.promise!;
    clearWindow();error = undefined;
    const visible = new Set(entries.map(value => value.entry));
    for (const [entry, grant] of grants) if (grant.pending && !visible.has(entry)) {
      grants.delete(entry);grant.controller.abort();grant.reject(cancelled());
    }
    const captured: Window = {first, last, entries, controller: new AbortController(), registrations: []};window = captured;
    captured.promise = Promise.resolve().then(async () => {
      if (!current() || window !== captured) throw cancelled();
      const stop = AbortSignal.any([signal, life.signal, captured.controller.signal]);
      let onAbort!: () => void;
      const aborted = new Promise<never>((_, reject) => {onAbort = () => reject(cancelled());if (stop.aborted) onAbort();else stop.addEventListener('abort', onAbort, {once: true});});
      try {
        const items = await Promise.race([Promise.all(entries.map(({entry}) => read(entry))), aborted]);
        if (!current() || window !== captured || stop.aborted || entries.some(({entry}) => !source.contains(entry))) throw cancelled();
        const ticket = model!.captureContext(), rows: LibraryPlayRow[] = [];
        for (let begin = 0; begin < entries.length;) {
          let end = begin + 1;while (end < entries.length && entries[end].position === entries[end - 1].position + 1) end++;
          const run = 'visible:' + entries[begin].position;
          const registration = model!.registerWindow(ticket, {id: run, sectionId: 'library', sectionOrder: 0, start: entries[begin].position,
            ...(whole ? {queryId: whole.query.id} : {}), items: items.slice(begin, end)});
          if (!registration.ok) throw cancelled();captured.registrations.push(registration.unregister);
          for (let index = begin; index < end; index++) {
            const row = Object.freeze({...entries[index], key: browseSelectionKey(model!.getSnapshot().context.scope, items[index]), capabilities: items[index].capabilities});
            rows.push(row);handles.set(row, {window: captured, run});
          }
          begin = end;
        }
        const result = Object.freeze(rows);publish({rows: result, resolving: false});return result;
      } catch (failure) {
        if (!closed && window === captured) {clearWindow();error = failure;publish({rows: Object.freeze([]), resolving: false});}
        throw failure;
      } finally {stop.removeEventListener('abort', onAbort);}
    });publish({rows: Object.freeze([]), resolving: true, error: undefined});return captured.promise;
  };
  /** Register the server's exact whole-view descriptor and select it. The model's
   * own checked query contract refuses any descriptor that does not name this
   * page's revision; the expected query is rebuilt from the controller's own
   * source-bound descriptor, so a same-revision selector from elsewhere is not
   * accepted. The mounted window is rebound to that same query so its rows can
   * be previewed and excluded; nothing here enumerates the view. */
  const selectWhole = (query: SelectionQuery) => {
    if (!current() || !model) throw cancelled();
    const descriptor: BrowseWholeSelection | undefined = 'wholeSelection' in source ? source.wholeSelection() : undefined;
    if (!descriptor) throw unavailable();
    const exact = wholeViewQuery(descriptor);
    if (JSON.stringify(exact) !== JSON.stringify(query)) throw unavailable();
    const ticket = model.captureContext();
    const registration = model.registerQuery(ticket, exact);
    if (!registration.ok) throw unavailable();
    const change = model.selectWholeQuery(exact.id);
    if (!change.ok) {registration.unregister();throw unavailable();}
    const previous = whole;whole = Object.freeze({query: exact, unregister: registration.unregister});
    if (previous && previous.query.id !== exact.id) previous.unregister();
    // The already mounted window predates the query registration; rebind it from
    // the same resident entries so a loaded card stays a member of the open view.
    if (window) {const {first, last} = window;clearWindow();void registerWindow(first, last).catch(() => {});}
    return change;
  };
  const rowCurrent = (row: LibraryPlayRow) => {
    const handle = handles.get(row);
    return current() && !!model && !!handle && handle.window === window && source.contains(row.entry);
  };
  const setPreview = (row: LibraryPlayRow, enabled: boolean) => {
    // A stale row's leave/cleanup cannot clear another original row's preview.
    const clearOwn = () => {if (rangeTarget === row) clearPreview();};
    if (!enabled) {clearOwn();return;}
    if (!rowCurrent(row) || !model!.getSnapshot().active) {clearOwn();return;}
    if (rangeTarget === row) return;
    const range = model!.previewRange(handles.get(row)!.run, row.key);
    if (!range.ok) {clearOwn();return;}
    rangeTarget = row;rangeKeys = new Set(range.keys);publish();
  };
  return {
    start,
    getSnapshot(): LibraryPlaySelectionSnapshot {
      if (!closed && (signal.aborted || !isCurrent())) retire(undefined, true);
      return state;
    },
    subscribe(listener: () => void) {if (closed) return () => {};listeners.add(listener);return () => {listeners.delete(listener);};},
    registerWindow,
    toggle(row: LibraryPlayRow, range = false) {
      const handle = handles.get(row);
      if (!current() || !model || !handle || handle.window !== window || !source.contains(row.entry)) throw cancelled();
      return model.toggle(handle.run, row.key, range);
    },
    /** A hovered card is a range preview only when the model would include it. */
    preview(row: LibraryPlayRow): boolean {
      if (!current() || !model) return false;
      const handle = handles.get(row);
      if (!handle || handle.window !== window || !source.contains(row.entry)) return false;
      return model.previewRange(handle.run, row.key).ok;
    },
    clearPreview,
    interaction(row: LibraryPlayRow) {
      return interactions.source(row, {current: () => rowCurrent(row), preview: enabled => setPreview(row, enabled)});
    },
    setPreview,
    rangePreviewed(row: LibraryPlayRow): boolean {
      const handle = handles.get(row);
      return current() && !!rangeTarget && !!handle && handle.window === window && source.contains(row.entry) && rangeKeys.has(row.key);
    },
    rangeHint(row: LibraryPlayRow) {
      const handle = handles.get(row);
      if (!current() || !model || !handle || handle.window !== window || !source.contains(row.entry)) return;
      const range = model.previewRange(handle.run, row.key);
      if (!range.ok) return;
      const first = state.rows.find(value => value.key === range.keys[0]), last = state.rows.find(value => value.key === range.keys.at(-1));
      return first && last ? {first: first.entry.title, last: last.entry.title, count: range.keys.length} : undefined;
    },
    /** The model's own membership for this current registered row: explicit picks
     * and whole-view members alike, with the same window/handle currency guards. */
    selected(row: LibraryPlayRow): boolean {
      if (!current() || !model) return false;
      const handle = handles.get(row);
      if (!handle || handle.window !== window || !source.contains(row.entry)) return false;
      return model.isSelected(row.key);
    },
    captureOperation() {return current() ? model?.captureOperation() ?? null : null;},
    isOperationCurrent(operation: SelectionOperation<BrowseSelectionContext>) {return current() && !!model?.isOperationCurrent(operation);},
    keepOnly(kind: SelectionItem['kind']) {if (!current() || !model) throw cancelled();return model.keepOnly(kind);},
    selectWhole,
    selectLoaded() {if (!current() || !model) throw cancelled();return model.selectLoaded('library');},
    clear() {if (!current() || !model) throw cancelled();model.clear();},
    async enqueue(request: Readonly<{items: readonly string[]; placement: 'next' | 'end'; signal: AbortSignal; isCurrent(): boolean}>) {
      if (!current() || !model || !queue || !request.isCurrent()) throw cancelled();
      const operation = model.captureOperation();
      if (!operation || operation.target.mode !== 'explicit' || !model.getSnapshot().summary.every.queue) throw unavailable();
      const items = operation.target.items.map(pick => pick.item);
      if (!items.every((item): item is SelectionItem & CatalogueSelectionIdentity => item.kind !== 'channel' && item.kind !== 'person' && 'libraryId' in item)) throw unavailable();
      if (items.length !== request.items.length || items.some((item, index) => item.id !== request.items[index])) throw unavailable();
      return enqueueCatalogueSelection(queue, items, request.placement, {
        signal: AbortSignal.any([signal, life.signal, request.signal]),
        isCurrent: () => current() && request.isCurrent() && model!.isOperationCurrent(operation),
      });
    },
    async enqueueQuery(request: Readonly<{placement: 'next' | 'end'; signal: AbortSignal; isCurrent(): boolean}>) {
      if (!current() || !model || !queue || !request.isCurrent()) throw cancelled();
      const operation = model.captureOperation();
      if (!operation || operation.target.mode !== 'query' || !model.getSnapshot().summary.every.queue) throw unavailable();
      return enqueueCatalogueQuerySelection(queue, operation.target, request.placement, {
        signal: AbortSignal.any([signal, life.signal, request.signal]),
        isCurrent: () => current() && request.isCurrent() && model!.isOperationCurrent(operation),
      });
    },
    async play(callerSignal: AbortSignal = signal) {
      if (!current()) throw cancelled();
      if (!model || !playback || !model.getSnapshot().summary.every.play) throw unavailable();
      callerSignal.throwIfAborted();
      const operation = model.captureOperation();if (!operation) throw unavailable();
      playing?.controller.abort();
      const captured = Object.freeze({operation, controller: new AbortController()});playing = captured;publish();
      const commandSignal = AbortSignal.any([signal, life.signal, callerSignal, captured.controller.signal]);
      const lifetime = {signal: commandSignal, isCurrent: () => current() && playing === captured && model!.isOperationCurrent(operation)};
      try {return operation.target.mode === 'query'
        ? await playCatalogueQuerySelection(playback, operation.target, lifetime)
        : await playCatalogueSelection(playback, operation.target.displayItems.map(value => value.item), lifetime);}
      finally {if (playing === captured) {playing = undefined;publish();}}
    },
    dispose,
  };
}
export type LibraryPlaySelection = ReturnType<typeof createLibraryPlaySelection>;
