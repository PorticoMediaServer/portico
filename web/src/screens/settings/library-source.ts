import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import type {LibraryAdminDocument, LibrarySettings} from '@core/administration.ts';
import {defaultI18n} from '@i18n';
import {LibraryManagementService} from '@core/library-management.ts';
import type {ContentScope, LibraryContentApi} from '@core/library-content.ts';
import {patchLibrarySettingsBinding, readLibrarySettingsBinding, patchLibrarySonicSettingsBinding, readLibrarySonicSettingsBinding} from '@core/presentation/settings-bindings.ts';
import {randomId} from '@core/random-id.ts';
import {readLibrarySettings, saveLibrarySettings} from '@core/server-admin/libraries.ts';
import {readLibrarySonic, retryLibrarySonic, saveLibrarySonic, type LibrarySonicDocument} from '@core/server-admin/library-sonic.ts';

import {sameUserMutationScope, UserMutationError, type UserMutationScope} from '@core/user-mutations.ts';

type PendingSettings = {document: LibraryAdminDocument; settings: string; draft: string; operationId: string; scope?: UserMutationScope; dispatched: boolean};

export type SettingsLibrarySnapshot = Readonly<{libraryId?: string; document?: LibraryAdminDocument; sonic?: LibrarySonicDocument; loading: boolean; saving: boolean; error?: unknown}>;
const empty: SettingsLibrarySnapshot = Object.freeze({loading: false, saving: false});

/** One explicitly selected library. A settings revision never becomes a folder revision. */
export class SettingsLibrarySource {
  private state = empty;
  private request?: AbortController;
  private pending?: Promise<void>;
  private closed = false;
  private settingsPending = new Map<string, PendingSettings>();
  private quietSerial = 0;
  private readonly quietReads = new Map<string,AbortController>();
  invalidateQuiet = () => {++this.quietSerial;for(const read of this.quietReads.values())read.abort();this.quietReads.clear();};
  quietBlocked = () => this.state.loading || this.state.saving || !!this.state.libraryId && this.settingsPending.has(this.state.libraryId) || !!this.state.error && !this.state.document;
  private async quiet(kind:'settings'|'sonic'):Promise<QuietRefreshOutcome> {
    if(!this.current() || this.quietBlocked())return {kind:'deferred'};
    const {libraryId,document,sonic}=this.state;
    if(!libraryId || !document || kind==='sonic' && !this.offersSonic(document))return {kind:'adopted'};
    this.quietReads.get(kind)?.abort();const read=new AbortController();this.quietReads.set(kind,read);const serial=this.quietSerial;
    const owns=()=>this.current() && !read.signal.aborted && this.quietReads.get(kind)===read && serial===this.quietSerial && this.state.libraryId===libraryId && !this.quietBlocked() && (kind==='settings'?this.state.document===document:this.state.sonic===sonic);
    try {
      if(kind==='settings'){
        const next=await readLibrarySettings(this.api,libraryId,read.signal);
        if(!owns())return {kind:'deferred'};this.publish({...this.state,document:next});
      }else{
        const next=await readLibrarySonic(this.api,libraryId,read.signal);
        if(!owns())return {kind:'deferred'};this.publish({...this.state,sonic:next});
      }
      return {kind:'adopted'};
    }catch(error){if(!owns())return {kind:'deferred'};if([401,403].includes((error as {status?:number})?.status??0))this.reportError(error);return {kind:'unreadable',error};}
    finally{if(this.quietReads.get(kind)===read)this.quietReads.delete(kind);}
  }
  refreshSettings = () => this.quiet('settings');
  refreshSonic = () => this.quiet('sonic');
  private readonly management?: LibraryManagementService;
  constructor(private readonly api: LibraryContentApi, private readonly isCurrent: () => boolean, private readonly notify: () => void, private readonly reportError: (error: unknown) => void, scope?: ContentScope) {
    if (scope) this.management = new LibraryManagementService({api, scope});
  }
  private current = () => !this.closed && this.isCurrent();
  getSnapshot = () => this.current() ? this.state : empty;
  document = (libraryId?: string) => libraryId && this.current() && this.state.libraryId === libraryId ? this.state.document : undefined;
  private offersSonic = (document: LibraryAdminDocument) => document.eligibleControls.includes('libraries.sonicAnalysis');
  private publish(state: SettingsLibrarySnapshot) {if (this.current()) {this.state = Object.freeze(state);this.notify();}}
  private owns(request: AbortController, libraryId: string) {return this.current() && !request.signal.aborted && this.request === request && this.state.libraryId === libraryId;}
  select = (libraryId?: string): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (libraryId && this.state.libraryId === libraryId) {
      if (this.pending) return this.pending;
      if (this.state.document) return Promise.resolve();
    }
    this.invalidateQuiet();this.request?.abort();this.pending = undefined;
    if (!libraryId) {this.publish(empty);return Promise.resolve();}
    this.invalidateQuiet();const request = new AbortController();this.request = request;
    this.publish({libraryId, loading: true, saving: false});
    const pending = readLibrarySettings(this.api, libraryId, request.signal).then(async document => {
      if (!this.owns(request, libraryId)) return;
      const sonic = this.offersSonic(document) ? await readLibrarySonic(this.api, libraryId, request.signal) : undefined;
      if (this.owns(request, libraryId)) this.publish({libraryId, document, sonic, loading: false, saving: false});
    }).catch(error => {
      if (this.owns(request, libraryId)) {this.publish({libraryId, loading: false, saving: false, error});this.reportError(error);}
    }).finally(() => {if (this.request === request) {this.request = undefined;this.pending = undefined;}});
    this.pending = pending;return pending;
  };
  read = (libraryId: string | undefined, rowId: string) => {
    const document = this.document(libraryId);
    if (document?.eligibleControls.includes(rowId)) {
      const current = rowId === 'libraries.name' ? document.libraryName : rowId === 'libraries.type'
        ? defaultI18n.t(('next.ownerLibraries.kind.' + document.libraryKind) as Parameters<typeof defaultI18n.t>[0])
        : rowId === 'libraries.access' ? defaultI18n.t(document.audience.everyone ? 'next.ownerLibraries.audience' : 'next.ownerLibraries.audienceSubset', {count: document.audience.accounts}) : undefined;
      if (current !== undefined) return {current, effective: current, default: undefined, source: 'library', configOnly: false};
    }
    if (rowId === 'libraries.sonicAnalysis') {
      const sonic = this.state.sonic;
      return document && sonic ? readLibrarySonicSettingsBinding(rowId, document, sonic) : undefined;
    }
    return document && readLibrarySettingsBinding(rowId, document);
  };
  /** Raw status is data for the future approved presenter, never generic JSON row copy. */
  sonicStatus = (libraryId: string) => {
    const document = this.document(libraryId), sonic = this.state.sonic;
    return document && sonic ? readLibrarySonicSettingsBinding('libraries.sonicStatus', document, sonic)?.current : undefined;
  };
  async change(libraryId: string, rowId: string, value: unknown): Promise<boolean> {
    if (rowId === 'libraries.sonicAnalysis') {
      if (typeof value !== 'boolean') throw new Error('Invalid Sonic setting value.');
      return this.sonicCommand(libraryId, value);
    }
    const retained = this.settingsPending.get(libraryId), before = this.document(libraryId) ?? retained?.document;
    if (!this.current() || !before || this.state.libraryId !== libraryId || this.state.loading || this.state.saving) return false;
    if (rowId === 'libraries.name') return this.rename(libraryId, value, before);
    this.invalidateQuiet();const request = new AbortController();this.request = request;
    this.publish({...this.state, libraryId, saving: true});
    const requireCurrent = () => {if (!this.owns(request, libraryId)) throw new UserMutationError('mutation_scope_changed');};
    try {
      let pending = retained;
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
      const draft = JSON.stringify([rowId, value]);
      if (pending && pending.draft !== draft) throw new UserMutationError('mutation_needs_review');
      if (!pending) {
        const document = this.document(libraryId);
        if (!document) throw new UserMutationError('mutation_needs_review');
        const settings = patchLibrarySettingsBinding(rowId, value, document);
        requireCurrent(); const scope = this.api.getUserMutationScope?.(); requireCurrent();
        pending = {document: JSON.parse(JSON.stringify(document)), settings: JSON.stringify(settings), draft, operationId: randomId(), scope: scope && {...scope}, dispatched: false};
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
      const document = await saveLibrarySettings(api, pending.document, JSON.parse(pending.settings) as LibrarySettings, pending.operationId, request.signal);
      requireCurrent(); this.settingsPending.delete(libraryId);
      this.publish({libraryId, document, sonic: this.offersSonic(document) ? this.state.sonic : undefined, loading: false, saving: false});return true;
    } catch (error) {
      if (this.owns(request, libraryId)) {this.publish({libraryId, loading: false, saving: false, error});throw error;}
    } finally {if (this.request === request) this.request = undefined;}
    return false;
  }

  private async rename(libraryId: string, value: unknown, before: LibraryAdminDocument): Promise<boolean> {
    const management = this.management;
    if (!management || typeof value !== 'string' || !before.actions.includes('rename')) return false;
    this.invalidateQuiet();const request = new AbortController(); this.request = request;
    this.publish({...this.state, saving: true});
    try {
      await management.selectLibrary(libraryId);
      if (!this.owns(request, libraryId)) return false;
      const selected = management.getSnapshot().selected;
      if (selected.phase !== 'ready' || selected.data?.library.id !== libraryId) throw new Error('library_changed');
      await management.rename(value, selected.data.library.revision);
      if (!this.owns(request, libraryId)) return false;
      if (management.getSnapshot().mutation.phase !== 'complete') throw new Error('library_rename_failed');
      const document = await readLibrarySettings(this.api, libraryId, request.signal);
      if (this.owns(request, libraryId)) {this.publish({...this.state, document, saving: false}); return true;}
    } catch (error) {
      if (this.owns(request, libraryId)) {this.publish({...this.state, loading: false, saving: false, error}); throw error;}
    } finally {if (this.request === request) this.request = undefined;}
    return false;
  }
  async reset(libraryId: string, rowId: string): Promise<boolean> {
    const value = this.read(libraryId, rowId)?.default;
    return value !== undefined ? this.change(libraryId, rowId, value) : false;
  }
  retrySonic = (libraryId: string) => this.sonicCommand(libraryId);
  private async sonicCommand(libraryId: string, enabled?: boolean): Promise<boolean> {
    const document = this.document(libraryId), sonic = this.state.sonic;
    if (!document || !sonic || this.state.loading || this.state.saving || !!this.state.libraryId && this.settingsPending.has(this.state.libraryId) || !readLibrarySonicSettingsBinding('libraries.sonicAnalysis', document, sonic)) return false;
    const patch = enabled === undefined ? undefined : patchLibrarySonicSettingsBinding('libraries.sonicAnalysis', enabled, document, sonic);
    this.invalidateQuiet();const request = new AbortController();this.request = request;
    this.publish({...this.state, saving: true});
    try {
      const next = patch ? await saveLibrarySonic(this.api, sonic, patch.enabled, request.signal) : await retryLibrarySonic(this.api, libraryId, request.signal);
      if (this.owns(request, libraryId)) {this.publish({libraryId, document, sonic: next, loading: false, saving: false});return true;}
    } catch (error) {
      if (this.owns(request, libraryId)) {this.publish({libraryId, loading: false, saving: false, error});throw error;}
    } finally {if (this.request === request) this.request = undefined;}
    return false;
  }
  dispose() {this.invalidateQuiet();this.closed = true;this.management?.dispose();this.request?.abort();this.request = undefined;this.pending = undefined;this.state = empty;this.settingsPending.clear();}
}
