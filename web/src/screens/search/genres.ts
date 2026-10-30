import {readSearchGenres, releaseSearchGenres, searchGenresReadMetadata, searchGenreBrowseRequest, type SearchGenre, type SearchGenrePage} from '@core/search-genres.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {QueueBrowseSelector} from '@core/playback-v1/types.ts';

export type SearchGenreSlot = 'backdrop' | 'covers';
export type SearchGenresSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  genres: readonly SearchGenre[];
  revision?: string;
  nextCursor?: string;
  canContinue: boolean;
  all?: boolean;
  hasGenres?: boolean;
  error?: unknown;
}>;
const empty = (): SearchGenresSnapshot => Object.freeze({phase: 'idle', genres: Object.freeze([]), canContinue: false});
const cancelled = () => new DOMException('Search genres changed', 'AbortError');
const stale = () => Object.assign(new Error('Search genre continuation changed'), {code: 'stale_continuation', retry: 'after_refresh'});

/** One bounded drawn prefix from the canonical genre read. Count/order/art are
 * server facts; only the slot requirement and prefix length are applied here.
 * No constructor work, whole-directory total, ranking or representative choice. */
export class SearchGenresController {
  private api: LibraryContentApi;
  private current: () => boolean;
  private signal: AbortSignal;
  private slot: SearchGenreSlot;
  private limit: number;
  private state = empty();
  private listeners = new Set<() => void>();
  private generation = 0;
  private controller?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  private page?: SearchGenrePage;
  private all = false;
  constructor(input: Readonly<{api: LibraryContentApi; isCurrent: () => boolean; signal: AbortSignal; slot: SearchGenreSlot; limit: number}>) {
    if (!['backdrop', 'covers'].includes(input.slot) || !Number.isSafeInteger(input.limit) || input.limit < 1 || input.limit > 50) throw new Error('Invalid Search genre window');
    this.api = input.api;this.current = input.isCurrent;this.signal = input.signal;this.slot = input.slot;this.limit = input.limit;
    this.signal.addEventListener('abort', this.dispose, {once: true});
    if (this.signal.aborted) this.dispose();
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);return () => {this.listeners.delete(listener);};
  };
  private active() {return !this.disposed && !this.signal.aborted && this.current();}
  private publish(state: SearchGenresSnapshot) {
    this.state = Object.freeze(state);for (const listener of this.listeners) listener();
  }
  /** A raw/cloned genre or a previous read cannot supply a navigation intent. */
  contains = (genre: SearchGenre) => this.active() && this.state.phase === 'ready' && this.state.genres.includes(genre);
  browseTarget(genre: SearchGenre): QueueBrowseSelector {
    if (!this.contains(genre)) throw cancelled();
    const request = searchGenreBrowseRequest(genre.value);
    return Object.freeze({libraryId: '*', pivot: request.pivot, query: request.query, sort: request.sort});
  }
  /** All genres starts its own directory read: a drawable preview does not
   * contain art-less genres and is never a complete directory prefix. */
  showAll = (original: SearchGenresSnapshot): void | Promise<void> => {
    if (!this.active() || this.state !== original || original.phase !== 'ready' || !this.page || this.page.revision !== original.revision) throw cancelled();
    this.cancel(); this.all = true; return this.read();
  };
  /** One page per request. Continue only while the actual cursor remains and
   * this prefix still needs art; never scan the library or invent an end. */
  load = (): Promise<void> => {
    const all = this.all;
    this.cancel();
    this.all = all;
    if (!this.active()) return Promise.reject(cancelled());
    return this.read();
  };
  next = (): Promise<void> => {
    if (!this.active()) return Promise.reject(cancelled());
    if (this.pending) return this.pending;
    if (!this.state.canContinue || !this.state.nextCursor) return Promise.resolve();
    return this.read(this.state.nextCursor);
  };
  private read(cursor?: string): Promise<void> {
    const generation = this.generation, controller = new AbortController(), signal = AbortSignal.any([controller.signal, this.signal]);
    this.controller = controller;
    const before = this.state;
    this.publish({...before, phase: 'loading', canContinue: false, error: undefined});
    const task = (async () => {
      try {
        const page = await readSearchGenres(this.api, this.all ? {limit: 50, ...(cursor ? {cursor} : {})} : {limit: this.limit, preview: this.slot}, signal,
          {previousPage: cursor ? this.page : undefined, isCurrent: () => this.active() && generation === this.generation,
            onFallback: () => {if (this.active() && generation === this.generation) this.publish({phase:'ready',genres:Object.freeze([]),canContinue:false,...(this.all?{all:true}:{})});}});
        if (!this.active() || generation !== this.generation || signal.aborted) throw cancelled();
        const reanchored = searchGenresReadMetadata(page)?.reanchored === true;
        if (!reanchored && cursor && page.revision !== before.revision || page.nextCursor && page.nextCursor === cursor) {releaseSearchGenres(page); throw stale();}
        const prior = cursor && !reanchored && !this.all ? before.genres : [], seen = new Set((cursor && !reanchored ? before.genres : []).map(genre => genre.value));
        if (page.genres.some(genre => seen.has(genre.value))) {releaseSearchGenres(page); throw stale();}
        const eligible = this.all ? page.genres : page.genres.filter(genre => this.slot === 'backdrop' ? !!genre.backdrop : genre.covers.length === 2);
        const genres = Object.freeze([...prior, ...eligible].slice(0, this.all ? 50 : this.limit));
        releaseSearchGenres(this.page); this.page = page;
        this.publish({phase: 'ready', genres, hasGenres: page.hasGenres ?? (page.genres.length > 0 || prior.length > 0), revision: page.revision, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {}),
          ...(this.all ? {all: true} : {}), canContinue: this.all && !!page.nextCursor});
      } catch (error) {
        if (generation === this.generation) {releaseSearchGenres(this.page); this.page = undefined;this.publish({...empty(), phase: this.active() ? 'error' : 'idle', ...(this.active() ? {error} : {})});}
        throw error;
      } finally {
        if (generation === this.generation) {this.controller = undefined;this.pending = undefined;}
      }
    })();
    this.pending = task;return task;
  }
  cancel = () => {
    this.generation++;this.controller?.abort();this.controller = undefined;this.pending = undefined;releaseSearchGenres(this.page);this.page = undefined;this.all = false;
    this.publish(empty());
  };
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true;this.signal.removeEventListener('abort', this.dispose);this.cancel();this.listeners.clear();
  };
}
