import {readSearchGenres, releaseSearchGenres, searchGenresReadMetadata, type SearchGenre, type SearchGenrePage, type SearchGenreRequest} from '../../../../../packages/client-core/src/search-genres';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import type {NativeSearchExplorePort, NativeSearchExploreSnapshot, NativeSearchGenre} from './types';

export type NativeSearchGenresSnapshot = NativeSearchExploreSnapshot & Readonly<{revision?: string; backdropRevision?: string}>;
const initial: NativeSearchGenresSnapshot = Object.freeze({phase: 'idle', genres: [], backdropGenres: [],
  initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'});
const nativeGenre = (genre: SearchGenre): NativeSearchGenre => Object.freeze({id: genre.value, name: genre.label, count: genre.count,
  ...(genre.backdrop ? {backdropPath: genre.backdrop.path} : {}), coverPaths: Object.freeze(genre.covers.map(art => art.path))});

/** Each authored tile slot owns its server prefix. Preview never walks the
 * ordinary directory, and the two phone prefixes never rerank each other. */
export class NativeSearchExplore implements NativeSearchExplorePort {
  private readonly options: Readonly<{api: LibraryContentApi; current: () => boolean;
    surface: 'phone' | 'tv'; openGenre: (genre: NativeSearchGenre) => void}>;
  private state: NativeSearchGenresSnapshot = initial;
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private pending?: Promise<void>;
  private active = true;
  private started = false;
  private disposed = false;
  constructor(options: NativeSearchExplore['options']) {this.options = options;}
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = (): NativeSearchGenresSnapshot => {this.current(); return this.state;};
  subscribe = (listener: () => void): (() => void) => {if (!this.current()) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeSearchGenresSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  setActive = (active: boolean): void => {
    if (!this.current() || this.active === active) return;
    this.active = active;
    if (!active) {
      this.controller?.abort(); this.pending = undefined;
      if (this.state.phase === 'loading') this.publish({phase: 'idle'});
    } else if (this.started && this.state.phase === 'idle') void this.load();
  };
  load = (): Promise<void> => {
    if (!this.current() || !this.active || this.state.phase === 'ready') return Promise.resolve();
    if (this.pending) return this.pending;
    this.started = true;
    const controller = new AbortController(); this.controller = controller;
    const task = Promise.resolve().then(async () => {
      if (!this.current() || controller.signal.aborted) return;
      this.publish({phase: 'loading', error: undefined});
      const request: SearchGenreRequest = {preview: this.options.surface === 'tv' ? 'backdrop' : 'covers', limit: this.options.surface === 'tv' ? 4 : 6};
      const showFallback = () => {if (this.current() && this.active && !controller.signal.aborted) this.publish({phase:'ready',genres:[],backdropGenres:[],hasGenres:undefined});};
      const primary = await readSearchGenres(this.options.api, request, controller.signal,{isCurrent:()=>this.current()&&this.active&&!controller.signal.aborted,onFallback:showFallback});
      if (!this.current() || controller.signal.aborted) return;
      const backdrop = this.options.surface === 'tv' ? primary : await readSearchGenres(this.options.api, {preview: 'backdrop', limit: 4}, controller.signal,{isCurrent:()=>this.current()&&this.active&&!controller.signal.aborted,onFallback:showFallback});
      if (!this.current() || controller.signal.aborted) return;
      // The two independent slot reads may accept different COMPLETE content
      // bases during a scan. Each response proves its own live authority.
      this.publish({phase: 'ready', revision: primary.revision, backdropRevision: backdrop.revision, hasGenres: primary.hasGenres || backdrop.hasGenres,
        genres: Object.freeze(primary.genres.map(nativeGenre)), backdropGenres: Object.freeze(backdrop.genres.map(nativeGenre))});
    }).catch(error => {if (!controller.signal.aborted) this.publish({phase: 'error', error});})
      .finally(() => {if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  openGenre = (genre: NativeSearchGenre): void => {
    if (this.current() && this.active && (this.state.genres.includes(genre) || this.state.backdropGenres?.includes(genre))) this.options.openGenre(genre);
  };
  dispose = (): void => {if (this.disposed) return; this.disposed = true; this.controller?.abort(); this.pending = undefined; this.state = initial; this.listeners.clear();};
}

export type NativeSearchGenreListSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error'; genres: readonly NativeSearchGenre[];
  revision?: string; nextCursor?: string; previousCursor?: string; error?: unknown;
}>;
type GenreListPage = Readonly<{genres: readonly NativeSearchGenre[]; lease: SearchGenrePage; cursor?: string; nextCursor?: string; previousCursor?: string; reloadCursor?: string}>;
const emptyList: NativeSearchGenreListSnapshot = Object.freeze({phase: 'idle', genres: []});

/** The directory owns a bounded resident window, independent of eligible
 * previews. Only resident pages and their keyset anchors are retained. */
export class NativeSearchGenreList {
  private state: NativeSearchGenreListSnapshot = emptyList;
  private pages: readonly GenreListPage[] = [];
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private pending?: Promise<void>;
  private active = true;
  private started = false;
  private disposed = false;
  private visibleIds: readonly string[] = [];
  private focusedId?: string;
  private retry?: Readonly<{direction: 'initial' | 'next' | 'previous' | 'reload'; cursor?: string}>;
  private resumeReload = false;
  constructor(private readonly options: Readonly<{api: LibraryContentApi; current: () => boolean; openGenre: (genre: NativeSearchGenre) => void}>) {}
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = (): NativeSearchGenreListSnapshot => {this.current(); return this.state;};
  subscribe = (listener: () => void): (() => void) => {if (!this.current()) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeSearchGenreListSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  // Retained navigation keeps ready leases; app background retires them.
  setActive = (active: boolean, releaseReady = true): void => {
    if (!this.current()) return;
    if (!active && releaseReady) {
      for (const page of this.pages) releaseSearchGenres(page.lease);
      this.resumeReload = this.pages.length > 0;
    }
    if (this.active === active) return;
    this.active = active;
    if (!active) {
      this.controller?.abort(); this.pending = undefined;
      if (this.state.phase === 'loading') this.publish({phase: this.pages.length ? 'ready' : 'idle'});
    } else if (this.resumeReload) {this.resumeReload = false; void this.reload();}
    else if (this.started && this.state.phase === 'idle') void this.load();
  };
  setVisible = (ids: readonly string[]): void => {if (this.current()) this.visibleIds = ids.filter(id => this.state.genres.some(genre => genre.id === id));};
  setFocused = (id: string, focused: boolean): void => {if (this.current() && (focused || this.focusedId === id)) this.focusedId = focused ? id : undefined;};
  private protected(page: GenreListPage): boolean {
    return page.genres.some(genre => genre.id === this.focusedId || this.visibleIds.includes(genre.id));
  }
  private read(direction: 'initial' | 'next' | 'previous' | 'reload', cursor?: string): Promise<void> {
    if (!this.current() || !this.active) return Promise.resolve();
    if (this.pending) return this.pending;
    // Never evict a visible/focused row to make room for a speculative edge.
    if (this.pages.length === 3 && (direction === 'next' && this.protected(this.pages[0]) || direction === 'previous' && this.protected(this.pages[2]))) return Promise.resolve();
    this.started = true; this.retry = {direction, cursor};
    const controller = new AbortController(); this.controller = controller;
    let returned: SearchGenrePage | undefined, adopted = false;
    const task = Promise.resolve().then(async () => {
      if (!this.current() || controller.signal.aborted) return;
      this.publish({phase: 'loading', error: undefined});
      if ((direction === 'next' || direction === 'previous') && this.pages.some(page => page.cursor === cursor)) throw Object.assign(new Error('invalid_response'), {code: 'invalid_response'});
      const result = await readSearchGenres(this.options.api, {limit: 50, ...(cursor ? {cursor} : {})}, controller.signal,
        {isCurrent: () => this.current() && this.active && !controller.signal.aborted,
          onFallback:()=>{if(this.current()&&this.active&&!controller.signal.aborted){for(const page of this.pages)releaseSearchGenres(page.lease);this.pages=[];this.publish({phase:'ready',genres:[],nextCursor:undefined,previousCursor:undefined});}}});
      returned = result;
      if (!this.current() || controller.signal.aborted) return;
      const reanchored = searchGenresReadMetadata(result)?.reanchored === true;
      if (!reanchored && direction !== 'initial' && result.revision !== this.state.revision || result.nextCursor && result.nextCursor === cursor
        || result.previousCursor && result.previousCursor === cursor) throw Object.assign(new Error('invalid_response'), {code: 'invalid_response'});
      const existing = new Map(this.state.genres.map(genre => [genre.id, genre]));
      const genres = Object.freeze(result.genres.map(value => {
        const previous = existing.get(value.value);
        // Keep the exact row object when an inclusive reload returns unchanged
        // metadata, so retained actions and mounted rows keep their identity.
        return previous && previous.name === value.label && previous.count === value.count && previous.backdropPath === value.backdrop?.path
          && previous.coverPaths.length === value.covers.length && previous.coverPaths.every((path, index) => path === value.covers[index].path) ? previous : nativeGenre(value);
      }));
      const page: GenreListPage = Object.freeze({genres, lease: result, cursor: reanchored ? undefined : cursor, nextCursor: result.nextCursor, previousCursor: result.previousCursor, reloadCursor: result.reloadCursor});
      let pages = reanchored ? [page] : direction === 'next' ? [...this.pages, page] : direction === 'previous' ? [page, ...this.pages] : [page];
      if (pages.length > 3) {
        pages = direction === 'next' ? pages.slice(1) : pages.slice(0, 3);
        // An old next-only server must not silently strand the evicted prefix.
        if (direction === 'next' && !pages[0].previousCursor) throw Object.assign(new Error('invalid_response'), {code: 'invalid_response'});
      }
      const rows = pages.flatMap(value => value.genres);
      if (new Set(rows.map(genre => genre.id)).size !== rows.length) throw Object.assign(new Error('invalid_response'), {code: 'invalid_response'});
      // At read completion the user may have moved back into the eviction edge.
      if (this.pages.length === 3 && (direction === 'next' && this.protected(this.pages[0]) || direction === 'previous' && this.protected(this.pages[2]))) {
        this.publish({phase: 'ready'}); return;
      }
      for (const old of this.pages) if (!pages.includes(old)) releaseSearchGenres(old.lease);
      this.pages = pages; adopted = true; this.retry = undefined;
      this.publish({phase: 'ready', revision: result.revision, genres: Object.freeze(rows), previousCursor: pages[0].previousCursor, nextCursor: pages.at(-1)?.nextCursor});
    }).catch(error => {if (!controller.signal.aborted) this.publish({phase: 'error', error});})
      .finally(() => {
        if (!adopted) releaseSearchGenres(returned);
        if (this.pending === task) this.pending = undefined;
        if (this.controller === controller) this.controller = undefined;
      });
    this.pending = task; return task;
  }
  load = (): Promise<void> => {
    if (this.state.phase === 'ready' && this.current()) return Promise.resolve();
    return this.retry ? this.read(this.retry.direction, this.retry.cursor) : this.read('initial');
  };
  next = (): Promise<void> => this.state.nextCursor ? this.read('next', this.state.nextCursor) : Promise.resolve();
  previous = (): Promise<void> => this.state.previousCursor ? this.read('previous', this.state.previousCursor) : Promise.resolve();
  reload = (): Promise<void> => {
    const anchor = this.pages.find(page => page.genres.some(genre => genre.id === (this.focusedId ?? this.visibleIds[0]))) ?? this.pages[0];
    return anchor ? this.read('reload', anchor.reloadCursor ?? anchor.cursor) : this.read('initial');
  };
  openGenre = (genre: NativeSearchGenre): void => {if (this.current() && this.active && this.state.genres.includes(genre)) this.options.openGenre(genre);};
  dispose = (): void => {if (this.disposed) return; this.disposed = true; this.controller?.abort(); this.pending = undefined;
    for (const page of this.pages) releaseSearchGenres(page.lease);
    this.pages = []; this.visibleIds = []; this.focusedId = undefined; this.retry = undefined; this.state = emptyList; this.listeners.clear();};
}
