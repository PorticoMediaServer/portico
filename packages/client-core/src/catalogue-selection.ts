import {validateHomeEntry} from './home.ts';
import {DetailService, type DetailApi, type DetailScope} from './detail.ts';
import {MediaSelectionService, selectionItem} from './media-selection.ts';
import type {ContentEntry} from './library-content.ts';
import type {SegmentInput} from './playback-v1/queue.ts';
import type {QueueHeader} from './playback-v1/types.ts';
import type {V1QueuePlayer} from './playback-v1/v1-queue-player.ts';
import type {SelectionCapabilities, SelectionItem} from './selection-model/types.ts';
import {checkedItem} from './selection-model/validation.ts';
import {selectionCapabilities} from './selection-model/capabilities.ts';

/** The caller captures its own source continuity and principal/route authority.
 * Search/Saved fences and opaque Browse revisions remain in their own domains. */
export type CatalogueSelectionLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
export type CatalogueSelectionOptions = Readonly<{api: DetailApi; scope: DetailScope; requestId(): Promise<string>}> & CatalogueSelectionLifetime;
/** Actual caller execution ports, never role-derived grants. Missing means unavailable. */
export type CatalogueSelectionPorts = Readonly<Partial<Pick<SelectionCapabilities, 'play' | 'queue' | 'myList' | 'favorite' | 'watched' | 'edit' | 'delete' | 'playlist' | 'collection'>>>;
const unavailable = () => Object.assign(new Error('catalogue-selection-unavailable'), {code: 'catalogue-selection-unavailable'});
function requireCurrent(lifetime: CatalogueSelectionLifetime): void {
  if (lifetime.signal.aborted || !lifetime.isCurrent()) throw Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
}
function capturedApi(api: DetailApi, lifetime: CatalogueSelectionLifetime): DetailApi {
  return {acknowledgeUserMutation: api.acknowledgeUserMutation ? async key => {
    requireCurrent(lifetime);
    await api.acknowledgeUserMutation!(key);
    requireCurrent(lifetime);
  } : undefined, request: async<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    requireCurrent(lifetime);
    const value = await api.request<T>(path, method, body, signal);
    requireCurrent(lifetime);
    return value;
  }};
}

/** Read only a selected loaded leaf's real Detail actions. Its own fence stays
 * in Detail: no comparison with Search/Saved/Home fences or Browse revisions.
 * Aggregate parent actions need their own authority; no first-child expansion. */
export async function readCatalogueSelectionItem(options: CatalogueSelectionOptions, input: ContentEntry, ports: CatalogueSelectionPorts = {}): Promise<SelectionItem> {
  const {api, scope, requestId, signal, isCurrent} = options, lifetime = {signal, isCurrent};
  const availablePorts = Object.freeze({...ports});
  requireCurrent(lifetime);
  const entry = validateHomeEntry(input), target = selectionItem(entry);
  if (!target && entry.libraryId && ['show', 'season', 'album', 'artist', 'book', 'collection'].includes(entry.kind)) {
    const offered = entry.homeActions?.selection;
    // Canonical displayed-container selection offers describe server-expanded
    // membership. They never borrow an entry.playback/resume child identity.
    return checkedItem({kind: entry.kind, id: entry.id, libraryId: entry.libraryId, capabilities: selectionCapabilities(entry.kind, {
      play: availablePorts.play === true && offered?.play === true,
      queue: availablePorts.queue === true && offered?.queue === true,
      myList: availablePorts.myList === true && offered?.myList === true,
      favorite: availablePorts.favorite === true && offered?.favorite === true,
      watched: availablePorts.watched === true && offered?.watched === true,
      edit: availablePorts.edit === true && offered?.edit === true,
      delete: availablePorts.delete === true && offered?.delete === true,
      playlist: availablePorts.playlist === true && offered?.playlist === true,
      collection: availablePorts.collection === true && offered?.collection === true,
    })});
  }
  if (!target || target.itemId !== entry.id) throw unavailable();
  const service = new DetailService({api: capturedApi(api, lifetime), scope: Object.freeze({...scope}), requestId});
  const stop = () => service.dispose();
  signal.addEventListener('abort', stop, {once: true});
  try {
    await service.select(target);requireCurrent(lifetime);
    const state = service.getSnapshot(), data = state.data;
    if (state.phase !== 'ready' || !data) throw state.error ?? unavailable();
    if (data.item.id !== entry.id || data.item.libraryId !== target.libraryId || data.item.kind !== entry.kind) throw unavailable();
    const enabled = (id: string) => data.actions.some(action => action.id === id && action.enabled);
    const play = data.actions.some(action => action.enabled && (action.id === 'play' || action.id === 'resume') && action.playback?.itemId === entry.id);
    return checkedItem({kind: entry.kind, id: entry.id, libraryId: target.libraryId, capabilities: selectionCapabilities(entry.kind, {
      play: availablePorts.play === true && data.item.available === true && play,
      queue: availablePorts.queue === true && data.item.available === true && data.actions.some(action =>
        action.id === 'enqueue' && action.enabled && action.playback?.itemId === entry.id),
      playlist: availablePorts.playlist === true && enabled('add_to_playlist'),
      collection: availablePorts.collection === true && enabled('add_to_collection'),
      delete: availablePorts.delete === true && enabled('delete'),
      edit: availablePorts.edit === true && enabled('metadata_edit'),
      myList: availablePorts.myList === true && enabled('watchlist'),
      favorite: availablePorts.favorite === true && enabled('favorite'),
      watched: availablePorts.watched === true && enabled('watched'),
    })});
  } finally {signal.removeEventListener('abort', stop);service.dispose();}
}

/** Loaded leaf personal actions use the existing revision/idempotency owner.
 * Retain this binding for retries; aggregates stay unavailable, never expanded
 * into a different parent My List/Favorite intent. Construction performs no I/O. */
export function createCataloguePersonalSelection(options: CatalogueSelectionOptions): Readonly<{service: MediaSelectionService; dispose(): void}> {
  const {api: sourceApi, requestId, signal, isCurrent} = options;
  const scope = Object.freeze({...options.scope});
  let disposed = false;
  const lifetime = {signal, isCurrent: () => !disposed && isCurrent()};
  requireCurrent(lifetime);
  const api = capturedApi(sourceApi, lifetime);
  const service = new MediaSelectionService({api, scope, requestId: async () => {
    requireCurrent(lifetime);
    const id = await requestId();
    requireCurrent(lifetime);
    return id;
  }});
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    signal.removeEventListener('abort', dispose);
    service.dispose();
  };
  signal.addEventListener('abort', dispose, {once: true});
  return Object.freeze({service, dispose});
}

/** Input is the bounded explicit selection, already in display order for Play
 * or pick order for queue actions. Membership expands only on the server. */
export function catalogueSelectionSegments(items: readonly SelectionItem[], capability: 'play' | 'queue' = 'play'): readonly SegmentInput[] {
  if (!items.length || !['play', 'queue'].includes(capability)) throw unavailable();
  const segments: SegmentInput[] = [], seen = new Set<string>();
  for (const raw of items) {
    const item = checkedItem(raw);
    if (!item.capabilities[capability] || item.kind === 'person' || item.kind === 'channel') throw unavailable();
    const key = JSON.stringify([item.libraryId, item.kind, item.id]);
    if (seen.has(key)) throw unavailable();
    seen.add(key);
    if (['show', 'season', 'album', 'artist', 'book', 'collection', 'playlist'].includes(item.kind)) {
      segments.push(Object.freeze({source: Object.freeze({container: Object.freeze({kind: item.kind, id: item.id})})}));
    } else if (['movie', 'episode', 'song', 'audiobook_file', 'extra'].includes(item.kind)) {
      const previous = segments.at(-1);
      if (previous && 'items' in previous.source && previous.source.items.ids.length < 500) {
        segments[segments.length - 1] = Object.freeze({source: Object.freeze({items: Object.freeze({ids: Object.freeze([...previous.source.items.ids, item.id])})})});
      } else segments.push(Object.freeze({source: Object.freeze({items: Object.freeze({ids: Object.freeze([item.id])})})}));
    } else throw unavailable();
  }
  return Object.freeze(segments);
}

/** The real player retains this original lifetime through queue POST/adoption.
 * A QueueHeader is the receipt; no visible-page queue or fake success. */
export async function playCatalogueSelection(player: Pick<V1QueuePlayer, 'playSegments'>, items: readonly SelectionItem[], lifetime: CatalogueSelectionLifetime): Promise<QueueHeader> {
  const captured = Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent});
  requireCurrent(captured);
  const segments = catalogueSelectionSegments(items);
  const result = await player.playSegments(segments, captured);
  requireCurrent(captured);
  return result;
}

/** An authority-local queue port, supplied only while the caller controls its
 * current queue/group. The same lifetime must survive the actual append. */
export type CatalogueSelectionQueuePort = Readonly<{
  enqueueSegments(segments: readonly SegmentInput[], placement: 'next' | 'end', lifetime: CatalogueSelectionLifetime): Promise<QueueHeader>;
}>;

/** Explicit bounded picks in pick order. Expansion stays on the server; each
 * leaf has its Detail enqueue grant and each parent its canonical selection grant. */
export async function enqueueCatalogueSelection(player: CatalogueSelectionQueuePort, items: readonly SelectionItem[],
  placement: 'next' | 'end', lifetime: CatalogueSelectionLifetime): Promise<QueueHeader> {
  const captured = Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent});
  requireCurrent(captured);
  if (!['next', 'end'].includes(placement) || !items.length || items.length > 256
    || items.some(item => !['movie', 'episode', 'song', 'audiobook_file', 'show', 'season', 'album', 'artist', 'book', 'collection'].includes(item.kind))) throw unavailable();
  const segments = catalogueSelectionSegments(items, 'queue');
  const result = await player.enqueueSegments(segments, placement, captured);
  requireCurrent(captured);
  return result;
}
