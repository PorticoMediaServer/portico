/**
 * Queues. A queue is the server's: built from one source (a show, season, album or artist, a
 * playlist or collection, a library view with its filters, titles named one by one, or a radio),
 * addressed by entry ids, read in runs of entries. The client never holds the whole queue:
 * `QueueView` puts the entries behind a windowed collection, so a long queue costs what is on screen.
 *
 * Making a queue does not start playback: the app then starts the current entry with
 * `POST /v1/playback/sessions` and `queue`. Next, Previous and "this title ended" name the entry
 * the app is on (`fromEntryId`), so one sent again after its answer was lost never skips a
 * title. Edits to the entries carry the queue's revision in `If-Match`; a stale one is 412.
 */
import {createWindowedCollection, type WindowedCollection} from '../collections/windowed.ts';
import {libraryItemQuery} from '../library-browse.ts';
import {PlaybackApiError, call, idempotencyKey, type V1Http} from './http.ts';
import {playbackPath} from './routes.ts';
import {ContractError, parseEntry, parsePreparedNext, parseQueue, parseQueueEntries, parseSession, obj, type PreparedNext, type QueueEntry, type QueueHeader, type Selector, type Session} from './types.ts';

/** The most entries one read asks for. */
export const QUEUE_WINDOW_MAX = 200;

export type QueueOrder = Readonly<{mode: 'source'} | {mode: 'shuffle'; seed?: string}>;
/** Where a play starts: an item (in a playlist, the pressed item); a show's next episode not yet watched. */
export type QueueAnchor = Readonly<{itemId: string; entryId?: string} | {entryId: string} | {position: number}> | 'resume';
export type SegmentInput = Readonly<{source: Selector; order?: QueueOrder; anchor?: QueueAnchor}>;
export type Placement = 'next' | 'end' | Readonly<{after: string}>;

export type CreateResult = Readonly<{queue: QueueHeader; window: readonly QueueEntry[]; session?: Session}>;

export type QueueCreateOptions = Readonly<{
  /** Kept for callers; the server keeps one audio and one video queue per device by itself. */
  retainQueueIds?: readonly string[];
  signal?: AbortSignal;
}>;

/** What the server builds a queue from, for one selector. */
export function queueSource(source: Selector): Record<string, unknown> {
  if ('container' in source) return {container: source.container.id};
  if ('items' in source) return {items: [...source.items.ids]};
  if ('saved' in source) return {list: source.saved.id};
  if ('query' in source) {
    // A library view: the list's own filters (as the list itself reads them) and its order.
    const q = source.query;
    const list = libraryItemQuery({id: q.libraryId}, q.pivot ?? 'movies', q.query ? [q.query] : [], q.sort ?? []);
    const filter = [...(list.genre ? [{field: 'genre', values: [list.genre]}] : []), ...(list.year ? [{field: 'year', values: [list.year]}] : []),
      ...(list.resolution ? [{field: 'resolution', values: [list.resolution]}] : [])];
    return {view: {library: list.library, ...(list.kind ? {kinds: [list.kind]} : {}), ...(filter.length ? {filter} : {}),
      ...(list.watched !== undefined ? {watched: list.watched} : {}), ...(list.favorite !== undefined ? {favorite: list.favorite} : {}),
      ...(list.inList !== undefined ? {inList: list.inList} : {}), ...(list.sort ? {sort: list.sort, ...(list.order === 'desc' ? {desc: true} : {})} : {})}};
  }
  throw new PlaybackApiError(422, 'unsupported_selector', 'never');
}

/** A play's one source: segments of titles named one by one become one list of titles. */
function createBody(segments: readonly SegmentInput[]): Record<string, unknown> {
  if (!segments.length) throw new ContractError('queue source');
  const first = segments[0]!;
  const source = segments.length === 1 ? queueSource(first.source)
    : segments.every(s => 'items' in s.source) ? {items: segments.flatMap(s => 'items' in s.source ? s.source.items.ids : [])} : undefined;
  if (!source) throw new PlaybackApiError(422, 'unsupported_selector', 'never');
  const anchor = first.anchor;
  return {source, ...(first.order?.mode === 'shuffle' ? {shuffle: true} : {}),
    ...(anchor === 'resume' ? {resume: true} : anchor && 'itemId' in anchor ? {startAt: anchor.itemId} : {})};
}

/** Array identity keeps the revision a window was read at, without adding wire fields. */
const windowScopes = new WeakMap<readonly QueueEntry[], Readonly<{queueId: string; revision: string}>>();
export function queueWindowScope(window: readonly QueueEntry[]): Readonly<{queueId: string; revision: string}> | undefined {return windowScopes.get(window);}
function bindWindow(queueId: string, observedRevision: string | undefined, window: readonly QueueEntry[]): readonly QueueEntry[] {
  if (observedRevision && observedRevision.length <= 256 && window.length <= QUEUE_WINDOW_MAX + 1) windowScopes.set(window, Object.freeze({queueId, revision: observedRevision}));
  return window;
}

/** Whether a header came with a current entry the server named, so edits may anchor on it. */
const observedCurrent = new WeakSet<QueueHeader>();
export function queueHasObservedCurrent(header: QueueHeader): boolean {return observedCurrent.has(header);}
function parseObservedQueue(value: unknown): QueueHeader {
  const header = parseQueue(value);
  if (obj(value) && obj(value.current) && typeof value.current.position === 'number'
    && Number.isSafeInteger(value.current.position) && value.current.position >= 0 && value.current.position < header.total
    && typeof value.current.entryId === 'string' && header.current?.entryId === value.current.entryId) observedCurrent.add(header);
  return header;
}

export class QueueClient {
  private http: V1Http;
  private key: () => string;
  constructor(http: V1Http, key: () => string = () => idempotencyKey()) { this.http = http; this.key = key; }

  /** Add a list or a whole selection at the end or next. */
  async batch(id: string, revision: string, source: Selector, placement: 'next' | 'end', signal?: AbortSignal): Promise<CreateResult> {
    const r = await this.command(id, revision, 'POST', playbackPath('POST /v1/queues/{id}/entries', {id}), {source: queueSource(source), placement}, signal);
    return Object.freeze({queue: r.queue, window: await this.window(id, 20, 50, signal)});
  }

  /** Play (or Shuffle, or Resume a show): this device's queue for that medium becomes this one. */
  async create(segments: readonly SegmentInput[], options: QueueCreateOptions = {}): Promise<CreateResult> {
    const r = await call(this.http, {method: 'POST', path: playbackPath('POST /v1/queues'), headers: {'Idempotency-Key': this.key()}, body: createBody(segments), signal: options.signal});
    const b = r.body as {queue?: unknown; window?: unknown};
    const queue = parseObservedQueue(b.queue);
    const window = bindWindow(queue.id, queue.revision, Object.freeze(Array.isArray(b.window) ? b.window.map(parseEntry) : []));
    return Object.freeze({queue, window});
  }

  /** Capture every match on the server. */
  createSelector(source: Selector, options: QueueCreateOptions & {shuffleSeed?: string; shuffle?: boolean} = {}): Promise<CreateResult> {
    return this.create([{source, ...(options.shuffle ? {order: {mode: 'shuffle' as const}} : {})}], options);
  }

  async header(id: string, signal?: AbortSignal): Promise<QueueHeader> {
    return parseObservedQueue((await call(this.http, {method: 'GET', path: playbackPath('GET /v1/queues/{id}', {id}), signal})).body);
  }

  /** The queue always has its current entry once it is made. */
  awaitCurrent(id: string, options: {signal?: AbortSignal} = {}): Readonly<{done: Promise<QueueHeader>; wake: () => void}> {
    return Object.freeze({done: this.header(id, options.signal), wake: () => {}});
  }

  /** A create never leaves its start to be chosen later on this server. */
  static startPending(_result: CreateResult): boolean { return false; }

  /** Entries around the current one (the Up Next panel). */
  async window(id: string, before = 20, after = 50, signal?: AbortSignal): Promise<readonly QueueEntry[]> {
    const b = Math.max(0, Math.min(before, 100)), a = Math.max(0, Math.min(after, 100));
    const page = parseQueueEntries((await call(this.http, {method: 'GET', path: playbackPath('GET /v1/queues/{id}/entries', {id}, new URLSearchParams({around: 'current', before: String(b), after: String(a)})), signal})).body);
    return bindWindow(id, page.revision, page.items);
  }

  /** A run of entries from a position (never more than 200). */
  async entries(id: string, from: number, limit: number, signal?: AbortSignal): Promise<Readonly<{items: readonly QueueEntry[]; total?: number; start?: number; revision?: string; nextCursor?: string}>> {
    const n = Math.max(1, Math.min(limit, QUEUE_WINDOW_MAX));
    const page = parseQueueEntries((await call(this.http, {method: 'GET', path: playbackPath('GET /v1/queues/{id}/entries', {id}, new URLSearchParams({from: String(Math.max(0, from)), limit: String(n)})), signal})).body);
    return Object.freeze({items: bindWindow(id, page.revision, page.items), total: page.total, start: page.start, revision: page.revision});
  }

  private async command(id: string, revision: string | undefined, method: 'POST' | 'PATCH' | 'DELETE', path: string, body?: unknown, signal?: AbortSignal, beforeSend?: () => void): Promise<Readonly<{queue: QueueHeader; session?: Session}>> {
    beforeSend?.();
    const r = await call(this.http, {method, path, headers: revision ? {'If-Match': `"${revision}"`} : {}, body, signal});
    return Object.freeze({queue: parseObservedQueue(r.body)});
  }

  /** Play next / Add to queue / insert after an entry. */
  addSegment(id: string, revision: string, placement: Placement, segment: SegmentInput) {
    return this.command(id, revision, 'POST', playbackPath('POST /v1/queues/{id}/entries', {id}),
      {source: queueSource(segment.source), ...(typeof placement === 'string' ? {placement} : {after: placement.after})});
  }
  /** Removes an entry. A removal sent again after its answer was lost finds the entry gone (404):
   * that is the removal done, and the queue is read as it now stands. */
  async remove(id: string, revision: string, entryId: string, signal?: AbortSignal, beforeSend?: () => void): Promise<Readonly<{queue: QueueHeader; session?: Session}>> {
    try {
      return await this.command(id, revision, 'DELETE', playbackPath('DELETE /v1/queues/{id}/entries/{entryId}', {id, entryId}), undefined, signal, beforeSend);
    } catch (e) {
      if (!(e instanceof PlaybackApiError) || e.status !== 404) throw e;
      return Object.freeze({queue: await this.header(id, signal)});
    }
  }
  move(id: string, revision: string, entryId: string, to: Readonly<{before: string} | {after: string}>, signal?: AbortSignal, beforeSend?: () => void) {
    return this.command(id, revision, 'POST', playbackPath('POST /v1/queues/{id}/entries/{entryId}/move', {id, entryId}), to, signal, beforeSend);
  }
  /** Repeat and shuffle. Shuffle on keeps the current entry and puts the rest in a new order. */
  update(id: string, revision: string, change: Readonly<{repeat?: 'off' | 'one' | 'all'; shuffle?: Readonly<{on: boolean; seed?: string}>}>, signal?: AbortSignal) {
    return this.command(id, revision, 'PATCH', playbackPath('PATCH /v1/queues/{id}', {id}),
      {...(change.repeat ? {repeat: change.repeat} : {}), ...(change.shuffle ? {shuffle: change.shuffle.on} : {})}, signal);
  }
  /**
   * The title ended (`completion`), Next, Previous, or a jump to an entry. `fromEntryId` is the
   * entry the app is on: sent again after its answer was lost, the queue has already moved off
   * it and does not move a second time. "Played to its end" also names the playback that ended
   * (`sessionId`): under repeat one the entry stays the same, and the session tells a resend from a
   * second play. The app then starts the queue's new current entry.
   */
  advance(id: string, fromEntryId: string | undefined, reason: 'completion' | 'next' | 'previous' | 'entry', entryId?: string, signal?: AbortSignal, sessionId?: string) {
    return this.command(id, undefined, 'POST', playbackPath('POST /v1/queues/{id}/advance', {id}),
      Object.freeze({reason, ...(entryId ? {entryId} : {}), ...(fromEntryId ? {fromEntryId} : {}), ...(reason === 'completion' && sessionId ? {sessionId} : {})}), signal);
  }
  /** Gets the next song ready before this one ends, for a join without a gap. */
  async prepareNext(id: string, _revision: string, current: Readonly<{sessionId: string; sessionGeneration?: number}>, signal?: AbortSignal): Promise<PreparedNext> {
    const r = await call(this.http, {method: 'POST', path: playbackPath('POST /v1/queues/{id}/prepare-next', {id}), body: {sessionId: current.sessionId}, signal});
    return parsePreparedNext(r.body);
  }
  /** Makes the prepared next song the session at the audio boundary. Safe to send again with the same token. */
  async commitNext(id: string, token: string, signal?: AbortSignal): Promise<Readonly<{queue: QueueHeader; session: Session}>> {
    const r = await call(this.http, {method: 'POST', path: playbackPath('POST /v1/queues/{id}/commit-next', {id}), body: {token}, signal});
    const b = r.body as {queue?: unknown; session?: unknown} | undefined;
    if (!obj(b) || b.session === undefined) throw new ContractError('committed next');
    return Object.freeze({queue: parseObservedQueue(b.queue), session: parseSession(b.session)});
  }
  /** Save what the queue holds as a playlist. `key` makes a resend the same save. */
  async saveAsPlaylist(id: string, name: string, key: string = this.key(), signal?: AbortSignal): Promise<QueuePlaylistSave> {
    return parseQueuePlaylistSave((await call(this.http, {method: 'POST', path: playbackPath('POST /v1/queues/{id}/playlist', {id}), headers: {'Idempotency-Key': key}, body: {name}, signal})).body);
  }
  async discard(id: string): Promise<void> {
    await call(this.http, {method: 'DELETE', path: playbackPath('DELETE /v1/queues/{id}', {id})}, [204, 200, 404]);
  }
}

/**
 * One queue on screen: the header and a windowed collection of its entries. `queue.updated`
 * events (or a command's result) swap in the new header; entries refetch as a new generation
 * while the old ones stay on screen.
 */
export class QueueView {
  readonly entries: WindowedCollection<QueueEntry>;
  private client: QueueClient;
  private current: QueueHeader;
  private listeners = new Set<() => void>();

  constructor(client: QueueClient, header: QueueHeader, options: {pageSize?: number; maxResidentPages?: number} = {}) {
    this.client = client;
    this.current = header;
    const pageSize = Math.min(options.pageSize ?? 100, QUEUE_WINDOW_MAX);
    this.entries = createWindowedCollection<QueueEntry>({
      pageSize, maxResidentPages: options.maxResidentPages ?? 8, keyOf: e => e.entryId,
      fetchPage: (start, count, signal) => this.fetch(start, count, signal),
    });
  }

  private async fetch(start: number, count: number, signal: AbortSignal) {
    const page = await this.client.entries(this.current.id, start, count, signal);
    return {items: page.items, total: page.total ?? this.current.total};
  }

  get header(): QueueHeader { return this.current; }
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };

  /** Adopt a newer header (a command result or a `queue.updated` re-read). Older revisions are ignored. */
  adopt(header: QueueHeader, entriesChanged = true): void {
    if (header.id !== this.current.id || header.revision === this.current.revision) return;
    this.current = header;
    if (entriesChanged) this.entries.invalidate();
    for (const l of [...this.listeners]) l();
  }

  /** A `queue.updated` event for this queue: re-read the header if the revision moved. */
  async updated(revision?: string, signal?: AbortSignal): Promise<void> {
    if (revision !== undefined && revision === this.current.revision) return;
    this.adopt(await this.client.header(this.current.id, signal));
  }

  /**
   * Run a command with the current revision; on 412 adopt the server's header and let the caller
   * decide again (the returned promise rejects with the 412 so a UI can say "the queue changed").
   */
  async run<T extends {queue: QueueHeader}>(command: (id: string, revision: string) => Promise<T>): Promise<T> {
    try {
      const result = await command(this.current.id, this.current.revision);
      this.adopt(result.queue);
      return result;
    } catch (e) {
      if (e instanceof PlaybackApiError && e.status === 412 && e.current) this.adopt(e.current as QueueHeader);
      throw e;
    }
  }

  dispose() { this.entries.dispose(); this.listeners.clear(); }
}

/** A queue saved as a playlist, with how many entries it holds. */
export type QueuePlaylistSave = Readonly<{playlistId: string; revision: string; entries: number; total?: number; state: 'saving' | 'saved' | 'failed'; errorCode?: string}>;
export function parseQueuePlaylistSave(v: unknown): QueuePlaylistSave {
  if (!obj(v) || typeof v.playlistId !== 'string' || !v.playlistId) throw new ContractError('saved playlist');
  const n = (x: unknown) => (typeof x === 'number' && Number.isSafeInteger(x) && x >= 0 ? x : undefined);
  // An unknown state is read as done: the playlist exists and nothing more can be learned by waiting.
  const state = v.state === 'saving' || v.state === 'failed' ? v.state : 'saved';
  return Object.freeze({playlistId: v.playlistId, revision: typeof v.revision === 'string' ? v.revision : '', entries: n(v.entries) ?? 0, total: n(v.total), state, errorCode: typeof v.errorCode === 'string' && v.errorCode ? v.errorCode : undefined});
}

/** Saves a queue as a playlist. Each answer is passed to `onProgress`. */
export async function saveQueueAsPlaylist(client: Pick<QueueClient, 'saveAsPlaylist'>, queueId: string, name: string, options: Readonly<{key?: string; onProgress?: (save: QueuePlaylistSave) => void; signal?: AbortSignal; waitMs?: (attempt: number) => number}> = {}): Promise<QueuePlaylistSave> {
  const key = options.key ?? idempotencyKey();
  const wait = options.waitMs ?? ((attempt: number) => Math.min(5000, 1000 * 1.5 ** attempt));
  for (let attempt = 0; ; attempt++) {
    const save = await client.saveAsPlaylist(queueId, name, key, options.signal);
    options.onProgress?.(save);
    if (save.state !== 'saving') return save;
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(done, wait(attempt));
      function done() { clearTimeout(timer); options.signal?.removeEventListener('abort', done); options.signal?.aborted ? reject(Object.assign(new Error('Aborted'), {name: 'AbortError'})) : resolve(); }
      options.signal?.addEventListener('abort', done, {once: true});
      if (options.signal?.aborted) done();
    });
  }
}

/** The catalogue message for a save's current state: progress while saving, the result after. */
export function queueSaveMessage(save: QueuePlaylistSave): Readonly<{id: 'player.queueSaving' | 'player.queueSavingProgress' | 'player.queueSaved' | 'player.queueSaveChanged' | 'player.queueSaveFailed'; values?: Readonly<{done: number; total: number}>}> {
  if (save.state === 'saving') return save.total ? {id: 'player.queueSavingProgress', values: {done: Math.min(save.entries, save.total), total: save.total}} : {id: 'player.queueSaving'};
  if (save.state === 'failed') return {id: save.errorCode === 'queue_changed' ? 'player.queueSaveChanged' : 'player.queueSaveFailed'};
  return {id: 'player.queueSaved'};
}
