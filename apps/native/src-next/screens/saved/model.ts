import {SavedService, fetchSavedSummary, type SavedApi, type SavedLandingSummary, type SavedRoute, type SavedScope, type SavedSnapshot} from '../../../../../packages/client-core/src/saved';
import {PersonalSavedService, type PersonalSavedSnapshot} from '../../../../../packages/client-core/src/personal-saved';
import {readPinOrder, type PinnedResource} from '../../../../../packages/client-core/src/library-pins';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';

export type NativeSavedSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  myList: SavedSnapshot; myListSquares: SavedSnapshot; favorites: SavedSnapshot; playlists: SavedSnapshot;
  collections: PersonalSavedSnapshot; views: PersonalSavedSnapshot;
  pins: readonly PinnedResource[]; error?: unknown;
  summary?: SavedLandingSummary;
}>;

/** Optional sections may be unavailable, but an identity refusal or rejected
 * document is not a successful empty section. Keep the checked parser's error. */
function requiredProducerFailure(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const value = error as {status?: unknown; statusCode?: unknown; code?: unknown};
  return value.status === 401 || value.status === 403 || value.statusCode === 401 || value.statusCode === 403
    || value.code === 'unauthorized' || value.code === 'forbidden' || value.code === 'not_permitted'
    || value.code === 'permission_changed' || value.code === 'session_revoked'
    || value.code === 'invalid_saved' || value.code === 'invalid_pins';
}

/** The Saved hub reads bounded canonical projections. It never fetches an entire
 * library to count it, invents a count, or treats a server preparation as local bytes. */
export class NativeSavedDestination {
  private readonly myList: SavedService;
  private readonly myListSquares: SavedService;
  private readonly favorites: SavedService;
  private readonly playlists: SavedService;
  private readonly collections: PersonalSavedService;
  private readonly views: PersonalSavedService;
  private readonly options: Readonly<{api: SavedApi; scope: SavedScope; requestId: () => Promise<string>; current: () => boolean}>;
  private state: NativeSavedSnapshot;
  private readonly closed: NativeSavedSnapshot;
  private readonly listeners = new Set<() => void>();
  private readonly subscriptions: (() => void)[];
  private controller?: AbortController;
  private disposed = false;
  private revision = 0;
  private pending?: Promise<void>;
  constructor(options: NativeSavedDestination['options']) {
    this.options = options;
    this.myList = new SavedService(options); this.myListSquares = new SavedService(options); this.favorites = new SavedService(options); this.playlists = new SavedService(options);
    this.collections = new PersonalSavedService(options.api, options.scope, options.requestId);
    this.views = new PersonalSavedService(options.api, options.scope, options.requestId);
    this.state = Object.freeze({phase: 'idle', myList: this.myList.getSnapshot(), myListSquares: this.myListSquares.getSnapshot(), favorites: this.favorites.getSnapshot(), playlists: this.playlists.getSnapshot(),
      collections: this.collections.getSnapshot(), views: this.views.getSnapshot(), pins: []});
    this.closed = this.state;
    this.subscriptions = [this.myList, this.myListSquares, this.favorites, this.playlists, this.collections, this.views].map(service => service.subscribe(() => this.publish({})));
  }
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = (): NativeSavedSnapshot => {this.current(); return this.state;};
  contains = (entry: ContentEntry): boolean => this.current() && (this.state.myList.projection?.sections.some(section => section.entries.some(value => value === entry)) === true
    || this.state.myListSquares.projection?.sections.some(section => section.entries.some(value => value === entry)) === true);
  /** The tablet My List grid reads the real watchlist in both drawn shapes under the
   * same page lifetime; sorting/filtering is a server policy on the canonical route. */
  selectMyList = (route: SavedRoute): Promise<void> => {
    if (!this.current() || route.view !== 'watchlist') return Promise.resolve();
    return (route.shape === 'squares' ? this.myListSquares : this.myList).select(route);
  };
  nextMyList = (shape: 'posters' | 'squares', sectionId: string): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    return (shape === 'squares' ? this.myListSquares : this.myList).next(sectionId);
  };
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeSavedSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, myList: this.myList.getSnapshot(), myListSquares: this.myListSquares.getSnapshot(), favorites: this.favorites.getSnapshot(), playlists: this.playlists.getSnapshot(),
      collections: this.collections.getSnapshot(), views: this.views.getSnapshot(), ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  /** `quiet` (pull to refresh) keeps the page's current content until the new reading lands. */
  load = (options: {quiet?: boolean} = {}): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    this.controller?.abort(); const controller = new AbortController(); this.controller = controller;
    const revision = ++this.revision;
    // H14: a reload keeps the painted summary and pins; only the first load starts empty.
    if (!options.quiet) this.publish({phase: 'loading', error: undefined});
    const task = Promise.allSettled([
      this.myList.select({view: 'watchlist', shape: 'posters'}), this.myListSquares.select({view: 'watchlist', shape: 'squares'}),
      fetchSavedSummary(this.options.api, this.options.scope, controller.signal),
      readPinOrder(this.options.api, this.options.scope.serverId, controller.signal),
    ]).then(results => {
      if (!this.current() || revision !== this.revision || controller.signal.aborted) return;
      // My List failures remain page errors. Optional transport failures omit their
      // section, while identity refusals and rejected documents remain real errors.
      const rejected = results.find(result => result.status === 'rejected' && requiredProducerFailure(result.reason));
      const failure = this.myList.getSnapshot().error ?? this.myListSquares.getSnapshot().error
        ?? (results[0].status === 'rejected' ? results[0].reason : undefined)
        ?? (rejected?.status === 'rejected' ? rejected.reason : undefined);
      const summary = results[2].status === 'fulfilled' ? results[2].value : undefined;
      const pins = results[3].status === 'fulfilled' ? results[3].value.order : [];
      this.publish({phase: failure ? 'error' : 'ready', summary, pins, error: failure ?? undefined});
    }).catch(error => {if (this.current() && revision === this.revision && !controller.signal.aborted) this.publish({phase: 'error', error});})
      .finally(() => {if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.revision++; this.controller?.abort();
    for (const unsubscribe of this.subscriptions) unsubscribe();
    for (const service of [this.myList, this.myListSquares, this.favorites, this.playlists, this.collections, this.views]) service.dispose();
    this.state = this.closed;
    this.listeners.clear();
  };
}

export function savedProjectionCount(snapshot: SavedSnapshot): number | undefined {
  return snapshot.phase === 'ready' ? snapshot.projection?.sections.reduce((sum, section) => sum + section.totalCount, 0) : undefined;
}
/** A partial directory has no total in the current contract. Its first page's length is
 * never presented as the total number of collections or saved views. */
export function savedDirectoryCount(snapshot: PersonalSavedSnapshot): number | undefined {
  return snapshot.route && !snapshot.loading && !snapshot.error && !snapshot.nextCursor ? snapshot.resources.length : undefined;
}
