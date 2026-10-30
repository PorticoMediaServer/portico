import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import {saveChannelPreference} from '../../../../../packages/client-core/src/channel-guide';
import {fetchGuideSourceSummaries, type GuideSourceSummary} from '../../../../../packages/client-core/src/guide/directory';
import {legacyChannel, legacyChannelRow, legacyGuideSource, type LegacyGuideOptions} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {GuideChannelRow} from '../../../../../packages/client-core/src/guide/types';
import {GuideWindowStore, type GuideViewport} from '../../../../../packages/client-core/src/guide/store';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from './lifetime';
import type {DVRCommandLifetime} from '../../../../../packages/client-core/src/dvr';
import {nativeGuideEntryKey, type NativeGuideChoice, type NativeGuideMemoryPort} from './guide-memory';

export type NativeLiveSnapshot = Readonly<{sources: readonly GuideSourceSummary[]; loading: boolean; error?: unknown; options: LegacyGuideOptions; view?: 'guide' | 'list'; revision: number; mutating: boolean; mutationError?: unknown}>;
type SourceChoice = Readonly<{view?: 'guide' | 'list'; sort?: LegacyGuideOptions['sort']}>;
export class NativeLiveController extends NativeScreenStore<NativeLiveSnapshot> {
  private store: GuideWindowStore;
  private viewport?: GuideViewport;
  private disposed = false;
  private refreshSources?: Promise<void>;
  private operationKeys = new Map<string, Promise<string>>();
  private sourceChoices = new Map<string, SourceChoice>();
  private memory?: Readonly<{port: NativeGuideMemoryPort; reportError: (error: unknown) => void}>;
  private memoryReady: boolean;
  private memoryRead?: Promise<void>;
  readonly api: LibraryContentApi;
  readonly lifetime: NativeScreenLifetime;
  constructor(lifetime: NativeScreenLifetime, api: LibraryContentApi, timezone: string, memory?: NativeLiveController['memory']) {
    const options: LegacyGuideOptions = {kind: 'all', sourceId: '', timezone, sort: 'number'};
    super({sources: [], loading: true, options, revision: 0, mutating: false});
    this.lifetime = lifetime; this.memory = memory; this.memoryReady = !memory;
    const requireCurrent = () => {lifetime.assert(); if (this.disposed) throw screenScopeEnded();};
    this.api = {
      ...(api.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
        requireCurrent(); await api.awaitUserMutationsReady!(); requireCurrent();
      }} : {}),
      ...(api.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
        requireCurrent(); const value = api.getUserMutationCoordinator!(); requireCurrent(); return value;
      }} : {}),
      ...(api.getUserMutationScope ? {getUserMutationScope: () => {
        requireCurrent(); const value = api.getUserMutationScope!(); requireCurrent(); return value;
      }} : {}),
      ...(api.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
        requireCurrent(); await api.acknowledgeUserMutation!(id); requireCurrent();
      }} : {}),
      request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        requireCurrent(); const value = await lifetime.request(next => api.request<T>(path, method, body, next), signal); requireCurrent(); return value;
      }};
    this.store = this.makeStore(options);
  }
  private makeStore(options: LegacyGuideOptions) {
    return new GuideWindowStore({source: legacyGuideSource(this.api, this.lifetime.authority.serverId, options), maxInFlight: 2, maxChannelPages: 4, maxTiles: 16});
  }
  get guide(): GuideWindowStore {return this.store;}
  private sourceKey(options: LegacyGuideOptions): string {return nativeGuideEntryKey(options);}
  private remember(options: LegacyGuideOptions, choice: SourceChoice): void {
    const key = this.sourceKey(options), next = {...this.sourceChoices.get(key), ...choice};
    this.sourceChoices.delete(key); this.sourceChoices.set(key, next);
    while (this.sourceChoices.size > 50) this.sourceChoices.delete(this.sourceChoices.keys().next().value!);
  }
  private persist(options: LegacyGuideOptions, choice: NativeGuideChoice): void {
    if (this.memory) void Promise.resolve().then(() => this.memory!.port.write(options, choice)).catch(error => {if (!this.disposed && this.lifetime.current()) this.memory?.reportError(error);});
  }
  private prepareMemory(): Promise<void> {
    if (this.memoryReady || !this.memory) return Promise.resolve();
    if (this.memoryRead) return this.memoryRead;
    return this.memoryRead = (async () => {
      try {
        const stored = await this.memory!.port.read();
        if (this.disposed || !this.lifetime.current()) return;
        // User choices made during the native read win field by field. The read
        // supplies the other field, without resetting the user's current source.
        const choices = new Map<string, SourceChoice>(stored);
        for (const [key, choice] of this.sourceChoices) {const merged = {...choices.get(key), ...choice}; choices.delete(key); choices.set(key, merged);}
        this.sourceChoices = new Map([...choices].slice(-50));
        const state = this.getSnapshot(), choice = this.sourceChoices.get(this.sourceKey(state.options));
        if (choice) {
          const options = {...state.options, sort: choice.sort ?? state.options.sort}, changed = options.sort !== state.options.sort;
          if (changed) {this.store.dispose(); this.store = this.makeStore(options);}
          this.publish({...state, options, view: choice.view ?? state.view, revision: state.revision + (changed ? 1 : 0)});
        }
      } catch (error) {if (!this.disposed && this.lifetime.current()) this.memory?.reportError(error);}
      finally {if (!this.disposed && this.lifetime.current()) {this.memoryReady = true; if (this.viewport) this.store.setViewport(this.viewport);}}
    })();
  }
  chooseView(view: 'guide' | 'list'): void {
    this.lifetime.assert(); if (this.disposed) return;
    const state = this.getSnapshot(); this.remember(state.options, {view}); this.publish({...state, view}); this.persist(state.options, {view});
  }
  setViewport(viewport: GuideViewport): void {if (this.disposed || !this.lifetime.current()) return; this.viewport = viewport; if (this.memoryReady) this.store.setViewport(viewport);}
  select(change: Partial<Pick<LegacyGuideOptions, 'kind' | 'sourceId' | 'group' | 'favorites' | 'sort' | 'includeServerHidden'>>): void {
    this.lifetime.assert();
    if (this.disposed) return;
    this.store.dispose();
    const state = this.getSnapshot(), options = {...state.options, ...change};
    const sourceChanged = this.sourceKey(options) !== this.sourceKey(state.options);
    if (sourceChanged && change.sort === undefined) options.sort = this.sourceChoices.get(this.sourceKey(options))?.sort ?? 'number';
    const view = sourceChanged ? this.sourceChoices.get(this.sourceKey(options))?.view : state.view;
    if (change.sort !== undefined) {this.remember(options, {sort: change.sort}); if (change.sort === 'number' || change.sort === 'name') this.persist(options, {sort: change.sort});}
    this.store = this.makeStore(options);
    this.publish({...state, options, view, revision: state.revision + 1});
    if (this.viewport) {this.viewport = {...this.viewport, firstRow: 0, lastRow: this.viewport.lastRow - this.viewport.firstRow}; if (this.memoryReady) this.store.setViewport(this.viewport);}
    // Showing or hiding server-hidden channels changes the sources and groups too.
    if (change.includeServerHidden !== undefined && !!change.includeServerHidden !== !!state.options.includeServerHidden) {this.refreshSources = undefined; void this.loadSources();}
  }
  loadSources(): Promise<void> {
    if (this.refreshSources) return this.refreshSources;
    this.lifetime.assert();
    const hidden = !!this.getSnapshot().options.includeServerHidden;
    const flight: Promise<void> = (async () => {
      try {
        await this.prepareMemory(); this.lifetime.assert(); if (this.disposed) return;
        const result = await fetchGuideSourceSummaries(this.api, this.lifetime.authority.serverId, this.getSnapshot().options.timezone, this.lifetime.signal, hidden);
        // A summary read for the other Show hidden choice is never published.
        if (!this.disposed && this.lifetime.current() && !!this.getSnapshot().options.includeServerHidden === hidden) this.publish({...this.getSnapshot(), sources: result.sources, loading: false, error: undefined});
      } catch (error) {if (!this.disposed && this.lifetime.current() && !!this.getSnapshot().options.includeServerHidden === hidden) this.publish({...this.getSnapshot(), loading: false, error});}
    })().finally(() => {if (this.refreshSources === flight) this.refreshSources = undefined;});
    return this.refreshSources = flight;
  }
  clearMutationError(): void {if (!this.disposed && this.lifetime.current()) this.publish({...this.getSnapshot(), mutationError: undefined});}
  /** The native crypto port supplies the existing 48hex protocol key. An unknown outcome
   * retains its exact key and expected revision; never rebase or optimistically toggle it. */
  async channelPreference(channel: GuideChannelRow, values: Readonly<{favorite: boolean; hidden: boolean}>, requestId: () => Promise<string>, lifetime?: DVRCommandLifetime): Promise<GuideChannelRow | undefined> {
    const original = lifetime ? Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent}) : undefined;
    const capturedValues = original ? Object.freeze({favorite: values.favorite, hidden: values.hidden}) : values;
    const requireCommand = () => {if (original && (this.disposed || !this.lifetime.current() || original.signal.aborted || !original.isCurrent())) throw screenScopeEnded();};
    requireCommand();
    this.lifetime.assert();
    if (this.disposed || this.getSnapshot().mutating) return undefined;
    const native = legacyChannel(channel);
    if (!native) throw new Error('native_channel_record_missing');
    const raw = original ? Object.freeze({...native}) : native;
    const key = JSON.stringify([raw.provenance, raw.sourceId, raw.id, raw.generation, raw.preferenceRevision, capturedValues]);
    this.publish({...this.getSnapshot(), mutating: true, mutationError: undefined});
    try {
      requireCommand();
      let operation = this.operationKeys.get(key);
      if (!operation) {
        // Only unresolved intents occupy slots; a failed generator never occupies one.
        if (this.operationKeys.size >= 16) throw new Error('native_channel_receipt_limit');
        operation = requestId(); this.operationKeys.set(key, operation);
        const pending = operation;
        operation.catch(() => {if (this.operationKeys.get(key) === pending) this.operationKeys.delete(key);});
      }
      let operationId: string;
      try {operationId = await operation;} catch (error) {requireCommand();throw error;}
      requireCommand();
      const api: LibraryContentApi = original ? {request: async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
        requireCommand();
        try {const value = await this.lifetime.request(signal => this.api.request<T>(path, method, body, signal), original.signal);requireCommand();return value;}
        catch (error) {requireCommand();throw error;}
      },
      ...(this.api.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {requireCommand(); await this.api.awaitUserMutationsReady!(); requireCommand();}} : {}),
      ...(this.api.getUserMutationScope ? {getUserMutationScope: () => {requireCommand(); const value = this.api.getUserMutationScope!(); requireCommand(); return value;}} : {}),
      ...(this.api.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {requireCommand(); const value = this.api.getUserMutationCoordinator!(); requireCommand(); return value;}} : {}),
      ...(this.api.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
        requireCommand(); await this.api.acknowledgeUserMutation!(id); requireCommand();
      }} : {})} : this.api;
      const revision = await saveChannelPreference(api, this.lifetime.authority.serverId, raw, operationId, capturedValues, original?.signal ?? this.lifetime.signal);
      requireCommand();
      this.lifetime.assert();
      if (this.disposed) return undefined;
      this.operationKeys.delete(key);
      const committed = legacyChannelRow({...raw, ...capturedValues, preferenceRevision: revision});
      this.store.reset();requireCommand();if (!original) void this.loadSources();
      requireCommand();
      return committed;
    } catch (mutationError) {requireCommand();if (!this.disposed && this.lifetime.current()) this.publish({...this.getSnapshot(), mutationError});requireCommand();if (original) throw mutationError;return undefined;}
    finally {if (!this.disposed && this.lifetime.current()) this.publish({...this.getSnapshot(), mutating: false});requireCommand();}
  }
  dispose(): void {if (this.disposed) return; this.disposed = true; this.store.dispose(); this.operationKeys.clear(); this.sourceChoices.clear(); this.clearListeners();}
}
