import {DetailService} from '../../../../../packages/client-core/src/detail';
import {SavedService} from '../../../../../packages/client-core/src/saved';
import {PersonalSavedService, type EntryOutcomes} from '../../../../../packages/client-core/src/personal-saved';
import {appendPlaylistSelection} from '../../../../../packages/client-core/src/playlist-selection';
import {validateContentEntry, type ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {CatalogueSelectionOptions} from '../../../../../packages/client-core/src/catalogue-selection';
import {fetchHomeDestinationPicker, type HomeDestinationPickerPage, type HomeSelectionPickerRow} from '../../../../../packages/client-core/src/home-selection/index';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';

const unavailable = () => Object.assign(new Error('catalogue-selection-unavailable'), {code: 'catalogue-selection-unavailable'});
/** One canonical picker page for the exact captured leaves, query and cursor. */
type PickerPage = Readonly<{kind: 'playlist' | 'collection'; query: string; rows: readonly HomeSelectionPickerRow[];
  nextCursor: string; revision: HomeDestinationPickerPage['revision']; token: object}>;
/** The dialog's own options: the selection lifetime plus the captured Home viewer fence. */
type NativeLoadedDestinationOptions = CatalogueSelectionOptions & Readonly<{viewerFence: string}>;
type PickerRead = Readonly<{kind: 'playlist' | 'collection'; query: string; cursor?: string; history: (string | undefined)[]}>;

/** Bounded direct Saved writes for an explicit captured Search selection.
 * Every service validates its own response fence. Search continuity is checked
 * around I/O, never compared with a Saved or Detail response fence. */
export class NativeLoadedDestinations {
  private readonly options: NativeLoadedDestinationOptions;
  private readonly api: CatalogueSelectionOptions['api'];
  private readonly requestId: CatalogueSelectionOptions['requestId'];
  private readonly entries: readonly ContentEntry[];
  private readonly playlist: SavedService;
  private readonly collection: PersonalSavedService;
  private readonly listeners = new Set<() => void>();
  private readonly stops: (() => void)[];
  private disposed = false;
  private running = false;
  private revision = 0;
  private chosen?: Readonly<{kind: 'playlist' | 'collection'; id: string}>;
  private completed = 0;
  /** The canonical Home destination-picker page this dialog is showing, and the
   * cursor/history that produced it. Rows are only ever the page's own rows. */
  private picker?: PickerPage;
  private pickerCursor?: string;
  private pickerHistory: (string | undefined)[] = [];
  private pickerError?: string;
  /** The exact failed read and an immutable token for it; only that token may retry it,
   * and any successor read start/success retires it. */
  private pickerErrorRead?: Readonly<{read: PickerRead; token: object}>;
  private pickerReads = 0;
  private attemptedCollection = false;
  private collectionItems: readonly string[];
  private readonly collectionReceipts: EntryOutcomes[] = [];
  /** The real pending failures, resolved against the captured entries only. */
  private playlistFailure?: Readonly<{id: string; title: string; code: string}>;

  constructor(options: NativeLoadedDestinationOptions, input: readonly ContentEntry[]) {
    if (typeof options.viewerFence !== 'string' || !options.viewerFence) throw unavailable();
    this.options = options;
    this.entries = Object.freeze(input.map(validateContentEntry));
    if (!this.entries.length || this.entries.length > 100 || this.entries.some(entry => !entry.libraryId
      || !['movie', 'episode', 'song', 'audiobook_file'].includes(entry.kind))
      || new Set(this.entries.map(entry => entry.id)).size !== this.entries.length) throw unavailable();
    this.collectionItems = Object.freeze(this.entries.map(entry => entry.id));
    if (options.signal.aborted || !options.isCurrent()) throw Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
    const api = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.assertCurrent();
      const result = await options.api.request<T>(path, method, body, signal);
      this.assertCurrent(); return result;
    }};
    const requestId = async () => {this.assertCurrent(); const id = await options.requestId(); this.assertCurrent(); return id;};
    this.api = api; this.requestId = requestId;
    this.playlist = new SavedService({api, scope: options.scope, requestId});
    this.collection = new PersonalSavedService(api, options.scope, requestId);
    this.stops = [this.playlist.subscribe(this.changed), this.collection.subscribe(this.changed)];
    options.signal.addEventListener('abort', this.dispose, {once: true});
  }
  private assertCurrent() {
    if (this.disposed || this.options.signal.aborted || !this.options.isCurrent()) {
      if (!this.disposed) this.dispose();
      throw Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
    }
  }
  private changed = () => {this.revision++; for (const listener of [...this.listeners]) listener();};
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.revision;
  /** Confirmed outcomes across every retained receipt: the newest state of each
   * captured id wins, so a retried title counts once and a still-missing one stays
   * failed. Nothing is counted twice and nothing is inferred from absence. */
  private collectionOutcome() {
    const state = new Map<string, Readonly<{state: 'added' | 'unchanged' | 'failed'; code?: string}>>();
    for (const receipt of this.collectionReceipts) {
      for (const id of receipt.added) state.set(id, {state: 'added'});
      for (const id of receipt.unchanged) state.set(id, {state: 'unchanged'});
      for (const failure of receipt.failed) state.set(failure.itemId, {state: 'failed', code: failure.code});
    }
    const added: string[] = [], unchanged: string[] = [], failed: {id: string; title: string; code: string}[] = [];
    for (const entry of this.entries) {
      const outcome = state.get(entry.id);
      if (!outcome) continue;
      if (outcome.state === 'added') added.push(entry.id);
      else if (outcome.state === 'unchanged') unchanged.push(entry.id);
      else failed.push({id: entry.id, title: entry.title, code: outcome.code ?? 'unavailable'});
    }
    return Object.freeze({added: Object.freeze(added), unchanged: Object.freeze(unchanged), failed: Object.freeze(failed)});
  }
  snapshot = () => ({busy: this.running, chosen: this.chosen, completed: this.completed, picker: this.picker, pickerError: this.pickerError,
    pickerCanRetry: !!this.pickerErrorRead, pickerErrorToken: this.pickerErrorRead?.token,
    pickerPrevious: this.pickerHistory.length > 0,
    collectionReceipts: Object.freeze([...this.collectionReceipts]), collectionOutcome: this.collectionOutcome(),
    playlistFailure: this.playlistFailure, playlist: this.playlist.getSnapshot(), collection: this.collection.getSnapshot()});
  /** One page of the canonical Home destination picker: the exact captured leaves,
   * the literal query, one bounded cursor and the captured Home viewer fence. */
  private async readPicker(kind: 'playlist' | 'collection', query: string, cursor: string | undefined, history: (string | undefined)[], origin?: object) {
    this.assertCurrent();
    if (origin && (!this.picker || this.picker.kind !== kind || this.picker.token !== origin)) throw unavailable();
    const attempt = ++this.pickerReads, itemIds = this.entries.map(entry => entry.id);
    // A successor read retires the old error token before it touches the page.
    this.pickerErrorRead = undefined;
    try {
      const page = await fetchHomeDestinationPicker({request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.api.request<T>(path, method, body, signal)},
        {kind, query, ...(cursor ? {cursor} : {}), limit: 40, itemIds}, this.options.scope.serverId, this.options.viewerFence, this.options.signal);
      if (this.disposed || this.options.signal.aborted || !this.options.isCurrent() || attempt !== this.pickerReads) throw unavailable();
      this.picker = Object.freeze({kind, query, rows: page.rows, nextCursor: page.nextCursor, revision: page.revision, token: {}});
      this.pickerCursor = cursor; this.pickerHistory = history; this.pickerError = undefined;
    } catch (error) {
      // A refused or malformed page withholds its rows and commands; the exact read is kept
      // so an explicit read retry can reissue it, never a write retry's chosen destination.
      if (attempt === this.pickerReads && !this.disposed && !this.options.signal.aborted && this.options.isCurrent()) {
        const read: PickerRead = Object.freeze({kind, query, ...(cursor ? {cursor} : {}), history});
        this.picker = undefined; this.pickerError = presentError(error, 'saved', {operation: 'load'}).body;
        this.pickerErrorRead = Object.freeze({read, token: {}}); this.changed();
      }
      throw error;
    }
    this.changed();
  }
  private async eligibility(kind: 'playlist' | 'collection') {
    // Read only these exact leaves, in bounded order; no parent expansion.
    for (const entry of this.entries) {
      this.assertCurrent();
      const detail = new DetailService({api: this.api, scope: this.options.scope, requestId: this.requestId});
      const stop = () => detail.dispose();
      this.options.signal.addEventListener('abort', stop, {once: true});
      try {
        await detail.select({libraryId: entry.libraryId!, itemId: entry.id}); this.assertCurrent();
        const state = detail.getSnapshot(), data = state.data;
        if (state.phase !== 'ready' || !data || data.item.id !== entry.id || data.item.kind !== entry.kind
          || data.item.libraryId !== entry.libraryId || !data.actions.some(action => action.enabled
            && action.id === (kind === 'playlist' ? 'add_to_playlist' : 'add_to_collection'))) throw unavailable();
      } finally {this.options.signal.removeEventListener('abort', stop); detail.dispose();}
    }
  }
  private async run(work: () => Promise<void>) {
    this.assertCurrent(); if (this.running) throw unavailable();
    this.running = true; this.changed();
    try {await work(); this.assertCurrent();}
    finally {this.running = false; this.changed();}
  }
  load = (kind: 'playlist' | 'collection') => this.run(async () => {
    if (this.chosen) throw unavailable();
    await this.eligibility(kind);
    await this.readPicker(kind, '', undefined, []);
  });
  next = (kind: 'playlist' | 'collection', token: object) => this.run(async () => {
    const page = this.picker;
    if (this.chosen || !page || page.kind !== kind || page.token !== token || !page.nextCursor) throw unavailable();
    await this.readPicker(kind, page.query, page.nextCursor, [...this.pickerHistory, this.pickerCursor], token);
  });
  previous = (kind: 'playlist' | 'collection', token: object) => this.run(async () => {
    const page = this.picker;
    if (this.chosen || !page || page.kind !== kind || page.token !== token || !this.pickerHistory.length) throw unavailable();
    await this.readPicker(kind, page.query, this.pickerHistory.at(-1), this.pickerHistory.slice(0, -1), token);
  });
  /** Adds to exactly the row the open page offered. Its revision and grant are the
   * page's; a later Saved read with another revision is refused, never adopted. */
  choose = (kind: 'playlist' | 'collection', id: string, token: object) => this.run(async () => {
    const page = this.picker;
    if (this.chosen || !page || page.kind !== kind || page.token !== token) throw unavailable();
    const offered = page.rows.find(row => row.id === id);
    if (!offered || !offered.canAdd) throw unavailable();
    await this.eligibility(kind);
    if (this.picker !== page) throw unavailable();
    if (kind === 'playlist') {
      await this.playlist.select({view: 'playlist', playlistId: id}); this.assertCurrent();
      const state = this.playlist.getSnapshot();
      if (state.phase !== 'ready' || state.playlist?.id !== id || state.playlist.revision !== offered.revision
        || !state.playlist.actions.includes('add')) throw unavailable();
    } else {
      await this.collection.select({view: 'resource', resourceId: id}); this.assertCurrent();
      const state = this.collection.getSnapshot();
      if (state.loading || state.error || state.resource?.id !== id || state.resource.kind !== 'collection'
        || state.resource.revision !== offered.revision || !state.resource.actions.includes('entries')) throw unavailable();
    }
    this.chosen = {kind, id}; await this.append();
  });
  private async append() {
    this.assertCurrent();
    if (this.chosen?.kind === 'playlist') {
      try {
        this.completed = await appendPlaylistSelection(this.playlist, this.entries.map(entry => entry.id), this.completed,
          count => {this.completed = count; this.changed();}, () => !this.disposed && !this.options.signal.aborted && this.options.isCurrent());
        this.playlistFailure = undefined;
      } catch (error) {
        // The completed prefix is real; the next captured entry is the one that
        // did not go in, and its own error code is the reason. Nothing is invented.
        const entry = this.entries[this.completed];
        this.playlistFailure = entry ? Object.freeze({id: entry.id, title: entry.title, code: (error as {code?: string}).code ?? 'unavailable'}) : undefined;
        throw error;
      }
    } else if (this.chosen?.kind === 'collection') {
      if (!this.collectionItems.length) throw unavailable();
      const state = this.collection.getSnapshot();
      if (state.resource?.id !== this.chosen.id || state.resource.kind !== 'collection' || !state.resource.actions.includes('entries')) throw unavailable();
      if (this.attemptedCollection) {
        if (!state.retryPending) throw unavailable();
        await this.collection.retry();
      } else {
        this.attemptedCollection = true;
        await this.collection.mutate({action: 'entries', addItemIds: this.collectionItems, removeEntryIds: []});
      }
      this.assertCurrent();
      const after = this.collection.getSnapshot();
      if (after.pending || after.retryPending || after.mutationError || !after.result?.entries) throw after.mutationError ?? unavailable();
      const receipt = after.result.entries;
      if (!this.collectionReceipts.includes(receipt)) this.collectionReceipts.push(receipt);
      // Retry keeps only the ids this receipt left unresolved.
      const missing = new Set(receipt.failed.map(failure => failure.itemId));
      this.collectionItems = Object.freeze(this.collectionItems.filter(id => missing.has(id)));
      this.attemptedCollection = false;
    } else throw unavailable();
  }
  /** A new playlist holding exactly the captured leaves, in their picked order. */
  create = (name: string) => this.run(async () => {
    if (this.chosen) throw unavailable();
    const trimmed = name.trim().slice(0, 200);
    if (!trimmed) throw unavailable();
    await this.eligibility('playlist');
    await this.playlist.mutate({action: 'create', name: trimmed, itemIds: this.collectionItems});
    this.assertCurrent();
    const state = this.playlist.getSnapshot();
    if (state.mutationError || state.pending.length) throw state.mutationError ?? unavailable();
    const id = state.lastReceipt?.playlistId;
    if (!id) throw unavailable();
    this.chosen = {kind: 'playlist', id};
    this.completed = this.collectionItems.length;
    this.changed();
  });
  retry = () => this.run(async () => {
    if (!this.chosen) throw unavailable();
    await this.eligibility(this.chosen.kind); await this.append();
  });
  /** Reissues only the exact failed read this token named; a newer page, a newer error
   * or a chosen destination refuses. It never writes. */
  retryPicker = (expected: object) => this.run(async () => {
    const failed = this.pickerErrorRead;
    if (this.chosen || !failed || failed.token !== expected) throw unavailable();
    await this.readPicker(failed.read.kind, failed.read.query, failed.read.cursor, failed.read.history);
  });
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true; this.options.signal.removeEventListener('abort', this.dispose);
    this.stops.forEach(stop => stop());
    this.playlist.dispose(); this.collection.dispose(); this.listeners.clear();
  };
}
