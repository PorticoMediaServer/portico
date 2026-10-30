import {PersonalSavedService, isPersonalSavedRoute, type PersonalResource, type PersonalSavedIntent, type PersonalSavedSnapshot} from '../../../../../packages/client-core/src/personal-saved';
import {SavedService, fetchSavedSummary, type SavedApi, type SavedIntent, type SavedLandingSummary, type SavedRoute, type SavedScope, type SavedSnapshot} from '../../../../../packages/client-core/src/saved';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeSavedViewRenamePort} from './rename-view';

export type NativeSavedViewSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  route?: SavedRoute;
  saved?: SavedSnapshot;
  personal?: PersonalSavedSnapshot;
  /** Canonical whole-directory totals; a loaded page is never a directory count. */
  summary?: SavedLandingSummary;
  error?: unknown;
}>;

/** A real Saved subview owns its services and fences independently of the hub's
 * poster preview. This consumes only published SavedRoute semantics; it does
 * not invent the still-missing Saved browse/filter facade. */
export class NativeSavedViewDestination {
  private readonly options: Readonly<{api: SavedApi; scope: SavedScope; requestId: () => Promise<string>; current: () => boolean}>;
  private state: NativeSavedViewSnapshot = Object.freeze({phase: 'idle'});
  private readonly listeners = new Set<() => void>();
  private saved?: SavedService;
  private personal?: PersonalSavedService;
  private unsubscribe?: () => void;
  private summaryRead?: AbortController;
  private disposed = false;
  private generation = 0;
  private pending?: Promise<void>;
  constructor(options: NativeSavedViewDestination['options']) {this.options = options;}
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = (): NativeSavedViewSnapshot => {this.current(); return this.state;};
  /** Borrowed entry actions require the actual current producer object, not a
   * matching ID copied from another page, occurrence or viewer. */
  contains = (entry: ContentEntry): boolean => this.current() && (
    this.state.saved?.projection?.sections.some(section => section.entries.some(value => value.kind !== 'playlist' &&
      (value.kind === 'playlist_entry' ? !value.hidden && value.media === entry : value === entry))) === true ||
    this.state.personal?.entries.some(value => !value.hidden && value.media === entry) === true
  );
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  /** The canonical per-resource Saved service, only for the exact arrived view
   * object it currently serves. A directory row or another kind offers no port. */
  viewRename(resource: PersonalResource): Readonly<{port: NativeSavedViewRenamePort; current: () => boolean}> | undefined {
    this.current();
    if (this.disposed || this.state.phase !== 'ready' || this.state.route?.view !== 'resource'
      || this.state.route.resourceId !== resource.id) return;
    const service = this.personal;
    if (!service) return;
    const snapshot = service.getSnapshot();
    // A fresh opening has no claim on a canonical command the owner still holds.
    // This gate is only for new binding creation; an existing controller's own
    // POST/refresh/retry must stay current through pending/retryPending.
    if (snapshot.pending || snapshot.retryPending) return;
    const loaded = snapshot.resource;
    if (loaded !== resource || loaded.kind !== 'view' || !loaded.actions.includes('update')) return;
    const generation = this.generation;
    const valid = (): boolean => {
      if (!this.current() || this.disposed || generation !== this.generation || this.personal !== service) return false;
      if (this.state.route?.view !== 'resource' || this.state.route.resourceId !== resource.id) return false;
      const live = service.getSnapshot().resource;
      return !!live && live.kind === 'view' && live.id === resource.id && live.serverId === resource.serverId;
    };
    const port: NativeSavedViewRenamePort = {
      getSnapshot: () => service.getSnapshot(),
      subscribe: listener => valid() ? service.subscribe(listener) : () => {},
      mutate: intent => valid() && intent.action === 'update' && typeof intent.name === 'string'
        ? service.mutate({action: 'update', name: intent.name}) : Promise.resolve(false),
      retry: () => valid() ? service.retry() : Promise.resolve(false),
    };
    return Object.freeze({port, current: valid});
  }
  private publish(patch: Partial<NativeSavedViewSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, saved: this.saved?.getSnapshot(), personal: this.personal?.getSnapshot(), ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private retire(): void {
    this.summaryRead?.abort(); this.summaryRead = undefined;
    this.unsubscribe?.(); this.unsubscribe = undefined;
    this.saved?.dispose(); this.personal?.dispose(); this.saved = undefined; this.personal = undefined;
  }
  select = (route: SavedRoute): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (JSON.stringify(route) === JSON.stringify(this.state.route)) {
      if (this.pending) return this.pending;
      if (this.state.phase === 'ready') return Promise.resolve();
    }
    this.retire(); const generation = ++this.generation;
    const live = () => this.current() && generation === this.generation;
    const api: SavedApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!live() || signal?.aborted) throw Object.assign(new Error('native_saved_view_cancelled'), {code: 'cancelled'});
      const result = await this.options.api.request<T>(path, method, body, signal);
      if (!live() || signal?.aborted) throw Object.assign(new Error('native_saved_view_cancelled'), {code: 'cancelled'});
      return result;
    }};
    if (isPersonalSavedRoute(route)) this.personal = new PersonalSavedService(api, this.options.scope, this.options.requestId);
    else this.saved = new SavedService({api, scope: this.options.scope, requestId: this.options.requestId});
    const service = this.personal ?? this.saved!;
    this.state = Object.freeze({phase: 'loading', route: Object.freeze({...route})});
    this.unsubscribe = service.subscribe(() => this.publish({}));
    this.publish({error: undefined, summary: undefined});
    const controller = new AbortController(); this.summaryRead = controller;
    const task = (async () => {
      try {
        // The canonical select validates the actual route before any summary read.
        const read = service.select(route);
        const summary = route.view === 'collections' || route.view === 'views' ? fetchSavedSummary(api, this.options.scope, controller.signal) : undefined;
        const [, totals] = await Promise.all([read, summary]);
        if (!live()) return;
        const error = this.personal?.getSnapshot().error ?? this.saved?.getSnapshot().error;
        this.publish({phase: error ? 'error' : 'ready', summary: totals, error: error ?? undefined});
      } catch (error) {if (live() && !controller.signal.aborted) this.publish({phase: 'error', error});}
    })().finally(() => {if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  next = (sectionId?: string): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    if (this.state.phase !== 'ready') return Promise.resolve();
    const generation = this.generation, saved = this.saved, personal = this.personal;
    if (!personal && (!saved || !sectionId)) return Promise.resolve();
    this.publish({phase: 'loading'});
    const task = (async () => {
      try {
        if (personal) await personal.next();
        else await saved!.next(sectionId!);
        if (!this.current() || generation !== this.generation) return;
        const error = personal?.getSnapshot().error ?? saved?.getSnapshot().error;
        this.publish({phase: error ? 'error' : 'ready', error: error ?? undefined});
      } catch (error) {if (this.current() && generation === this.generation) this.publish({phase: 'error', error});}
    })().finally(() => {if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  retry = (): Promise<void> => this.state.route ? this.select(this.state.route) : Promise.resolve();
  /** The directory may create a playlist; all other commands still require its loaded resource. */
  mutateSaved = (intent: SavedIntent): Promise<void> => {
    this.current();
    if (this.disposed || !this.saved || (intent.action === 'create' ? this.state.route?.view !== 'playlists' : this.state.route?.view !== 'playlist'))
      return Promise.reject(new Error('Open the matching playlist page before changing it.'));
    return this.saved.mutate(intent);
  };
  /** The exact personal-resource command port for the loaded collection/view route. */
  mutatePersonal = (intent: PersonalSavedIntent): Promise<boolean> => {
    this.current();
    if (this.disposed || !this.personal) return Promise.resolve(false);
    return this.personal.mutate(intent);
  };
  /** Retry the canonical personal command with its retained operation ID and payload. */
  retryPersonalMutation = (): Promise<boolean> => {
    this.current();
    return !this.disposed && this.personal ? this.personal.retry() : Promise.resolve(false);
  };
  /** Re-read the current page in place after another Saved page changed it. */
  refresh = (): Promise<void> => {
    this.current();
    if (this.disposed) return Promise.resolve();
    if (this.personal) return this.personal.refresh();
    if (this.saved && this.state.route) return this.saved.refresh();
    return Promise.resolve();
  };
  /** A full playlist order window, read only when a move needs it. */
  loadOrder = (): Promise<void> => {
    this.current();
    if (this.disposed || !this.saved) return Promise.resolve();
    return this.saved.loadOrder();
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.generation++; this.retire(); this.pending = undefined;
    this.state = Object.freeze({phase: 'idle'}); this.listeners.clear();
  };
}
