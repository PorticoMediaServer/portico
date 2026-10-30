import type {SavedRoute, HistoryPeriod, PlaylistListFilter, SavedListFilter, SavedListSort, SavedProjection, SavedShapeCounts} from '../../../../../../packages/client-core/src/saved';
import {savedListSorts} from '../../../../../../packages/client-core/src/saved';
import {SavedService, fetchSavedSummary} from '../../../../../../packages/client-core/src/saved';
import type {PersonalResource, PersonalSavedSnapshot} from '../../../../../../packages/client-core/src/personal-saved';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import type {NativeSavedViewDestination} from '../../saved/view-model';
import {NativeSavedViewDestination as SavedView} from '../../saved/view-model';
import {nativeDestinationLifetime} from '../lifetime';
import {nativeScreenServices} from '../../native-screen-services';
import type {NativeDestinationActivationPort, NativeDestinationActivation} from '../focus';

export type NativeTVSavedTab = Extract<SavedRoute['view'], 'watchlist' | 'favorites' | 'playlists' | 'collections' | 'views' | 'history'>;
export type NativeTVSavedSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  tab: NativeTVSavedTab;
  posters?: SavedProjection;
  squares?: SavedProjection;
  playlists?: SavedProjection;
  resources: readonly PersonalResource[];
  entries: PersonalSavedSnapshot['entries'];
  counts?: SavedShapeCounts;
  error?: unknown;
}>;

const tabs: readonly NativeTVSavedTab[] = ['watchlist', 'favorites', 'playlists', 'collections', 'views', 'history'];

/** One TV Saved landing: six server-owned tabs, one shape pair for lists, and the
 * profile's own history. No page is assembled from another screen's fixture. */
export class NativeTVSavedController {
  private options: Readonly<{api: NativeShellBootstrapContext['api']; scope: ReturnType<typeof nativeDestinationLifetime>['scope']; current: () => boolean; requestId: () => Promise<string>}>;
  private views = new Map<string, NativeSavedViewDestination>();
  private squares = new Map<string, SavedService>();
  private listeners = new Set<() => void>();
  private state: NativeTVSavedSnapshot = Object.freeze({phase: 'idle', tab: 'watchlist', resources: [], entries: []});
  private generation = 0;
  private disposed = false;
  constructor(options: NativeTVSavedController['options']) {this.options = options;}
  getSnapshot = (): NativeTVSavedSnapshot => this.state;
  contains = (entry: ContentEntry): boolean => {
    if (!this.live()) return false;
    const state = this.state;
    const inProjection = (projection?: SavedProjection) => !!projection?.sections.some(section => section.entries.some(value => value.kind !== 'playlist' && (value.kind === 'playlist_entry' ? !value.hidden && value.media?.id === entry.id : value.id === entry.id)));
    return inProjection(state.posters) || inProjection(state.squares) || inProjection(state.playlists)
      || state.resources.some(value => value.id === entry.id) || state.entries.some(value => value.media?.id === entry.id);
  };
  subscribe = (listener: () => void): (() => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private live(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  private publish(patch: Partial<NativeTVSavedSnapshot>): void {
    if (!this.live()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private view(key: string): NativeSavedViewDestination {
    let view = this.views.get(key);
    if (!view) {
      view = new SavedView({api: this.options.api, scope: this.options.scope, current: () => this.live(), requestId: this.options.requestId});
      this.views.set(key, view);
    }
    return view;
  }
  private square(view: 'watchlist' | 'favorites'): SavedService {
    let service = this.squares.get(view);
    if (!service) {
      service = new SavedService({api: this.options.api, scope: this.options.scope, requestId: this.options.requestId});
      this.squares.set(view, service);
      service.subscribe(() => this.refresh());
    }
    return service;
  }
  private refresh(): void {
    if (!this.live()) return;
    const tab = this.state.tab;
    if (tab === 'watchlist' || tab === 'favorites') {
      const posters = this.views.get(`${tab}:posters`)?.getSnapshot();
      const squares = this.squares.get(tab)?.getSnapshot();
      const error = posters?.error ?? squares?.error;
      this.publish({phase: posters?.phase === 'error' || error ? 'error' : posters?.phase === 'loading' ? 'loading' : 'ready',
        posters: posters?.saved?.projection ?? undefined, squares: squares?.projection ?? undefined,
        counts: posters?.saved?.projection?.shapeCounts ?? this.state.counts, error: error ?? undefined});
      return;
    }
    const view = this.views.get(tab)?.getSnapshot();
    if (!view) return;
    this.publish({phase: view.phase, playlists: view.saved?.projection ?? undefined, resources: view.personal?.resources ?? [],
      entries: view.personal?.entries ?? [], error: view.error});
  }
  select = async (tab: NativeTVSavedTab, options: Readonly<{sort?: string; filter?: SavedListFilter | PlaylistListFilter; period?: HistoryPeriod; direction?: 'asc' | 'desc'}> = {}): Promise<void> => {
    if (!this.live() || !tabs.includes(tab)) return;
    const generation = ++this.generation;
    this.publish({phase: 'loading', tab, error: undefined, posters: undefined, squares: undefined, playlists: undefined, resources: [], entries: []});
    if (tab === 'watchlist' || tab === 'favorites') {
      const base: SavedRoute = {view: tab, shape: 'posters', sort: options.sort ?? 'added', direction: options.direction ?? 'desc', filter: options.filter ?? 'all'};
      const squareRoute: SavedRoute = {...base, shape: 'squares'};
      const posters = this.view(`${tab}:posters`), squares = this.square(tab);
      try {
        await Promise.all([posters.select(base), squares.select(squareRoute)]);
        if (generation === this.generation) this.refresh();
      } catch (error) {if (generation === this.generation) this.publish({phase: 'error', error});}
      return;
    }
    const route: SavedRoute = tab === 'history' ? {view: 'history', ...(options.period ? {period: options.period} : {})}
      : tab === 'playlists' ? {view: 'playlists', ...(options.filter ? {filter: options.filter as PlaylistListFilter} : {}),
        ...(options.sort && (savedListSorts as readonly string[]).includes(options.sort) ? {sort: options.sort as SavedListSort} : {}),
        ...(options.direction ? {direction: options.direction} : {})} : {view: tab};
    const view = this.view(tab);
    try {await view.select(route); if (generation === this.generation) this.refresh();}
    catch (error) {if (generation === this.generation) this.publish({phase: 'error', error});}
  };
  /** The body from screens/saved reads this exact source and owns no route. */
  source = (view: 'playlists' | 'collections' | 'views'): NativeSavedViewDestination => this.view(view);
  summary = async (): Promise<SavedShapeCounts | undefined> => {
    if (!this.live()) return;
    try {
      const summary = await fetchSavedSummary(this.options.api, this.options.scope);
      if (!this.live()) return;
      this.publish({counts: summary.myList});
      return summary.myList;
    } catch (error) {if (this.live()) this.publish({error});}
  };
  /** The personal history revision a clear confirmation must be drawn against. */
  historyRevision = (): number | undefined => this.live() ? this.views.get('history')?.getSnapshot().personal?.revision : undefined;
  /** The clear itself is profile-wide, exactly as the drawn sentence says; `period` only picks
   * the History filter reloaded afterwards. The captured revision, the caller's signal and a
   * live check before and after the request id keep a replaced opening from clearing, and its
   * completion from reloading a view that moved on. */
  clearHistory = async (operation: Readonly<{revision: number; period?: HistoryPeriod; signal: AbortSignal; isCurrent: () => boolean}>): Promise<void> => {
    const {revision, period, signal, isCurrent} = operation;
    // The caller's own scope is the authority here: the signal alone depends on the retirement
    // having rendered, so the captured operation is asked directly before and after the id.
    const live = () => this.live() && !signal.aborted && isCurrent();
    if (!live() || this.historyRevision() !== revision) return;
    const operationId = await this.options.requestId();
    if (!live() || this.historyRevision() !== revision) return;
    await this.options.api.request(routePath('post_personal_history_actions'), 'POST',
      {operationId, expectedRevision: revision, action: 'clear-history'}, signal);
    if (!live()) return;
    if (this.state.tab === 'history') await this.select('history', {period});
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.generation++;
    for (const view of this.views.values()) view.dispose();
    for (const service of this.squares.values()) service.dispose();
    this.views.clear(); this.squares.clear(); this.listeners.clear();
    this.state = Object.freeze({phase: 'idle', tab: this.state.tab, resources: [], entries: []});
  };
}

export function createNativeTVSavedDestination(context: NativeShellBootstrapContext) {
  let disposed = false;
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  const controller = new NativeTVSavedController({api: context.api, scope: lifetime.scope, current: () => !disposed && lifetime.current(), requestId: common.requestId});
  const navigation = context.navigation;
  // useSyncExternalStore needs the same snapshot until something changes: a fresh object on every read
  // re-rendered TV Saved without end ("Maximum update depth exceeded", P2-74 E-tv-11).
  let snapshot: NativeDestinationActivation | undefined;
  const activation: NativeDestinationActivationPort = {
    subscribe: navigation.subscribe,
    getSnapshot: (): NativeDestinationActivation => {
      const route = navigation.getSnapshot(), live = !disposed && lifetime.current() && route.route.kind === 'shell' && route.route.destination.kind === 'saved';
      const intent = navigation.getIntent();
      if (!snapshot || snapshot.visible !== live || snapshot.intent !== intent || snapshot.revision !== route.revision)
        snapshot = Object.freeze({visible: live, intent, revision: route.revision});
      return snapshot;
    },
  };
  return Object.freeze({controller, activation, dispose: () => {if (disposed) return; disposed = true; controller.dispose();}});
}

export type NativeSavedEntry = Readonly<{entry: ContentEntry; updatedAt?: string; positionSeconds?: number; completed?: boolean}>;
export const savedTabOrder = tabs;
