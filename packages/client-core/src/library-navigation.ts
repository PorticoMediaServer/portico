import {isSettingsDestination,type SettingsDestination} from './settings-destinations.ts';
/** Navigation only: adapters own catalog requests, playback and actual native focus. */
export type NavigationScope = Readonly<{ viewerId: string; serverId: string }>;
export type LibraryEntityView = 'show' | 'season' | 'artist' | 'album' | 'book' | 'collection' | 'disc' | 'author' | 'book_series';
export type LibraryRoute =
  | Readonly<{ kind: 'home' | 'search' | 'channels' | 'library-channels' | 'downloads' }>
  | Readonly<{ kind: 'account' | 'settings'; section?:SettingsDestination }>
  | Readonly<{ kind: 'dvr'; view?: 'upcoming'|'recorded'|'rules'|'history'|'storage'; cursor?:string; recordingId?:string }>
  | Readonly<{ kind: 'saved'; view: 'watchlist' | 'favorites' | 'playlists' | 'collections' | 'views' | 'history' | 'resource';resourceId?:string }>
  | Readonly<{ kind: 'playlist'; playlistId: string }>
  | Readonly<{ kind: 'library'; libraryId: string; tab: string }>
  | Readonly<{ kind: 'entity'; libraryId: string; entityId: string; view: LibraryEntityView }>
  | Readonly<{ kind: 'detail' | 'player'; libraryId: string; itemId: string }>;
export type NavigationFocus = Readonly<{ region: 'rail' | 'tabs' | 'content'; targetId?: string }>;
export type ScrollAnchor = Readonly<{ itemId: string; offset: number }>;
export type LibraryNavigationMetadata = Readonly<{
  id: string;
  name: string;
  tabs: readonly string[];
  sorts: readonly string[];
  filters?: Readonly<Record<string, readonly string[]>>;
  /** Initial server default only; remembered choices remain tentative until projected. */
  pending: boolean;
  /** The exact tab whose projection supplied choices; null only while metadata is pending. */
  queryTab: string | null;
}>;
export type LibraryViewState = Readonly<{
  libraryId: string;
  tab: string;
  sort: string;
  direction: '' | 'asc' | 'desc';
  q: string;
  filters: Readonly<Record<string, string>>;
  pageCursor: string | null;
  focusedItemId: string | null;
  scrollAnchor: ScrollAnchor | null;
  focus: NavigationFocus;
}>;
export type EntityViewState = Readonly<Omit<LibraryViewState, 'tab'> & { entityId: string; view: LibraryEntityView }>;
export type LibraryViewUpdate = Partial<Pick<LibraryViewState, 'sort' | 'direction' | 'q' | 'filters' | 'pageCursor' | 'focusedItemId' | 'scrollAnchor'>>;
export type SavedViewState = Readonly<{sort:string;direction:''|'asc'|'desc';cursor:string|null;history:readonly (string|null)[];focusedItemId:string|null;scrollAnchor:ScrollAnchor|null;focus:NavigationFocus}>;
export type SavedViewUpdate = Partial<Omit<SavedViewState,'focus'>>;
function savedView(value?:SavedViewUpdate):SavedViewState {
 const v={sort:'',direction:'',cursor:null,history:[],focusedItemId:null,scrollAnchor:null,...value};
 if (!(v.sort===''||identifier(v.sort)) || !['','asc','desc'].includes(v.direction) || !(v.cursor===null||text(v.cursor,4096)) || !Array.isArray(v.history) || v.history.length>64 || v.history.some(c=>c!==null&&!text(c,4096)) || !(v.focusedItemId===null||identifier(v.focusedItemId))) throw new Error('Invalid Saved position.');
 return Object.freeze({...v,direction:v.direction as SavedViewState['direction'],history:Object.freeze([...v.history]),scrollAnchor:anchor(v.scrollAnchor),focus:Object.freeze({region:'content'})});
}
export interface NavigationStorage {
  read(key: string): Promise<string | null>;
  write(key: string, value: string): Promise<void>;
}
export type LibraryNavigationSnapshot = Readonly<{
  scope: NavigationScope;
  generation: number;
  route: LibraryRoute;
  focus: NavigationFocus;
  libraries: readonly LibraryNavigationMetadata[];
  currentLibrary: LibraryViewState | null;
  currentEntity: EntityViewState | null;
  currentSaved: SavedViewState | null;
  remembered: readonly LibraryViewState[];
  /** True when the current view's saved query still needs server validation. */
  metadataPending: boolean;
  canGoBack: boolean;
  /** Includes the current destination: always 1..16. */
  stackDepth: number;
  /** Optional native root tab; each root retains its own bounded Back history. */
  navigationTab: string | null;
  restoring: boolean;
  storageError: string | null;
}>;
type Frame = { route: LibraryRoute; focus: NavigationFocus; view: LibraryViewState | null; entity: EntityViewState | null; saved: SavedViewState | null };
const MAX_VIEWS = 64;
const MAX_STACK = 16;
const MAX_STORED_LENGTH = 2 * 1024 * 1024;
const object = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
function text(v: unknown, max = 128): v is string {
  return typeof v === 'string' && v.length > 0 && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
}
function identifier(v: unknown): v is string { return text(v) && /^[a-zA-Z0-9_.:-]+$/.test(v); }
function assertScope(scope: NavigationScope): NavigationScope {
  if (!object(scope) || !text(scope.viewerId, 512) || !text(scope.serverId, 512)) throw new Error('A viewer and server navigation scope is required.');
  return Object.freeze({ viewerId: scope.viewerId, serverId: scope.serverId });
}
/** The viewer ID must identify the authority, account and selected profile, not a display name. */
export function navigationStorageKey(scope: NavigationScope): string {
  const valid = assertScope(scope);
  return 'portico.navigation.v1:' + encodeURIComponent(JSON.stringify([valid.viewerId, valid.serverId]));
}
function choices(values: unknown, max: number, allowEmpty = false): readonly string[] {
  if (!Array.isArray(values) || (!allowEmpty && values.length === 0) || values.length > max || !values.every(identifier) || new Set(values).size !== values.length) throw new Error('Invalid library navigation choices.');
  return Object.freeze([...values]);
}
function metadata(values: readonly LibraryNavigationMetadata[]): Map<string, LibraryNavigationMetadata> {
  if (!Array.isArray(values) || values.length > 2048) throw new Error('Invalid library metadata.');
  const result = new Map<string, LibraryNavigationMetadata>();
  for (const v of values) {
    if (!object(v) || !identifier(v.id) || !text(v.name, 160) || result.has(v.id)) throw new Error('Invalid or duplicate library metadata.');
    const filters: Record<string, readonly string[]> = {};
    if (v.filters !== undefined) {
      if (!object(v.filters) || Object.keys(v.filters).length > 16) throw new Error('Invalid filter metadata.');
      for (const [key, options] of Object.entries(v.filters)) {
        if (!identifier(key)) throw new Error('Invalid filter key.');
        Object.defineProperty(filters, key, { value: choices(options, 64, true), enumerable: true });
      }
    }
    const tabs = choices(v.tabs, 16);
    if (typeof v.pending !== 'boolean' || (v.pending ? v.queryTab !== null : !identifier(v.queryTab) || !tabs.includes(v.queryTab))) throw new Error('Invalid metadata validation state.');
    result.set(v.id, Object.freeze({ id: v.id, name: v.name, tabs, sorts: choices(v.sorts, 32, true), filters: Object.freeze(filters), pending: v.pending, queryTab: v.pending ? null : v.queryTab as string }));
  }
  return result;
}
function focus(value: unknown): NavigationFocus {
  if (!object(value) || !['rail', 'tabs', 'content'].includes(String(value.region)) || (value.targetId !== undefined && !identifier(value.targetId))) throw new Error('Invalid navigation focus.');
  return Object.freeze({ region: value.region as NavigationFocus['region'], ...(value.targetId === undefined ? {} : { targetId: value.targetId as string }) });
}
function anchor(value: unknown): ScrollAnchor | null {
  if (value === null) return null;
  if (!object(value) || !identifier(value.itemId) || typeof value.offset !== 'number' || !Number.isFinite(value.offset) || Math.abs(value.offset) > 10_000_000) throw new Error('Invalid scroll anchor.');
  return Object.freeze({ itemId: value.itemId, offset: value.offset });
}
function filterValues(value: unknown, meta: LibraryNavigationMetadata, validate = true): Readonly<Record<string, string>> {
  if (!object(value) || Object.keys(value).length > 16) throw new Error('Invalid library filters.');
  const result: Record<string, string> = {};
  for (const [key, selected] of Object.entries(value).sort(([a], [b]) => a.localeCompare(b))) {
    if (!identifier(key) || !text(selected) || (validate && (!Object.hasOwn(meta.filters ?? {}, key) || !meta.filters![key].includes(selected)))) throw new Error('Unknown library filter.');
    Object.defineProperty(result, key, { value: selected, enumerable: true });
  }
  return Object.freeze(result);
}
function defaultView(meta: LibraryNavigationMetadata, tab = meta.tabs[0]): LibraryViewState {
  return Object.freeze({ libraryId: meta.id, tab, sort: !meta.pending && meta.queryTab === tab ? meta.sorts[0] ?? '' : '', direction: '', q: '', filters: Object.freeze({}), pageCursor: null, focusedItemId: null, scrollAnchor: null, focus: Object.freeze({ region: 'content' }) });
}
function view(value: unknown, meta: LibraryNavigationMetadata): LibraryViewState {
  if (!object(value) || value.libraryId !== meta.id || !identifier(value.tab) || (!meta.pending && !meta.tabs.includes(value.tab)) || !(value.sort === '' || identifier(value.sort)) || !(value.pageCursor === null || text(value.pageCursor, 4096)) || !(value.focusedItemId === null || identifier(value.focusedItemId))) throw new Error('Invalid saved library view.');
  const validateQuery = !meta.pending && meta.queryTab === value.tab;
  if (validateQuery && !(meta.sorts.length === 0 ? value.sort === '' : meta.sorts.includes(value.sort))) throw new Error('Invalid saved library sort.');
  const direction = value.direction;
  const q = value.q;
  if (typeof direction !== 'string' || !['', 'asc', 'desc'].includes(direction) || typeof q !== 'string' || q.length > 512 || /[\x00-\x1f\x7f]/.test(q)) throw new Error('Invalid saved library query.');
  return Object.freeze({ libraryId: meta.id, tab: value.tab as string, sort: value.sort as string, direction: direction as LibraryViewState['direction'], q, filters: filterValues(value.filters, meta, validateQuery), pageCursor: value.pageCursor as string | null, focusedItemId: value.focusedItemId as string | null, scrollAnchor: anchor(value.scrollAnchor), focus: focus(value.focus) });
}
function libraryId(route: LibraryRoute): string | null { return 'libraryId' in route ? route.libraryId : null; }

export class LibraryNavigationService {
  private scope: NavigationScope;
  private metas: Map<string, LibraryNavigationMetadata>;
  private views = new Map<string, LibraryViewState>();
  private tabViews = new Map<string, LibraryViewState>();
  private frames: Frame[] = [];
  private navigationTab: string | null = null;
  private tabHistories = new Map<string, {current: Frame; frames: Frame[]}>();
  private activeEntity: EntityViewState | null = null;
  private activeSaved: SavedViewState | null = null;
  private lastLibrary: Extract<LibraryRoute, { kind: 'library' }> | null = null;
  private pendingWrites = new Map<string, { payload: string; scope: NavigationScope }>();
  private writing = false;
  private route: LibraryRoute = Object.freeze({ kind: 'home' });
  private activeFocus: NavigationFocus = Object.freeze({ region: 'rail', targetId: 'home' });
  private listeners = new Set<() => void>();
  private storage?: NavigationStorage;
  private writes: Promise<void> = Promise.resolve();
  private generation = 0;
  private restoring = false;
  private storageError: string | null = null;
  private restoreTicket = 0;
  private disposed = false;
  private snapshot!: LibraryNavigationSnapshot;
  constructor(options: { scope: NavigationScope; libraries: readonly LibraryNavigationMetadata[]; storage?: NavigationStorage }) {
    this.scope = assertScope(options.scope);
    this.metas = metadata(options.libraries);
    this.storage = options.storage;
    this.publish();
  }
  getSnapshot = (): LibraryNavigationSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };
  private publish(): void {
    if (this.disposed) return;
    const id = libraryId(this.route);
    this.snapshot = Object.freeze({ scope: this.scope, generation: this.generation, route: this.route, focus: this.activeFocus, libraries: Object.freeze([...this.metas.values()]), currentLibrary: id ? this.views.get(id) ?? null : null, currentEntity: this.activeEntity, currentSaved: this.activeSaved, remembered: Object.freeze([...this.views.values()]), metadataPending: id ? !!this.metas.get(id)?.pending || this.metas.get(id)?.queryTab !== this.views.get(id)?.tab : false, canGoBack: this.frames.length > 0, stackDepth: this.frames.length + 1, navigationTab: this.navigationTab, restoring: this.restoring, storageError: this.storageError });
    for (const listener of this.listeners) listener();
  }
  private assertActive(): void { if (this.disposed) throw new Error('Navigation service is disposed.'); }
  private changed(): void {
    this.restoreTicket++;
    this.restoring = false;
    this.generation++;
    if (this.route.kind === 'library') this.lastLibrary = this.route;
    this.publish();
    this.save();
  }
  private remember(v: LibraryViewState): void {
    const key = JSON.stringify([v.libraryId, v.tab]);
    this.tabViews.delete(key); this.tabViews.set(key, v);
    while (this.tabViews.size > MAX_VIEWS) this.tabViews.delete(this.tabViews.keys().next().value!);
    this.views.delete(v.libraryId);
    this.views.set(v.libraryId, v);
    while (this.views.size > MAX_VIEWS) this.views.delete(this.views.keys().next().value!);
  }
  private getView(id: string, tab?: string): LibraryViewState {
    const meta = this.metas.get(id);
    if (!meta) throw new Error('Library is no longer available.');
    const selected = this.views.get(id);
    if (tab === undefined || selected?.tab === tab) return selected ?? defaultView(meta);
    return this.tabViews.get(JSON.stringify([id, tab])) ?? defaultView(meta, tab);
  }
  private capture(): Frame {
    const id = libraryId(this.route);
    return { route: this.route, focus: this.activeFocus, view: id ? this.getView(id) : null, entity: this.activeEntity, saved: this.activeSaved };
  }
  private validateRoute(value: LibraryRoute): LibraryRoute {
    if (!object(value)) throw new Error('Invalid navigation route.');
    if (value.kind === 'home' || value.kind === 'search' || value.kind === 'channels' || value.kind === 'library-channels' || value.kind === 'downloads') return Object.freeze({ kind: value.kind });
    if (value.kind === 'account' || value.kind === 'settings') {
      if(value.section!==undefined&&!isSettingsDestination(value.section))throw new Error('Unknown settings page.');
      return Object.freeze({kind:value.kind,...(value.section?{section:value.section}:{})});
    }
    if (value.kind === 'dvr') {
      if(value.view!==undefined&&!['upcoming','recorded','rules','history','storage'].includes(value.view)||value.cursor!==undefined&&!text(value.cursor,4096)||value.recordingId!==undefined&&!/^[a-f0-9]{64}$/.test(value.recordingId))throw new Error('Invalid recordings destination.');
      return Object.freeze({kind:'dvr',view:value.view??'upcoming',...(value.cursor?{cursor:value.cursor}:{}),...(value.recordingId?{recordingId:value.recordingId}:{})});
    }
    if (value.kind === 'saved') {
      if (!['watchlist', 'favorites', 'playlists','collections','views','history','resource'].includes(value.view)) throw new Error('Invalid Saved destination.');
      if(value.view==='resource'&&!identifier(value.resourceId))throw new Error('Invalid saved resource destination.');
      return Object.freeze({ kind: 'saved', view: value.view,...(value.resourceId?{resourceId:value.resourceId}:{}) });
    }
    if (value.kind === 'playlist') {
      if (!identifier(value.playlistId)) throw new Error('Invalid playlist destination.');
      return Object.freeze({ kind: 'playlist', playlistId: value.playlistId });
    }
    if (value.kind !== 'library' && value.kind !== 'detail' && value.kind !== 'player' && value.kind !== 'entity') throw new Error('Invalid navigation route.');
    const meta = this.metas.get(value.libraryId);
    if (!meta) throw new Error('Library is no longer available.');
    if (value.kind === 'library') {
      if (!identifier(value.tab) || (!meta.pending && !meta.tabs.includes(value.tab))) throw new Error('Unknown library tab.');
      return Object.freeze({ kind: value.kind, libraryId: meta.id, tab: value.tab });
    }
    if (value.kind === 'entity') {
      if (!identifier(value.entityId) || !['show', 'season', 'artist', 'album', 'book', 'collection', 'disc', 'author', 'book_series'].includes(value.view)) throw new Error('Invalid library entity route.');
      return Object.freeze({ kind: 'entity', libraryId: meta.id, entityId: value.entityId, view: value.view });
    }
    if (!identifier(value.itemId)) throw new Error('Invalid media item ID.');
    return Object.freeze({ kind: value.kind, libraryId: meta.id, itemId: value.itemId });
  }
  navigate(next: LibraryRoute): void {
    this.assertActive();
    const route = this.validateRoute(next);
    if (JSON.stringify(route) === JSON.stringify(this.route)) return;
    this.frames.push(this.capture());
    if (this.frames.length >= MAX_STACK) this.frames.shift();
    this.initializeRoute(route);
    this.changed();
  }
  private initializeRoute(route: LibraryRoute): void {
    this.route = route;
    this.activeEntity = route.kind === 'entity' ? Object.freeze({ libraryId: route.libraryId, entityId: route.entityId, view: route.view, sort: '', direction: '', q: '', filters: Object.freeze({}), pageCursor: null, focusedItemId: null, scrollAnchor: null, focus: Object.freeze({ region: 'content', targetId: route.entityId }) }) : null;
    this.activeSaved = route.kind==='saved'||route.kind==='playlist'?savedView():null;
    const id = libraryId(route);
    if (id) {
      const v = this.getView(id, route.kind === 'library' ? route.tab : undefined);
      this.remember(v);
      this.activeFocus = route.kind === 'library' ? v.focus : Object.freeze({ region: 'content', targetId: 'itemId' in route ? route.itemId : 'entityId' in route ? route.entityId : undefined });
    } else this.activeFocus = Object.freeze({ region: 'rail', targetId: route.kind });
  }
  /** Enable native root tabs after restoration, binding the current route to its tab.
   * Histories are session-only: never restore players or another viewer's stack. */
  enableNavigationTabs(currentTab: string): void {
    this.assertActive();
    if (!identifier(currentTab)) throw new Error('Invalid navigation tab.');
    if (this.navigationTab !== null) return;
    this.navigationTab = currentTab;
    this.changed();
  }
  switchNavigationTab(nextTab: string, root: LibraryRoute): void {
    this.assertActive();
    if (!identifier(nextTab) || this.navigationTab === null) throw new Error('Enable navigation tabs before switching.');
    if (nextTab === this.navigationTab) return;
    const checkedRoot = this.validateRoute(root);
    this.tabHistories.delete(this.navigationTab);
    this.tabHistories.set(this.navigationTab, {current: this.capture(), frames: [...this.frames]});
    // Five standard roots, bounded even if an adapter supplies custom roots.
    while (this.tabHistories.size > 8) this.tabHistories.delete(this.tabHistories.keys().next().value!);
    const saved = this.tabHistories.get(nextTab);
    this.tabHistories.delete(nextTab);
    this.navigationTab = nextTab;
    this.frames = saved ? [...saved.frames] : [];
    if (saved) this.applyFrame(saved.current);
    else this.initializeRoute(checkedRoot);
    this.changed();
  }
  private applyFrame(frame: Frame): void {
    this.route = frame.route; this.activeEntity = frame.entity;
    this.activeSaved = frame.saved; this.activeFocus = frame.focus;
    if (frame.view) this.remember(frame.view);
  }
  selectLibrary(id: string, tab?: string): void { this.navigate({ kind: 'library', libraryId: id, tab: tab ?? this.getView(id).tab }); }
  selectTab(tab: string): void {
    this.assertActive();
    if (this.route.kind !== 'library') throw new Error('Select a library before selecting its tab.');
    if (this.route.tab === tab) return;
    const id = this.route.libraryId;
    this.route = this.validateRoute({ ...this.route, tab });
    const v = this.getView(id, tab);
    this.activeFocus = Object.freeze({ region: 'tabs', targetId: tab });
    this.remember(Object.freeze({ ...v, focus: this.activeFocus }));
    this.changed();
  }
  updateLibrary(id: string, patch: LibraryViewUpdate): void {
    this.assertActive();
    const meta = this.metas.get(id);
    if (!meta) throw new Error('Library is no longer available.');
    const previous = this.getView(id);
    // Copy only explicitly supported fields; no arbitrary API data enters storage.
    const next = { ...previous };
    for (const key of ['sort', 'direction', 'q', 'filters', 'pageCursor', 'focusedItemId', 'scrollAnchor'] as const) if (Object.hasOwn(patch, key)) Object.assign(next, { [key]: patch[key] });
    const queryChanged = next.sort !== previous.sort || next.direction !== previous.direction || next.q !== previous.q || JSON.stringify(next.filters) !== JSON.stringify(previous.filters);
    if (queryChanged) {
      next.pageCursor = null; next.focusedItemId = null; next.scrollAnchor = null;
      next.focus = Object.freeze({ region: 'content' });
    }
    this.remember(view(next, meta));
    if (queryChanged && this.route.kind === 'library' && this.route.libraryId === id) this.activeFocus = next.focus;
    this.changed();
  }
  /** Entity query/position is transient and belongs only to the bounded Back frame. */
  updateEntity(patch: LibraryViewUpdate): void {
    this.assertActive();
    if (this.route.kind !== 'entity' || !this.activeEntity) throw new Error('Select an entity before updating its view.');
    const previous = this.activeEntity, next = { ...previous };
    for (const key of ['sort', 'direction', 'q', 'filters', 'pageCursor', 'focusedItemId', 'scrollAnchor'] as const) if (Object.hasOwn(patch, key)) Object.assign(next, { [key]: patch[key] });
    const queryChanged = next.sort !== previous.sort || next.direction !== previous.direction || next.q !== previous.q || JSON.stringify(next.filters) !== JSON.stringify(previous.filters);
    if (queryChanged) { next.pageCursor = null; next.focusedItemId = null; next.scrollAnchor = null; next.focus = Object.freeze({ region: 'content' }); }
    // Server options remain authoritative. Structural validation prevents unbounded
    // transient state without inventing the entity's supported sort/filter choices.
    const checked = view({ ...next, tab: this.route.view }, { id: this.route.libraryId, name: '', tabs: [this.route.view], sorts: [], pending: true, queryTab: null });
    const { tab: _tab, ...fields } = checked;
    this.activeEntity = Object.freeze({ ...fields, entityId: previous.entityId, view: previous.view });
    this.activeFocus = this.activeEntity.focus;
    this.changed();
  }
  /** Saved order/query/cursors are transient, bounded and restored only through Back. */
  updateSaved(patch: SavedViewUpdate): void {
    this.assertActive();
    if (!this.activeSaved || (this.route.kind!=='saved'&&this.route.kind!=='playlist')) throw new Error('Open Saved before updating its position.');
    const next={...this.activeSaved};
    for(const key of ['sort','direction','cursor','history','focusedItemId','scrollAnchor'] as const) if(Object.hasOwn(patch,key))Object.assign(next,{[key]:patch[key]});
    if(next.sort!==this.activeSaved.sort||next.direction!==this.activeSaved.direction){next.cursor=null;next.history=[];next.focusedItemId=null;next.scrollAnchor=null;}
    const checked=savedView(next);
    this.activeSaved=Object.freeze({...checked,focus:this.activeFocus});this.changed();
  }
  setFocus(next: NavigationFocus): void {
    this.assertActive();
    this.activeFocus = focus(next);
    if(this.activeSaved)this.activeSaved=Object.freeze({...this.activeSaved,focus:this.activeFocus});
    if (this.activeEntity && this.route.kind === 'entity') this.activeEntity = Object.freeze({ ...this.activeEntity, focus: this.activeFocus });
    // Detail/player focus belongs to its Back frame, not the underlying grid.
    if (this.route.kind === 'library') {
      const v = this.getView(this.route.libraryId);
      this.remember(Object.freeze({ ...v, focus: this.activeFocus }));
    }
    this.changed();
  }
  goBack(): boolean {
    this.assertActive();
    const frame = this.frames.pop();
    if (!frame) return false;
    this.applyFrame(frame);
    this.changed();
    return true;
  }
  /** Metadata is authoritative: removed libraries and invalid history cannot be reopened. */
  setLibraries(libraries: readonly LibraryNavigationMetadata[]): void {
    this.assertActive();
    this.metas = metadata(libraries);
    if (this.lastLibrary) {
      const meta = this.metas.get(this.lastLibrary.libraryId);
      this.lastLibrary = meta ? Object.freeze({ kind: 'library', libraryId: meta.id, tab: (meta.pending || meta.tabs.includes(this.lastLibrary.tab)) ? this.lastLibrary.tab : meta.tabs[0] }) : null;
    }
    for (const [key, v] of this.tabViews) {
      const meta = this.metas.get(v.libraryId);
      if (!meta || (!meta.pending && !meta.tabs.includes(v.tab))) this.tabViews.delete(key);
      else { try { this.tabViews.set(key, view(v, meta)); } catch { this.tabViews.set(key, defaultView(meta, v.tab)); } }
    }
    for (const [id, v] of this.views) {
      const meta = this.metas.get(id);
      if (!meta) this.views.delete(id);
      else { try { this.views.set(id, view(v, meta)); } catch { this.views.set(id, defaultView(meta, meta.pending || meta.tabs.includes(v.tab) ? v.tab : meta.tabs[0])); } }
    }
    this.frames = this.frames.filter(frame => {
      try {
        this.validateRoute(frame.route);
        if (frame.view) frame.view = view(frame.view, this.metas.get(frame.view.libraryId)!);
        return true;
      } catch { return false; }
    });
    for (const [key, history] of this.tabHistories) {
      try {
        this.validateRoute(history.current.route);
        if (history.current.view) history.current.view = view(history.current.view, this.metas.get(history.current.view.libraryId)!);
        history.frames = history.frames.filter(frame => {
          try { this.validateRoute(frame.route); if (frame.view) frame.view = view(frame.view, this.metas.get(frame.view.libraryId)!); return true; }
          catch { return false; }
        });
      } catch { this.tabHistories.delete(key); }
    }
    try { this.route = this.validateRoute(this.route); }
    catch {
      const id = libraryId(this.route);
      if (id && this.metas.has(id) && this.route.kind === 'library') {
        const v = this.getView(id); this.route = Object.freeze({ kind: 'library', libraryId: id, tab: v.tab }); this.activeFocus = v.focus;
      } else { this.route = Object.freeze({ kind: 'home' }); this.activeFocus = Object.freeze({ region: 'rail', targetId: 'home' }); }
    }
    if (this.route.kind !== 'entity') this.activeEntity = null;
    if(this.route.kind!=='saved'&&this.route.kind!=='playlist')this.activeSaved=null;
    if (this.route.kind === 'library') this.activeFocus = this.getView(this.route.libraryId).focus;
    this.changed();
  }
  async setScope(scope: NavigationScope, libraries: readonly LibraryNavigationMetadata[]): Promise<void> {
    this.assertActive();
    const validScope = assertScope(scope), validMetadata = metadata(libraries);
    this.restoreTicket++;
    this.lastLibrary = null;
    this.tabHistories.clear(); this.navigationTab = null;
    this.scope = validScope; this.metas = validMetadata; this.views.clear(); this.tabViews.clear(); this.frames = []; this.activeEntity = null; this.activeSaved = null;
    this.route = Object.freeze({ kind: 'home' }); this.activeFocus = Object.freeze({ region: 'rail', targetId: 'home' });
    this.storageError = null; this.generation++; this.restoring = false; this.publish();
    await this.restore();
  }
  /** Explicit restoration never auto-opens a player or resurrects old Back history. */
  async restore(): Promise<void> {
    this.assertActive();
    if (!this.storage) return;
    const ticket = ++this.restoreTicket;
    const scope = this.scope, key = navigationStorageKey(scope);
    this.restoring = true; this.storageError = null; this.publish();
    try {
      // Drain earlier writes before a repeated restore of the same scope.
      await this.writes;
      if (this.disposed || ticket !== this.restoreTicket) return;
      const raw = await this.storage.read(key);
      if (this.disposed || ticket !== this.restoreTicket) return;
      const restored = new Map<string, LibraryViewState>();
      const restoredTabs = new Map<string, LibraryViewState>();
      let destination: LibraryRoute = Object.freeze({ kind: 'home' });
      if (raw !== null) {
        if (typeof raw !== 'string' || raw.length > MAX_STORED_LENGTH) throw new Error('Invalid navigation storage.');
        const data: unknown = JSON.parse(raw);
        if (!object(data) || Object.keys(data).length !== 5 || !['version','scope','lastLibrary','views','tabViews'].every(key => Object.hasOwn(data,key)) || data.version !== 1 || !object(data.scope) || data.scope.viewerId !== scope.viewerId || data.scope.serverId !== scope.serverId || !Array.isArray(data.views) || data.views.length > MAX_VIEWS || !Array.isArray(data.tabViews) || data.tabViews.length > MAX_VIEWS) throw new Error('Invalid navigation storage scope or shape.');
        if (data.lastLibrary !== null && (!object(data.lastLibrary) || data.lastLibrary.kind !== 'library' || !identifier(data.lastLibrary.libraryId) || !identifier(data.lastLibrary.tab))) throw new Error('Invalid saved library destination.');
        if (object(data.lastLibrary) && data.lastLibrary.kind === 'library' && identifier(data.lastLibrary.libraryId)) {
          const meta = this.metas.get(data.lastLibrary.libraryId);
          if (meta) destination = Object.freeze({ kind: 'library', libraryId: meta.id, tab: identifier(data.lastLibrary.tab) && (meta.pending || meta.tabs.includes(data.lastLibrary.tab)) ? data.lastLibrary.tab as string : meta.tabs[0] });
        }
        {
          if (!Array.isArray(data.tabViews) || data.tabViews.length > MAX_VIEWS) throw new Error('Invalid saved tab views.');
          for (const candidate of data.tabViews) {
            if (!object(candidate) || !identifier(candidate.libraryId) || !identifier(candidate.tab)) throw new Error('Invalid saved tab view.');
            const meta = this.metas.get(candidate.libraryId), key = JSON.stringify([candidate.libraryId, candidate.tab]);
            if (restoredTabs.has(key)) throw new Error('Duplicate saved tab view.');
            if (!meta || (!meta.pending && !meta.tabs.includes(candidate.tab))) continue;
            view(candidate, { ...meta, pending: true, queryTab: null });
            try { restoredTabs.set(key, view(candidate, meta)); } catch { restoredTabs.set(key, defaultView(meta, candidate.tab)); }
          }
        }
        for (const candidate of data.views) {
          if (!object(candidate) || !identifier(candidate.libraryId) || restored.has(candidate.libraryId)) throw new Error('Invalid navigation storage entries.');
          const meta = this.metas.get(candidate.libraryId);
          if (!meta) continue;
          // Refuse an old record shape before reconciling changed server choices.
          view(candidate, { ...meta, pending: true, queryTab: null });
          // Removed metadata choices safely reset this library's query/position.
          try { restored.set(meta.id, view(candidate, meta)); }
          catch { restored.set(meta.id, defaultView(meta, identifier(candidate.tab) && (meta.pending || meta.tabs.includes(candidate.tab)) ? candidate.tab : meta.tabs[0])); }
        }
      }
      // Merge only when there have been no user mutations since restoration began.
      this.tabHistories.clear(); this.navigationTab = null;
      this.views = restored; this.tabViews = restoredTabs; this.route = destination; this.frames = []; this.activeEntity = null; this.activeSaved = null;
      this.lastLibrary = destination.kind === 'library' ? destination : null;
      if (destination.kind === 'library') {
        const selected = this.getView(destination.libraryId, destination.tab);
        this.remember(selected); this.activeFocus = selected.focus;
      } else this.activeFocus = Object.freeze({ region: 'rail', targetId: 'home' });
      this.restoring = false; this.generation++; this.publish();
    } catch {
      if (this.disposed || ticket !== this.restoreTicket) return;
      this.restoring = false; this.storageError = 'Saved navigation could not be restored.'; this.publish();
    }
  }
  private save(): void {
    if (!this.storage) return;
    const key = navigationStorageKey(this.scope);
    const payload = JSON.stringify({ version: 1, scope: this.scope, lastLibrary: this.lastLibrary, views: [...this.views.values()], tabViews: [...this.tabViews.values()] });
    // A slow storage adapter cannot accumulate one retained snapshot per focus event.
    this.pendingWrites.delete(key);
    this.pendingWrites.set(key, { payload, scope: this.scope });
    while (this.pendingWrites.size > MAX_VIEWS) this.pendingWrites.delete(this.pendingWrites.keys().next().value!);
    if (!this.writing) {
      this.writing = true;
      this.writes = this.drainWrites();
    }
  }
  private async drainWrites(): Promise<void> {
    while (this.pendingWrites.size > 0) {
      const [key, entry] = this.pendingWrites.entries().next().value!;
      this.pendingWrites.delete(key);
      try { await this.storage!.write(key, entry.payload); }
      catch {
        if (!this.disposed && this.scope === entry.scope) { this.storageError = 'Navigation changes could not be saved.'; this.publish(); }
      }
    }
    this.writing = false;
  }
  async flush(): Promise<void> { await this.writes; }
  dispose(): void { this.disposed = true; this.restoreTicket++; this.tabHistories.clear(); this.listeners.clear(); }
}
