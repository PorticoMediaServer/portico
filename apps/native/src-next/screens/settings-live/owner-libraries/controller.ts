import {encodedRoutePath, routeBody, type RouteResponse} from '../../../../../../packages/contracts/src/routes';
import {RemoteSourceService, type AnalysisPolicy} from '../../../../../../packages/client-core/src/remote-sources';
import type {ExpectedRemoteSource} from '../../../../../../packages/client-core/src/remote-source-selection';
import {LibraryManagementService, type LibraryManagementSnapshot, type ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import {fetchEpisodeIssues, type EpisodeIssue} from '../../../../../../packages/client-core/src/library-management';
import {LibraryInventoryService, type InventoryConfig, type SourceSettings} from '../../../../../../packages/client-core/src/library-inventory';
import {readLibrarySettings, reorderLibraries, saveLibrarySettings} from '../../../../../../packages/client-core/src/server-admin/libraries';
import {readScanIssues, type ScanIssues} from '../../../../../../packages/client-core/src/scan-issues';
import {removeLibrary} from '../../../../../../packages/client-core/src/title-identity';
import {metadataAgentPath, parseMetadataAgent, saveMetadataAgent, type LibraryMetadataAgent} from '../../../../../../packages/client-core/src/server-admin/metadata-agent';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import {readLibrarySonic, retryLibrarySonic, saveLibrarySonic, type LibrarySonicDocument} from '../../../../../../packages/client-core/src/server-admin/library-sonic';
import {patchLibrarySonicSettingsBinding, readLibrarySonicSettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import type {ContentScope, LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';

import {sameUserMutationScope, UserMutationError, type UserMutationScope} from '../../../../../../packages/client-core/src/user-mutations';

type PendingSettings = {document: LibraryAdminDocument; settings: string; patch: string; operationId: string; scope?: UserMutationScope; dispatched: boolean};

export type NativeOwnerLibrariesSnapshot = Readonly<{
  directory: LibraryManagementSnapshot['directory'];
  selectedId?: string;
  selected: LibraryManagementSnapshot['selected'];
  document?: LibraryAdminDocument;
  strm?: AnalysisPolicy;
  strmError?: unknown;
  sonic?: LibrarySonicDocument;
  sonicBusy: boolean;
  sonicError?: unknown;
  sonicErrorOperation?: 'load' | 'save';
  sonicNeedsReload: boolean;
  sonicRetryUnavailable: boolean;
  inventory?: InventoryConfig;
  inventoryLoading?: boolean;
  inventoryError?: unknown;
  lyricsJob?: RouteResponse<'post_libraries_id_lyrics_fetch'>;
  lyricsError?: unknown;
  lyricsBusy?: boolean;
  metadata?: LibraryMetadataAgent;
  detail: 'idle' | 'loading' | 'ready' | 'error';
  detailError?: unknown;
  skipped?: ScanIssues;
  skippedError?: unknown;
  skippedLoading: boolean;
  unmatched: readonly EpisodeIssue[];
  unmatchedCursor: string;
  unmatchedLoading: boolean;
  unmatchedError?: unknown;
  busy: boolean;
  saved: number;
  error?: unknown;
}>;

/** An immutable page/mount command lifetime, captured when the Scanning row dispatches. */
export type SonicPageLease = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;

/** The one admission object a committed Scanning layout lease hands its rendered handlers. */
export function sonicPageLease(controller: AbortController, parentCurrent: () => boolean): SonicPageLease {
  return Object.freeze({signal: controller.signal, isCurrent: () => !controller.signal.aborted && parentCurrent()});
}

const blankDirectory: LibraryManagementSnapshot['directory'] = {phase: 'idle', data: null, error: null, cursor: null, canPrevious: false};
const blankSelected: LibraryManagementSnapshot['selected'] = {phase: 'idle', data: null, error: null, cursor: null, canPrevious: false};
const empty: NativeOwnerLibrariesSnapshot = {directory: blankDirectory, selected: blankSelected, detail: 'idle', skippedLoading: false,
  unmatched: Object.freeze([]), unmatchedCursor: '', unmatchedLoading: false, sonicBusy: false, sonicNeedsReload: false, sonicRetryUnavailable: false, busy: false, saved: 0};

/** Real owner library administration for the phone page: the same services and routes the
 * web console uses, with the screen's own lifetime. No optimistic rows and no replayed commands. */
export class NativeOwnerLibrariesController {
  private management: LibraryManagementService;
  private remotePolicy: RemoteSourceService;
  private inventory?: LibraryInventoryService;
  private api: LibraryContentApi;
  private settingsPending = new Map<string, PendingSettings>();
  private scope: ContentScope;
  private state: NativeOwnerLibrariesSnapshot = empty;
  private listeners = new Set<() => void>();
  private disposed = false;
  private generation = 0;
  private unsubscribe?: () => void;
  private unsubscribeInventory?: () => void;
  private sonicRequest?: AbortController;
  private strmRead = 0;
  private lyricsKeys = new Map<string, string>();
  private lyricsFlight = false;
  private requestId: () => string;

  constructor(api: LibraryContentApi, scope: ContentScope, requestId: () => string) {
    this.api = api; this.scope = scope; this.requestId = requestId;
    this.management = new LibraryManagementService({api, scope});
    this.remotePolicy = new RemoteSourceService(api, scope, async () => `${Date.now()}-${requestId()}`, () => !this.disposed);
    this.unsubscribe = this.management.subscribe(() => this.accept());
  }

  getSnapshot = (): NativeOwnerLibrariesSnapshot => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};

  private publish(patch: Partial<NativeOwnerLibrariesSnapshot>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }

  private accept() {
    if (this.disposed) return;
    const snapshot = this.management.getSnapshot();
    this.publish({directory: snapshot.directory, selected: snapshot.selected, ...(snapshot.selectedLibraryId ? {selectedId: snapshot.selectedLibraryId} : {})});
  }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.unsubscribe?.(); this.unsubscribeInventory?.();
    this.sonicRequest?.abort(); this.sonicRequest = undefined;
    this.inventory?.dispose(); this.management.dispose(); this.remotePolicy.dispose(); this.listeners.clear();
  }

  private command<T>(work: () => Promise<T>): Promise<T | undefined> {
    if (this.disposed) return Promise.resolve(undefined);
    this.publish({busy: true, error: undefined});
    return work().catch(error => {this.publish({error}); return undefined;}).finally(() => {this.publish({busy: false});});
  }

  async refresh(): Promise<void> {
    if (this.disposed) return;
    try {await this.management.loadLibraries();}
    catch (error) {if (!this.disposed) this.publish({error});}
    const id = this.state.selectedId;
    if (!this.disposed && id) await this.load(id);
  }

  /** Opens one library: its settings document, folders, skipped files and unmatched items. */
  async load(libraryId: string): Promise<void> {
    if (this.disposed) return;
    const generation = ++this.generation;
    this.inventory?.dispose(); this.unsubscribeInventory?.();
    this.sonicRequest?.abort(); this.sonicRequest = undefined;
    const inventory = new LibraryInventoryService({api: this.api, scope: this.scope, libraryId});
    this.inventory = inventory; this.unsubscribeInventory = inventory.subscribe(() => this.acceptInventory(generation));
    this.publish({inventory: undefined, inventoryLoading: true, inventoryError: undefined, document: undefined, lyricsJob: undefined, lyricsError: undefined, lyricsBusy: false, selectedId: libraryId, strm: undefined, strmError: undefined, detail: 'loading', detailError: undefined, skipped: undefined, skippedError: undefined, unmatched: Object.freeze([]), unmatchedCursor: '', sonic: undefined, sonicError: undefined, sonicErrorOperation: undefined, sonicNeedsReload: false, sonicBusy: false, sonicRetryUnavailable: false});
    try {
      await this.management.selectLibrary(libraryId);
      if (this.disposed || generation !== this.generation) return;
      const [document, , , metadata] = await Promise.all([readLibrarySettings(this.api, libraryId), inventory.refresh(), this.loadUnmatched(libraryId, '', generation),
        this.api.request<unknown>(metadataAgentPath(libraryId)).then(parseMetadataAgent, () => undefined)]);
      if (this.disposed || generation !== this.generation) return;
      this.publish({document, metadata, detail: 'ready'});
      void this.loadSonic(document, generation);
      void this.loadStrm(document, generation);
      await this.loadSkipped(document, generation);
    } catch (error) {
      if (!this.disposed && generation === this.generation) this.publish({detail: 'error', detailError: error});
    }
  }

  /** STRM analysis is a separate revisioned resource, never a LibrarySettings field. */
  private async loadStrm(document: LibraryAdminDocument, generation: number): Promise<void> {
    if (!['movie', 'tv', 'anime'].includes(document.libraryKind)) return;
    const read = ++this.strmRead;
    try {
      const strm = await this.remotePolicy.policy(document.libraryId);
      if (!this.disposed && read === this.strmRead && this.generation === generation && this.state.selectedId === document.libraryId) this.publish({strm, strmError: undefined});
    } catch (error) {
      if (!this.disposed && read === this.strmRead && this.generation === generation && this.state.selectedId === document.libraryId) this.publish({strmError: error});
    }
  }

  async setStrm(enabled: boolean, document: LibraryAdminDocument, policy: AnalysisPolicy, lease: SonicPageLease): Promise<void> {
    const generation = this.generation;
    const current = () => !this.disposed && this.generation === generation && this.state.document === document && this.state.strm === policy && lease.isCurrent();
    if (this.state.busy || this.state.strmError !== undefined || policy.libraryId !== document.libraryId || !current()) return;
    this.strmRead++;
    this.publish({busy: true, strmError: undefined});
    try {
      const strm = await this.remotePolicy.setPolicy(policy, enabled);
      if (current()) this.publish({strm});
      else if (!this.disposed && this.generation === generation && this.state.strm === policy) this.publish({strmError: {code: 'refresh_failed'}});
    } catch (error) {if (!this.disposed && this.generation === generation && this.state.strm === policy) this.publish({strmError: error});}
    finally {if (!this.disposed && this.generation === generation) this.publish({busy: false});}
  }

  reloadStrm(document: LibraryAdminDocument): Promise<void> {
    return this.state.document === document && !this.disposed && !this.state.busy ? this.loadStrm(document, this.generation) : Promise.resolve();
  }

  /** Sonic status is its own original-owner read, separate from the library document and never its permission. */
  private async loadSonic(document: LibraryAdminDocument, generation: number): Promise<void> {
    if (document.libraryKind !== 'music' || this.disposed || generation !== this.generation || this.state.selectedId !== document.libraryId || this.state.document !== document) return;
    const request = new AbortController();
    this.sonicRequest?.abort(); this.sonicRequest = request;
    try {
      const sonic = await readLibrarySonic(this.api, document.libraryId, request.signal);
      if (this.ownsSonic(request, document, generation)) this.publish({sonic, sonicError: undefined, sonicErrorOperation: undefined, sonicNeedsReload: false});
    } catch (error) {
      if (this.ownsSonic(request, document, generation)) this.publish({sonic: undefined, sonicError: error, sonicErrorOperation: 'load'});
    } finally {
      if (this.sonicRequest === request) this.sonicRequest = undefined;
    }
  }

  private ownsSonic(request: AbortController, document: LibraryAdminDocument, generation: number): boolean {
    return !this.disposed && !request.signal.aborted && this.sonicRequest === request && generation === this.generation && this.state.selectedId === document.libraryId && this.state.document === document;
  }

  /** Toggle the library's own Sonic setting at that document's revision; no optimistic state, stale page or stale document. */
  async setSonicEnabled(enabled: boolean, document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease): Promise<boolean> {
    const generation = this.generation;
    if (!this.admitsSonic(document, sonic, generation, lease) || !readLibrarySonicSettingsBinding('libraries.sonicAnalysis', document, sonic)) return false;
    const patch = patchLibrarySonicSettingsBinding('libraries.sonicAnalysis', enabled, document, sonic);
    return this.sonicCommand(document, sonic, generation, lease, signal => saveLibrarySonic(this.api, sonic, patch.enabled, signal), true);
  }

  /** Retry the server's own runtime probe for the original library; the response, never optimism, sets the result. */
  async retrySonic(document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease): Promise<boolean> {
    const generation = this.generation;
    if (!this.admitsSonic(document, sonic, generation, lease)) return false;
    return this.sonicCommand(document, sonic, generation, lease, signal => retryLibrarySonic(this.api, document.libraryId, signal), false);
  }

  /** Recover an unadopted outcome with one ordinary GET, never a late POST or retry. */
  async reloadSonic(document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease): Promise<boolean> {
    const generation = this.generation;
    if (!this.residentSonic(document, sonic, generation, lease) || this.state.sonicBusy) return false;
    this.publish({sonicBusy: true, sonicError: undefined, sonicErrorOperation: undefined});
    const request = new AbortController(), unlink = this.linkLease(request, lease);
    this.sonicRequest?.abort(); this.sonicRequest = request;
    try {
      const next = await readLibrarySonic(this.api, document.libraryId, request.signal);
      if (!this.sonicRequestIs(request, document, generation)) return false;
      if (!lease.isCurrent()) {this.publish({sonicBusy: false, sonicNeedsReload: true}); return false;}
      this.publish({sonic: next, sonicBusy: false, sonicNeedsReload: false, sonicError: undefined, sonicErrorOperation: undefined});
      return true;
    } catch (error) {
      if (this.sonicRequestIs(request, document, generation)) this.publish({sonicBusy: false, sonicError: error, sonicErrorOperation: 'load'});
      return false;
    } finally {unlink(); if (this.sonicRequest === request) this.sonicRequest = undefined;}
  }

  private async sonicCommand(document: LibraryAdminDocument, sonic: LibrarySonicDocument, generation: number, lease: SonicPageLease, run: (signal: AbortSignal) => Promise<LibrarySonicDocument>, save: boolean): Promise<boolean> {
    const request = new AbortController(), unlink = this.linkLease(request, lease);
    this.sonicRequest?.abort(); this.sonicRequest = request;
    this.publish({sonicBusy: true, sonicError: undefined, sonicErrorOperation: undefined, sonicRetryUnavailable: false});
    try {
      const next = await run(request.signal);
      if (!this.sonicRequestIs(request, document, generation)) return false;
      if (!lease.isCurrent()) {this.publish({sonicBusy: false, sonicNeedsReload: true}); return false;}
      this.publish({sonic: next, sonicBusy: false, sonicNeedsReload: false, ...(save ? {} : {sonicRetryUnavailable: !next.available || next.state === 'unavailable'})});
      return true;
    } catch (error) {
      if (this.sonicRequestIs(request, document, generation)) {
        this.publish({sonicBusy: false, sonicError: error, sonicErrorOperation: save ? 'save' : 'save', ...(save ? {sonicNeedsReload: true} : {})});
      }
      return false;
    } finally {unlink(); if (this.sonicRequest === request) this.sonicRequest = undefined;}
  }

  private linkLease(request: AbortController, lease: SonicPageLease): () => void {
    const abort = () => request.abort();
    if (lease.signal.aborted) request.abort(); else lease.signal.addEventListener('abort', abort, {once: true});
    return () => lease.signal.removeEventListener('abort', abort);
  }

  /** This exact request still owns the original resident row, regardless of a page hide. */
  private sonicRequestIs(request: AbortController, document: LibraryAdminDocument, generation: number): boolean {
    return !this.disposed && this.sonicRequest === request && generation === this.generation && this.state.selectedId === document.libraryId && this.state.document === document;
  }

  /** A once-per-render read of the exact resident row and page witness; recovery allows a needs-reload row. */
  private residentSonic(document: LibraryAdminDocument, sonic: LibrarySonicDocument, generation: number, lease: SonicPageLease): boolean {
    return !this.disposed && !lease.signal.aborted && lease.isCurrent() && generation === this.generation &&
      this.state.selectedId === document.libraryId && this.state.document === document && this.state.sonic === sonic;
  }

  /** The original committed row's document and Sonic object, admitted before any mutation is made. */
  private admitsSonic(document: LibraryAdminDocument, sonic: LibrarySonicDocument, generation: number, lease: SonicPageLease): boolean {
    return this.residentSonic(document, sonic, generation, lease) && !this.state.sonicBusy && !this.state.sonicNeedsReload;
  }

  private acceptInventory(generation: number) {
    if (this.disposed || generation !== this.generation || !this.inventory) return;
    const snapshot = this.inventory.getSnapshot();
    this.publish({inventory: snapshot.error || snapshot.loading ? undefined : snapshot.config ?? undefined, inventoryLoading: snapshot.loading, inventoryError: snapshot.error ?? undefined});
  }

  async reloadInventory(): Promise<void> {
    if (this.disposed || !this.inventory || this.state.inventoryLoading) return;
    await this.inventory.refresh();
  }

  /** Keep the operation key after an uncertain answer; a repeat gesture recovers the same job. */
  async fetchLyrics(document: LibraryAdminDocument, current: () => boolean): Promise<void> {
    const generation = this.generation;
    const owns = () => !this.disposed && generation === this.generation && this.state.document === document && this.state.selectedId === document.libraryId && current();
    if (!owns() || this.lyricsFlight || this.state.busy || document.libraryKind !== 'music' || document.settings.lyricsSource !== 'lrclib' || !document.eligibleControls.includes('libraries.lyrics')) return;
    const key = this.lyricsKeys.get(document.libraryId) ?? this.requestId();
    this.lyricsKeys.set(document.libraryId, key); this.lyricsFlight = true;
    this.publish({lyricsBusy: true, lyricsError: undefined, lyricsJob: undefined});
    try {
      if (!owns()) return;
      const job = await this.api.request<RouteResponse<'post_libraries_id_lyrics_fetch'>>(encodedRoutePath('post_libraries_id_lyrics_fetch', {id: encodeURIComponent(document.libraryId)}), 'POST', routeBody('post_libraries_id_lyrics_fetch', {idempotencyKey: key}));
      if (!job || typeof job.id !== 'string' || !job.id || typeof job.state !== 'string') throw new Error('Invalid lyrics job response.');
      this.lyricsKeys.delete(document.libraryId);
      if (!owns()) return;
      this.publish({lyricsJob: job});
      // Reconcile the selected library, without converting an accepted job into a second POST.
      if (!owns()) return;
      await this.management.selectLibrary(document.libraryId);
    } catch (error) {if (owns()) this.publish({lyricsError: error});}
    finally {this.lyricsFlight = false; if (!this.disposed && generation === this.generation) this.publish({lyricsBusy: false});}
  }

  /** The last finished scan's own diagnostics, narrowed by the screen to files Portico cannot play. */
  private async loadSkipped(document: LibraryAdminDocument, generation: number): Promise<void> {
    const job = this.management.getSnapshot().selected.data?.library.lastScan;
    if (!job?.id) {this.publish({skipped: undefined}); return;}
    this.publish({skippedLoading: true, skippedError: undefined});
    try {
      const page = await readScanIssues(this.api, document.libraryId, job.id);
      if (this.disposed || generation !== this.generation) return;
      this.publish({skipped: page, skippedLoading: false});
    } catch (error) {
      if (!this.disposed && generation === this.generation) this.publish({skippedError: error, skippedLoading: false});
    }
  }

  async loadSkippedMore(): Promise<void> {
    const current = this.state.skipped, job = this.management.getSnapshot().selected.data?.library.lastScan;
    if (!current?.hasMore || !job?.id || this.state.skippedLoading) return;
    const generation = this.generation;
    this.publish({skippedLoading: true});
    try {
      const next = await readScanIssues(this.api, this.state.selectedId ?? '', job.id, current.nextSequence);
      if (this.disposed || generation !== this.generation) return;
      this.publish({skipped: Object.freeze({...next, items: Object.freeze([...current.items, ...next.items])}), skippedLoading: false});
    } catch (error) {
      if (!this.disposed && generation === this.generation) this.publish({skippedError: error, skippedLoading: false});
    }
  }

  private async loadUnmatched(libraryId: string, cursor: string, generation: number): Promise<void> {
    this.publish({unmatchedLoading: true, unmatchedError: undefined});
    try {
      const page = await fetchEpisodeIssues(this.api, libraryId, cursor);
      if (this.disposed || generation !== this.generation) return;
      this.publish({unmatched: Object.freeze([...this.state.unmatched, ...page.issues]), unmatchedCursor: page.nextCursor, unmatchedLoading: false});
    } catch (error) {
      if (!this.disposed && generation === this.generation) this.publish({unmatchedError: error, unmatchedLoading: false});
    }
  }

  async loadUnmatchedMore(): Promise<void> {
    const libraryId = this.state.selectedId, cursor = this.state.unmatchedCursor;
    if (!libraryId || !cursor || this.state.unmatchedLoading) return;
    await this.loadUnmatched(libraryId, cursor, this.generation);
  }

  scanNow(): Promise<void | undefined> {return this.command(() => this.management.scan());}

  /** A scan started from the list: select the library the server must scan first. */
  async scanLibrary(libraryId: string): Promise<void> {
    const done = await this.command(() => this.management.selectLibrary(libraryId).then(() => this.management.scan()));
    if (done !== undefined) await this.refresh();
  }

  scanAll(): Promise<void | undefined> {return this.command(() => this.management.scan('full'));}

  /** Creates the library from the real form, then opens it so its first scan is visible in place. */
  async create(input: Readonly<{name: string; kind: ManagedLibrary['kind']; folders: readonly string[]; expectedRemoteSource?: ExpectedRemoteSource; metadataAgent?: 'online' | 'local'; metadataLanguage?: string}>): Promise<string | undefined> {
    const done = await this.command(async () => {await this.management.createLibrary(input); return this.management.getSnapshot().selectedLibraryId ?? undefined;});
    if (done) {await this.refresh(); await this.load(done);}
    return done ?? undefined;
  }

  cancelScan(): Promise<void | undefined> {
    const job = this.state.selected.data?.library.lastScan;
    return job ? this.command(() => this.management.cancelJob(job.id)) : Promise.resolve(undefined);
  }

  rename(name: string): Promise<void | undefined> {
    const library = this.state.selected.data?.library;
    return library ? this.command(() => this.management.rename(name, library.revision)) : Promise.resolve(undefined);
  }

  async remove(confirmation: string): Promise<boolean> {
    const library = this.state.selected.data?.library;
    if (!library) return false;
    const done = await this.command(() => removeLibrary(this.api, library.id, confirmation, library.revision).then(() => true));
    if (done) {this.publish({selectedId: undefined, document: undefined, detail: 'idle'}); await this.refresh();}
    return done === true;
  }

  /** One save of the library's own settings document; the whole document goes back with its revision. */
  async save(patch: Partial<LibrarySettings>): Promise<boolean> {
    const libraryId = this.state.selectedId, generation = this.generation;
    if (!libraryId || this.disposed || this.state.busy) return false;
    const requireCurrent = () => {if (this.disposed || generation !== this.generation || this.state.selectedId !== libraryId) throw new UserMutationError('mutation_scope_changed');};
    const done = await this.command(async () => {
      let pending = this.settingsPending.get(libraryId);
      if (pending?.dispatched && pending.scope && this.api.awaitUserMutationsReady && this.api.getUserMutationCoordinator && this.api.getUserMutationScope) {
        requireCurrent(); await this.api.awaitUserMutationsReady?.(); requireCurrent();
        const scope = this.api.getUserMutationScope(); requireCurrent();
        if (!scope || !sameUserMutationScope(scope, pending.scope)) throw new UserMutationError('mutation_scope_changed');
        const coordinator = this.api.getUserMutationCoordinator(); requireCurrent();
        const retained = coordinator && await coordinator.inspectIntent(pending.operationId); requireCurrent();
        if (coordinator && !retained) {
          this.settingsPending.delete(libraryId); pending = undefined;
        }
      }
      const draft = JSON.stringify(patch);
      if (pending && pending.patch !== draft) throw new UserMutationError('mutation_needs_review');
      if (!pending) {
        const document = this.state.document;
        if (!document || document.libraryId !== libraryId) return false;
        requireCurrent(); const scope = this.api.getUserMutationScope?.(); requireCurrent();
        pending = {document: JSON.parse(JSON.stringify(document)), settings: JSON.stringify({...document.settings, ...patch}), patch: draft, operationId: this.requestId(), scope: scope && {...scope}, dispatched: false};
        this.settingsPending.set(libraryId, pending);
      }
      requireCurrent();
      const api: LibraryContentApi = {
        ...(this.api.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
          requireCurrent(); await this.api.awaitUserMutationsReady!(); requireCurrent();
        }} : {}),
        ...(this.api.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
          requireCurrent(); const value = this.api.getUserMutationCoordinator!(); requireCurrent(); return value;
        }} : {}),
        ...(this.api.getUserMutationScope ? {getUserMutationScope: () => {
          requireCurrent(); const value = this.api.getUserMutationScope!(); requireCurrent(); return value;
        }} : {}),
        request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
          requireCurrent(); pending!.dispatched = true;
          const value = await this.api.request<T>(path, method, body, signal); requireCurrent(); return value;
        },
        ...(this.api.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
          requireCurrent(); await this.api.acknowledgeUserMutation!(id); requireCurrent();
        }} : {}),
      };
      const value = await saveLibrarySettings(api, pending.document, JSON.parse(pending.settings) as LibrarySettings, pending.operationId);
      requireCurrent(); this.settingsPending.delete(libraryId);
      this.publish({document: value, saved: this.state.saved + 1}); return true;
    });
    return done === true;
  }

  /** The metadata source is its own resource with its own revision; a conflict re-reads rather than retries blind. */
  async saveAgent(agent: string): Promise<boolean> {
    const current = this.state.metadata;
    if (!current) return false;
    const request = <T,>(path: string, method?: string, body?: unknown) => this.api.request<T>(path, method, body);
    const done = await this.command(() => saveMetadataAgent(request, current, current.libraryId, agent).then(value => {this.publish({metadata: value, saved: this.state.saved + 1}); return true;}));
    if (done !== true) await this.reloadAgent();
    return done === true;
  }

  private async reloadAgent(): Promise<void> {
    const id = this.state.selectedId;
    if (!id) return;
    try {const metadata = parseMetadataAgent(await this.api.request<unknown>(metadataAgentPath(id))); this.publish({metadata});}
    catch (error) {this.publish({error});}
  }

  async saveFolder(sourceId: string | undefined, input: SourceSettings): Promise<boolean> {
    const inventory = this.inventory;
    if (!inventory) return false;
    const done = await this.command(() => inventory.saveSource(sourceId, input).then(value => value));
    return done === true;
  }

  async removeFolder(sourceId: string): Promise<boolean> {
    const revision = this.state.inventory?.revision;
    if (!revision) return false;
    const inventory = this.inventory;
    const done = await this.command(() => inventory ? inventory.removeSource(sourceId, revision) : Promise.resolve(false));
    return done === true;
  }

  /** The owner's order is one server command; dragging is not drawn on the phone, the menu moves. */
  async move(libraryId: string, by: -1 | 1): Promise<boolean> {
    const ids = this.libraries().map(library => library.id), from = ids.indexOf(libraryId), to = from + by;
    if (from < 0 || to < 0 || to >= ids.length) return false;
    const next = [...ids]; const [moved] = next.splice(from, 1); next.splice(to, 0, moved!);
    const done = await this.command(() => reorderLibraries(this.api, next).then(() => true));
    if (done) await this.refresh();
    return done === true;
  }

  async scanFolder(sourceId: string): Promise<boolean> {
    const inventory = this.inventory;
    const done = await this.command(() => inventory ? inventory.scanSource(sourceId) : Promise.resolve(false));
    return done === true;
  }

  async checkFolder(sourceId: string): Promise<boolean> {
    const inventory = this.inventory;
    const done = await this.command(() => inventory ? inventory.checkSource(sourceId) : Promise.resolve(false));
    return done === true;
  }

  /** The visible list, in the server's order: the directory page carries it already. */
  libraries(): readonly ManagedLibrary[] {return this.state.directory.data?.items ?? [];}
}
