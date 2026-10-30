import {PersonalSavedService, type PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {HttpLocalApi} from '../../../../../packages/client-core/src';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {nativeReadingCollectionCapability, type NativeReadingCollectionKind} from './read-collection-capability';

const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
const unavailable = () => Object.assign(new Error('reading_collection_unavailable'), {code: 'reading_collection_unavailable'});

/** One bounded Reading collection picker. Eligibility comes from the exact
 * item's enabled `add_to_collection` Detail action; the write is the existing
 * `PUT /v1/saved-resources/{id}/entries` through `PersonalSavedService` with the
 * selected collection's own revision and one generated operation id. */
export type NativeReadingCollectionState = Readonly<{
  phase: 'loading' | 'ready' | 'error';
  collections: readonly PersonalResource[];
  nextCursor: string;
  loadingMore: boolean;
  error?: unknown;
  busy?: string;
  added?: string;
  unchanged?: string;
  failure?: unknown;
  retryPending: boolean;
}>;

export type NativeReadingCollectionOptions = Readonly<{api: HttpLocalApi; scope: Readonly<{serverId: string; viewerId: string}>;
  requestId: () => Promise<string>; signal: AbortSignal; isCurrent: () => boolean;
  itemId: string; libraryId: string; kind: NativeReadingCollectionKind}>;

/** The exact accepted list row: its id, revision and viewer fence are the offer
 * the viewer pressed, never a later revision found by id. */
type Offer = Readonly<{id: string; revision: number; viewerFence: string}>;

export class NativeReadingCollectionPicker {
  private readonly options: NativeReadingCollectionOptions;
  private readonly collections: PersonalSavedService;
  private readonly collection: PersonalSavedService;
  private readonly stops: (() => void)[];
  private readonly listeners = new Set<() => void>();
  private disposed = false;
  private revision = 0;
  private phase: NativeReadingCollectionState['phase'] = 'loading';
  private loadingMore = false;
  private error: unknown;
  private busy?: string;
  private added?: string;
  private unchanged?: string;
  private failure: unknown;
  private continuationFailure: unknown;
  private retryPending = false;
  private target?: Offer;

  constructor(options: NativeReadingCollectionOptions) {
    if (!options.itemId || !options.libraryId || !options.scope.serverId || !options.scope.viewerId
      || (options.kind !== 'ebook' && options.kind !== 'comic')) throw unavailable();
    if (options.signal.aborted || !options.isCurrent()) throw cancelled();
    this.options = Object.freeze({...options, scope: Object.freeze({...options.scope})});
    const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.assertCurrent();
      // The service's own bounded signal never replaces the picker's lifetime.
      const scoped = linkNativeAbortSignals([this.options.signal, signal]);
      try {
        const result = await this.options.api.request<T>(path, method, body, scoped.signal);
        this.assertCurrent(); return result;
      } finally {scoped.dispose();}
    }};
    const requestId = async () => {this.assertCurrent(); const id = await this.options.requestId(); this.assertCurrent(); return id;};
    this.collections = new PersonalSavedService(api, options.scope, requestId);
    this.collection = new PersonalSavedService(api, options.scope, requestId);
    this.stops = [this.collections.subscribe(this.changed), this.collection.subscribe(this.changed)];
    options.signal.addEventListener('abort', this.dispose, {once: true});
  }
  private assertCurrent() {
    if (this.disposed || this.options.signal.aborted || !this.options.isCurrent()) {
      if (!this.disposed) this.dispose();
      throw cancelled();
    }
  }
  private changed = () => {this.revision++; for (const listener of [...this.listeners]) listener();};
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.revision;
  snapshot = (): NativeReadingCollectionState => {
    const listed = this.collections.getSnapshot();
    return Object.freeze({
      phase: this.phase, collections: listed.resources.filter(resource => resource.kind === 'collection' && resource.actions.includes('entries')),
      nextCursor: listed.nextCursor,
      loadingMore: this.loadingMore, ...(this.error !== undefined ? {error: this.error} : {}),
      ...(this.busy ? {busy: this.busy} : {}), ...(this.added ? {added: this.added} : {}),
      ...(this.unchanged ? {unchanged: this.unchanged} : {}),
      ...(this.failure !== undefined ? {failure: this.failure} : this.continuationFailure !== undefined ? {failure: this.continuationFailure} : {}),
      retryPending: this.retryPending,
    });
  };
  /** The exact item must still offer the enabled action for its own Reading
   * kind before any collection list is read; the list is the real first page. */
  load = async () => {
    if (this.disposed || this.phase !== 'loading' || this.error !== undefined) return;
    try {
      const offered = await nativeReadingCollectionCapability({api: this.options.api, scope: this.options.scope,
        requestId: this.options.requestId, signal: this.options.signal, isCurrent: this.options.isCurrent,
        itemId: this.options.itemId, libraryId: this.options.libraryId});
      this.assertCurrent();
      // The re-read must admit the same producer kind the menu cached.
      if (offered !== this.options.kind) throw unavailable();
      await this.collections.select({view: 'collections'});
      this.assertCurrent();
      const listed = this.collections.getSnapshot();
      if (listed.loading || listed.error) throw listed.error ?? unavailable();
      this.failure = undefined; this.continuationFailure = undefined; this.phase = 'ready'; this.changed();
    } catch (error) {
      if (this.disposed || this.options.signal.aborted || !this.options.isCurrent()) return;
      this.error = error; this.phase = 'error'; this.changed();
    }
  };
  retryLoad = async () => {
    if (this.disposed || this.busy) return;
    this.assertCurrent();
    // A retained entries command is settled only by Retry, never by a reload.
    if (this.retryPending || this.collection.getSnapshot().retryPending) return;
    this.error = undefined; this.phase = 'loading'; this.changed();
    await this.load();
  };
  /** Each explicit Show more reads exactly one real next page. A continuation
   * that returns the same nonempty cursor and rows is a stalled read, never a
   * proven end: the real cursor stays available and the failure is reported so
   * the same cursor can be retried explicitly or after a reload. */
  more = async () => {
    if (this.disposed || this.phase !== 'ready' || this.loadingMore) return;
    this.assertCurrent();
    const listed = this.collections.getSnapshot();
    if (!listed.nextCursor) return;
    const cursor = listed.nextCursor, count = listed.resources.length;
    this.loadingMore = true; this.continuationFailure = undefined; this.changed();
    try {
      await this.collections.next(); this.assertCurrent();
      const next = this.collections.getSnapshot();
      if (next.error) throw next.error;
      if (next.nextCursor === cursor && next.resources.length === count) {
        throw Object.assign(new Error('reading_collection_continuation_stalled'), {code: 'reading_collection_continuation_stalled'});
      }
    } catch (error) {if (!this.disposed && this.options.isCurrent()) this.continuationFailure = error;}
    finally {this.loadingMore = false; this.changed();}
  };
  /** Add exactly this Reading item to the exact offered collection. The row's
   * own revision and viewer fence must still be what the opened collection
   * returns; a later revision is refused, never adopted. */
  choose = async (offer: PersonalResource) => {
    if (this.disposed || this.phase !== 'ready' || this.busy) return;
    this.assertCurrent();
    // A retained uncertain command owns the collection service until Retry.
    if (this.retryPending || this.collection.getSnapshot().retryPending) return;
    // Identity against the live list: any refresh makes a retained row stale.
    if (!this.collections.getSnapshot().resources.includes(offer) || offer.kind !== 'collection' || !offer.actions.includes('entries')) return;
    const target: Offer = Object.freeze({id: offer.id, revision: offer.revision, viewerFence: offer.viewerFence});
    this.busy = offer.id; this.target = target; this.failure = undefined; this.continuationFailure = undefined; this.added = undefined; this.unchanged = undefined; this.retryPending = false;
    this.changed();
    try {
      await this.collection.select({view: 'resource', resourceId: target.id}); this.assertCurrent();
      const opened = this.collection.getSnapshot(), resource = opened.resource;
      if (opened.loading || opened.error || !resource || resource.id !== target.id || resource.kind !== 'collection'
        || resource.revision !== target.revision || resource.viewerFence !== target.viewerFence
        || !resource.actions.includes('entries')) throw unavailable();
      const written = await this.collection.mutate({action: 'entries', addItemIds: [this.options.itemId]});
      this.assertCurrent();
      if (!written) throw this.collection.getSnapshot().mutationError ?? unavailable();
      this.settle(target.id);
    } catch (error) {
      if (!this.disposed && this.options.isCurrent()) {
        this.failure = error;
        this.retryPending = this.collection.getSnapshot().retryPending;
      }
    } finally {this.busy = undefined; this.changed();}
  };
  /** Retry the retained command byte-for-byte against the original collection;
   * it never selects or re-targets another resource. */
  retry = async () => {
    if (this.disposed || this.busy || !this.retryPending || !this.target) return;
    this.assertCurrent();
    const target = this.target, opened = this.collection.getSnapshot(), resource = opened.resource;
    if (!opened.retryPending || !resource || resource.id !== target.id || resource.viewerFence !== target.viewerFence) return;
    this.busy = target.id; this.failure = undefined; this.changed();
    try {
      const written = await this.collection.retry(); this.assertCurrent();
      if (!written) throw this.collection.getSnapshot().mutationError ?? unavailable();
      this.settle(target.id);
      this.retryPending = false;
    } catch (error) {
      if (!this.disposed && this.options.isCurrent()) {
        this.failure = error;
        this.retryPending = this.collection.getSnapshot().retryPending;
      }
    } finally {this.busy = undefined; this.changed();}
  };
  private settle(resourceId: string) {
    const state = this.collection.getSnapshot();
    if (state.pending || state.mutationError || !state.result?.entries) throw state.mutationError ?? unavailable();
    const receipt = state.result.entries;
    if (receipt.added.includes(this.options.itemId)) {this.added = resourceId; this.retryPending = false; return;}
    if (receipt.unchanged.includes(this.options.itemId)) {this.unchanged = resourceId; this.retryPending = false; return;}
    const refused = receipt.failed.find(value => value.itemId === this.options.itemId);
    throw Object.assign(new Error(refused?.code ?? 'unavailable'), {code: refused?.code ?? 'unavailable'});
  }
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true;
    this.options.signal.removeEventListener('abort', this.dispose);
    for (const stop of this.stops) stop();
    this.collections.dispose(); this.collection.dispose(); this.listeners.clear();
  };
}
