import type {SourceChoiceDefinition} from '../../../../../../packages/client-core/src/remote-source-choice';
import type {SourceChoiceIntent, SourceChoiceOpening} from '../../../../../../packages/client-core/src/remote-sources';
import {LibraryManagementService} from '../../../../../../packages/client-core/src/library-management';
import {readLibraryInventoryConfig, saveLibraryFolder} from '../../../../../../packages/client-core/src/server-admin/libraries';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {RemoteFolder} from '../../../../../../packages/client-core/src/remote-sources';
import {RemoteSourceService, type RemoteState, type RemoteSource, type DAVConfiguration, type DAVConnectionInput, type DAVConnectionResult, type SourceReadSettings, type SourceReadValues, type RcloneSourceDraft, type RcloneTestInput, type RcloneTestResult, type SourceAdvancedSettings, type SourceAdvanced} from '../../../../../../packages/client-core/src/remote-sources';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from '../../settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../settings-live/ports';

function terminalRefusal(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const refusal = error as {status?: unknown; statusCode?: unknown; code?: unknown};
  return refusal.status === 401 || refusal.status === 403 || refusal.statusCode === 401 || refusal.statusCode === 403
    || refusal.code === 'unauthorized' || refusal.code === 'forbidden' || refusal.code === 'not_permitted';
}

export type NativeRemoteSourcesSnapshot = Readonly<{allowed: boolean; sources: RemoteState}>;

/** A screen owns its reads. It neither borrows the legacy panel nor persists credential drafts. */
export class NativeRemoteSourcesController extends NativeScreenStore<NativeRemoteSourcesSnapshot> {
  private readonly lifetime: NativeScreenLifetime;
  private readonly service: RemoteSourceService;
  private readonly ports: NativeSettingsLivePorts;
  private readonly viewportCurrent: () => boolean;
  private offService: () => void = () => {};
  private offAuthority: () => void = () => {};
  private disposed = false;

  constructor(ports: NativeSettingsLivePorts, viewportCurrent: () => boolean = () => true) {
    const lifetime = new NativeScreenLifetime(ports.context);
    let retire = () => lifetime.dispose();
    const admitted = () => {
      const account = ports.context.account;
      return viewportCurrent() && lifetime.current() && account.canManage && !account.account.disabled
        && account.account.role === 'owner'
        && account.serverId === lifetime.authority.serverId
        && account.account.id === lifetime.authority.accountId
        && account.account.primaryProfileId === lifetime.authority.profileId;
    };
    const originalApi = ports.context.api;
    const requireCurrent = () => {if (!admitted()) throw screenScopeEnded();};
    const service = new RemoteSourceService({
      ...(originalApi.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
        requireCurrent(); await originalApi.acknowledgeUserMutation(id); requireCurrent();
      }} : {}),
      ...(originalApi.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
        requireCurrent(); await originalApi.awaitUserMutationsReady(); requireCurrent();
      }} : {}),
      ...(originalApi.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
        requireCurrent(); const value = originalApi.getUserMutationCoordinator(); requireCurrent(); return value;
      }} : {}),
      ...(originalApi.getUserMutationScope ? {getUserMutationScope: () => {
        requireCurrent(); const value = originalApi.getUserMutationScope(); requireCurrent(); return value;
      }} : {}),
      request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
        if (!admitted()) return Promise.reject(screenScopeEnded());
        return lifetime.request(async current => {
          try {
            const result = await originalApi.request<T>(path, method, body, current);
            if (!admitted()) throw screenScopeEnded();
            return result;
          } catch (error) {
            if (terminalRefusal(error)) retire();
            throw error;
          }
        }, signal);
      },
    }, {serverId: lifetime.authority.serverId, viewerId: lifetime.authority.profileId}, async () => {
      if (!admitted()) throw screenScopeEnded();
      return `${Date.now()}-${ports.requestId()}`;
    }, admitted);
    super({allowed: admitted(), sources: {...service.getSnapshot(), loading: admitted()}});
    this.ports = ports;
    this.viewportCurrent = viewportCurrent;
    this.lifetime = lifetime;
    this.service = service;
    retire = () => this.dispose();
    this.offService = service.subscribe(() => {
      if (!this.allowed()) {this.dispose(); return;}
      this.publish({allowed: true, sources: service.getSnapshot()});
    });
    this.offAuthority = ports.context.subscribeAuthority(() => {
      if (!this.allowed()) this.dispose();
    });
  }

  private allowed(): boolean {
    const account = this.ports.context.account;
    return !this.disposed && this.viewportCurrent() && this.lifetime.current() && account.canManage && !account.account.disabled
      && account.account.role === 'owner' && account.serverId === this.lifetime.authority.serverId
      && account.account.id === this.lifetime.authority.accountId
      && account.account.primaryProfileId === this.lifetime.authority.profileId;
  }

  async load(): Promise<void> {
    if (!this.allowed()) {this.dispose(); return;}
    await this.service.load();
    if (!this.allowed()) this.dispose();
  }

  private assertAllowed(): void {
    if (!this.allowed()) {this.dispose(); throw screenScopeEnded();}
  }

  /** Credential fields stay in this call and the service request, never in a snapshot. */
  async testConnection(input: DAVConnectionInput, guard: () => boolean = () => true): Promise<DAVConnectionResult> {
    this.assertAllowed();
    try {
      if (!guard()) throw screenScopeEnded();
      const result = await this.service.testConnection(input, guard);
      if (!guard()) throw screenScopeEnded();
      this.assertAllowed();
      return result;
    } finally {if (!this.allowed()) this.dispose();}
  }

  async configure(id: string | null, input: DAVConfiguration, base?: RemoteSource, guard: () => boolean = () => true): Promise<void> {
    this.assertAllowed();
    try {if (!guard()) throw screenScopeEnded(); await this.service.configure(id, input, base, guard);}
    finally {if (!this.allowed()) this.dispose();}
  }

  private assignmentApi(guard: () => boolean): LibraryContentApi {
    const originalApi = this.ports.context.api;
    const requireCurrent = () => {this.assertAllowed(); if (!guard()) throw screenScopeEnded();};
    return {
      ...(originalApi.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
        requireCurrent(); await originalApi.awaitUserMutationsReady(); requireCurrent();
      }} : {}),
      ...(originalApi.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
        requireCurrent(); const value = originalApi.getUserMutationCoordinator(); requireCurrent(); return value;
      }} : {}),
      ...(originalApi.getUserMutationScope ? {getUserMutationScope: () => {
        requireCurrent(); const value = originalApi.getUserMutationScope(); requireCurrent(); return value;
      }} : {}),
      ...(originalApi.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
        this.assertAllowed(); if (!guard()) throw screenScopeEnded();
        await originalApi.acknowledgeUserMutation(id);
        this.assertAllowed(); if (!guard()) throw screenScopeEnded();
      }} : {}),
      request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.assertAllowed();
      if (!guard()) return Promise.reject(screenScopeEnded());
      return this.lifetime.request(async current => {
        try {
          this.assertAllowed();
          if (!guard()) throw screenScopeEnded();
          const result = await originalApi.request<T>(path, method, body, current);
          this.assertAllowed();
          if (!guard()) throw screenScopeEnded();
          return result;
        } catch (error) {if (terminalRefusal(error)) this.dispose(); throw error;}
      }, signal);
    }};
  }

  libraryAssignment(guard: () => boolean): LibraryManagementService {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return new LibraryManagementService({api: this.assignmentApi(guard), scope: {serverId: this.lifetime.authority.serverId,
      viewerId: this.lifetime.authority.profileId}, readRetries: 0});
  }

  async attachFolder(source: RemoteSource, libraryId: string, folder: Pick<RemoteFolder, 'name' | 'path' | 'relative'>, guard: () => boolean): Promise<void> {
    const status = await this.sourceStatus(source, guard);
    if (!('assignmentFenceSupported' in status) || status.assignmentFenceSupported !== true) throw Object.assign(screenScopeEnded(), {code: 'remote_source_changed'});
    const fresh = await this.sourceFolders(source, folder.relative ? {path: folder.relative} : {}, guard);
    if (fresh.path !== folder.path || fresh.relative !== folder.relative) throw screenScopeEnded();
    const api = this.assignmentApi(guard), scope = {serverId: this.lifetime.authority.serverId, viewerId: this.lifetime.authority.profileId};
    const config = await readLibraryInventoryConfig(api, scope, libraryId);
    if (config.sources.some(value => value.path === folder.path && value.enabled)) return;
    const settings = {expectedRemoteSource: {sourceId: source.id, generation: source.generation}, name: folder.name, path: folder.path, classification: 'local' as const,
      followSymlinks: false, intervalSeconds: 21600, expectedRevision: config.revision, acceptReplacement: false};
    await saveLibraryFolder(api, libraryId, undefined, settings);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
  }

  async validateAssignmentFolders(source: RemoteSource, folders: readonly Pick<RemoteFolder, 'path' | 'relative'>[], guard: () => boolean): Promise<void> {
    if (!folders.length || folders.length > 64) throw screenScopeEnded();
    const status = await this.sourceStatus(source, guard);
    if (!('assignmentFenceSupported' in status) || status.assignmentFenceSupported !== true) throw Object.assign(screenScopeEnded(), {code: 'remote_source_changed'});
    for (const folder of folders) {
      const fresh = await this.sourceFolders(source, folder.relative ? {path: folder.relative} : {}, guard);
      if (fresh.path !== folder.path || fresh.relative !== folder.relative) throw screenScopeEnded();
    }
  }

  async readAdvanced(source: RemoteSource, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.readAdvanced(source.id, guard);
    this.assertAllowed();
    if (!guard() || result.generation !== String(source.generation)) throw screenScopeEnded();
    return result;
  }

  async saveAdvanced(settings: SourceAdvancedSettings, values: SourceAdvanced, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.saveAdvanced(settings, values, guard);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return result;
  }

  async sourceFolders(source: RemoteSource, options: Readonly<{path?: string; cursor?: string; showHidden?: boolean}>, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.listFolders(source.id, {...options, limit: 100}, guard);
    this.assertAllowed();
    if (!guard() || result.generation !== String(source.generation)) throw screenScopeEnded();
    return result;
  }

  async sourceStatus(source: RemoteSource, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.readStatus(source.id, guard);
    this.assertAllowed();
    if (!guard() || result.generation !== String(source.generation)) throw screenScopeEnded();
    return result;
  }

  async importedFacts(guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const [program, providers] = await Promise.all([this.service.rcloneProgram(), this.service.rcloneProviders()]);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return {program, providers};
  }

  async accountConfig(guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.rcloneConfig();
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return result;
  }

  async configureFromAccount(input: Readonly<{name: string; remote: string}>, guard: () => boolean): Promise<void> {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    await this.service.configureRcloneFromConfig(input, guard);
    this.assertAllowed();
  }

  async startDriveOAuth(clientId: string, clientSecret: string, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.startDriveOAuth(clientId, clientSecret, guard);
    this.assertAllowed();
    if (!guard()) {void this.service.driveOAuthCancel(result.state); throw screenScopeEnded();}
    return result;
  }

  async driveOAuthStatus(state: string, redirect: string | undefined, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    if (redirect !== undefined) {
      // Paste records a code only; the status owner performs the token exchange.
      await this.service.driveOAuthPaste(state, redirect, guard);
      this.assertAllowed();
      if (!guard()) throw screenScopeEnded();
    }
    const result = await this.service.driveOAuthStatus(state, guard);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return result;
  }

  async cancelDriveOAuth(state: string) {
    if (!this.allowed()) return;
    await this.service.driveOAuthCancel(state);
  }

  async testImported(input: RcloneTestInput, guard: () => boolean): Promise<RcloneTestResult> {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.testRclone(input, guard);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return result;
  }

  async configureImported(source: RemoteSource | undefined, input: RcloneSourceDraft, guard: () => boolean): Promise<void> {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    await this.service.configureRclone(source?.id ?? null, input, source, guard);
    this.assertAllowed();
  }

  async remove(source: RemoteSource, guard: () => boolean = () => true): Promise<void> {
    this.assertAllowed();
    try {if (!guard()) throw screenScopeEnded(); if (source.kind === 'rclone') await this.service.removeRclone(source, guard); else await this.service.remove(source, guard);}
    finally {if (!this.allowed()) this.dispose();}
  }

  async readProfile(source: RemoteSource, guard: () => boolean): Promise<SourceReadSettings> {
    this.assertAllowed();
    try {
      const result = await this.service.readProfile(source.id, String(source.generation), guard);
      this.assertAllowed();
      if (!guard()) throw screenScopeEnded();
      return result;
    } finally {if (!this.allowed()) this.dispose();}
  }

  async saveProfile(profile: SourceReadSettings, values: SourceReadValues, guard: () => boolean): Promise<SourceReadSettings> {
    this.assertAllowed();
    try {
      const result = await this.service.saveProfile(profile, values, guard);
      this.assertAllowed();
      if (!guard()) throw screenScopeEnded();
      return result;
    } finally {if (!this.allowed()) this.dispose();}
  }

  async saveProfileStartChoice(profile: SourceReadSettings, advanced: SourceAdvancedSettings, offered: SourceChoiceDefinition, opening: SourceChoiceOpening) {
    this.assertAllowed();
    if (!opening.isCurrent()) throw screenScopeEnded();
    const result = await this.service.saveProfileStartChoice(profile, advanced, offered, opening);
    this.assertAllowed();
    if (!opening.isCurrent()) throw screenScopeEnded();
    return result;
  }

  async recoverStartChoice(intent: SourceChoiceIntent, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.recoverStartChoice(intent);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return result;
  }

  /** A choice's accepted cache child may have a new generation; never require the historical parent's. */
  async reconcileChoiceProfile(sourceId: string, guard: () => boolean) {
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    const result = await this.service.readProfile(sourceId, undefined, guard);
    this.assertAllowed();
    if (!guard()) throw screenScopeEnded();
    return result;
  }

  async recover(): Promise<void> {
    this.assertAllowed();
    try {await this.service.recover();}
    finally {if (!this.allowed()) this.dispose();}
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.offAuthority();
    this.offService();
    this.service.dispose();
    this.lifetime.dispose();
    this.publish({allowed: false, sources: this.service.getSnapshot()});
    this.clearListeners();
  }
}
