import {setServerChannelHidden} from '../../../../../packages/client-core/src/server-channel-visibility';
import {clonePortableDto} from '../../../../../packages/client-core/src';
import {liveSourceChoicesService, parseEnvelope, type LiveSourceChoicePage} from '../../../../../packages/client-core/src/administration';
import {rebaseDraft, sendWithFreshRecord, ALREADY_DONE} from '../../../../../packages/client-core/src/freshness-actions';
import {lanConfirmationRoots, previewRemoteSource, remoteSourceDraft, withConfirmedLanRoots, type RemoteSourceDraft, type RemoteSourcePreview, type SourceKind} from '../../../../../packages/client-core/src/linear-api';
import {normalizeHdHomerunLocator} from '../../../../../packages/client-core/src/server-admin/panel-logic';
import type {LiveSourceInput} from '../../../../../packages/client-core/src/live-source-draft';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from './lifetime';
import {
  adminResult, parseChannelMapPage, parseLiveDefaults, parseLiveSourceReceipt, parseLiveSources, parseLogoImport, parseSourceDocument, parseTunerDiscovery, parseUploadPreview, type UploadPreview,
  type ChannelMapEdit, type ChannelMapPage, type LiveDefaults, type LiveSource, type LiveSources, type LiveSourceSettings, type LogoImportOutcome, type TunerDiscovery,
} from './live-owner-model';
import {needsRuleOwner, parseAdminRulePage, parseDVRDocument, parseRecordingPolicy, parseTunerView, type AdminRule, type DVRDefaults, type DVRDocument, type RecordingPolicy, type TunerView} from './recording-owner-model';
import {recordingGuideRoute, recordingOwnerKey, recordingRulePayload, recordingSeries, type RecordingGrant, type RecordingOwner, type RecordingSeries} from '../../../../../packages/client-core/src/server-admin/recording-policy';
import {parseChannelGuide} from '../../../../../packages/client-core/src/channel-guide';

export type LiveOwnerSnapshot = Readonly<{
  list?: LiveSources; loading: boolean; busy: boolean; checkedAt?: number; error?: unknown; notice?: string;
}>;

export type LiveOwnerApi = Readonly<{request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>}>;
export type NativeRecordingCommandLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
type Request = <T>(path: string, method?: string, body?: unknown) => Promise<T>;

const sourcePath = (id: string, rest: string) => `/v1/admin/live-sources/${encodeURIComponent(id)}${rest}`;

/** One owner's genuine live-source administration: reads and fenced writes through the screen's
 * single lifetime. Nothing is written optimistically; a row shows what the server returned. */
export class NativeLiveOwnerController extends NativeScreenStore<LiveOwnerSnapshot> {
  readonly lifetime: NativeScreenLifetime;
  private api: LiveOwnerApi;
  private operationId: () => string;
  private disposed = false;
  private loadTicket = 0;
  private ready?: Promise<void>;
  constructor(lifetime: NativeScreenLifetime, api: LiveOwnerApi, operationId: () => string) {
    super({loading: false, busy: false});
    this.lifetime = lifetime; this.api = api; this.operationId = operationId;
  }
  private request: Request = <T,>(path: string, method?: string, body?: unknown) => this.lifetime.request(signal => this.api.request<T>(path, method, body, signal));
  readonly current = () => !this.disposed && this.lifetime.current() && !this.disposed;
  load(quiet = false): Promise<void> {
    if (!this.current()) return Promise.resolve();
    const ticket = ++this.loadTicket;
    if (!quiet) this.publish({...this.getSnapshot(), loading: true, error: undefined});
    return this.request<unknown>('/v1/admin/live-sources').then(raw => {
      if (!this.current() || ticket !== this.loadTicket) return;
      this.publish({list: parseLiveSources(raw, this.lifetime.authority.serverId), loading: false, busy: this.getSnapshot().busy, checkedAt: Date.now(), error: undefined});
    }, error => {
      if (this.current() && ticket === this.loadTicket) this.publish({...this.getSnapshot(), loading: false, error});
    });
  }
  /** The first real read of a screen or sheet; later opens reuse it until something changes. */
  ensure(): Promise<void> {
    if (!this.ready) this.ready = this.load().finally(() => {this.ready = undefined;});
    return this.ready;
  }
  private act<T>(work: () => Promise<T>, after: (value: T) => void, caller?: Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>): Promise<T | undefined> {
    const live = () => !caller || (caller.isCurrent() && caller.signal?.aborted !== true);
    if (!this.current() || this.getSnapshot().busy || !live()) return Promise.resolve(undefined);
    this.publish({...this.getSnapshot(), busy: true, error: undefined, notice: undefined});
    return work().then(value => {
      if (!this.current()) return undefined;
      if (!live()) {this.publish({...this.getSnapshot(), busy: false});return undefined;}
      after(value);
      this.publish({...this.getSnapshot(), busy: false});
      return value;
    }, error => {
      if (this.current()) {
        if (live()) this.publish({...this.getSnapshot(), busy: false, error});
        else this.publish({...this.getSnapshot(), busy: false});
      }
      return undefined;
    });
  }
  channelHidden = async (sourceId: string, channelId: string, hidden: boolean, caller: NativeRecordingCommandLifetime, expected?: Readonly<{revision: number; hidden: boolean}>) => {
    const current = () => this.current() && caller.isCurrent() && !caller.signal.aborted;
    const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.lifetime.request(next => this.api.request<T>(path, method, body, next), signal)};
    return setServerChannelHidden(api, this.lifetime.authority.serverId, sourceId, channelId, hidden, this.operationId, caller.signal, current, expected);
  };
  nextId = (): string => this.operationId();
  private row = (id: string) => this.getSnapshot().list?.sources.find(value => value.id === id);
  private freshRow = async (id: string) => {
    const raw = await this.request<unknown>('/v1/admin/live-sources');
    const list = parseLiveSources(raw, this.lifetime.authority.serverId);
    return list.sources.find(value => value.id === id);
  };
  refreshSource = (id: string) => this.act(async () => {
    const row = this.row(id); if (!row) throw new Error('missing_source');
    const result = await sendWithFreshRecord(row, (fresh, retries) => this.request<unknown>(sourcePath(fresh.id, '/refresh'), 'POST', {expectedRevision: fresh.revision, ...(retries ? {} : {})}), {reread: () => this.freshRow(id), applies: now => now.state !== 'disabled'});
    if (result === ALREADY_DONE) return;
    await this.load(true);
  }, () => undefined);
  setEnabled = (id: string, enabled: boolean) => this.act(async () => {
    const row = this.row(id); if (!row) throw new Error('missing_source');
    const key = this.operationId();
    const result = await sendWithFreshRecord(row, fresh => this.request<unknown>(sourcePath(fresh.id, '/enabled'), 'POST', {expectedRevision: fresh.revision, enabled, requestId: key}),
      {reread: () => this.freshRow(id), applies: now => (now.state !== 'disabled') !== enabled});
    if (result === ALREADY_DONE) return;
    await this.load(true);
  }, () => undefined);
  removeSource = (id: string) => this.act(async () => {
    const row = this.row(id); if (!row) throw new Error('missing_source');
    const key = this.operationId();
    const result = await sendWithFreshRecord(row, fresh => this.request<unknown>(sourcePath(fresh.id, '/remove'), 'POST', {expectedRevision: fresh.revision, requestId: key}), {reread: () => this.freshRow(id), applies: () => false});
    if (result === ALREADY_DONE) return;
    await this.load(true);
  }, () => undefined);
  importLogos = (id: string) => this.act(async () => parseLogoImport(await this.request<unknown>(sourcePath(id, '/logos'), 'POST', {operationId: this.operationId()}), this.lifetime.authority.serverId), () => undefined);
  document = async (id: string) => parseSourceDocument(await this.request<unknown>(sourcePath(id, '/configuration')), this.lifetime.authority.serverId);
  saveDocument = async (mark: Readonly<{sourceId: string; revision: number; settings: LiveSourceSettings}>, draft: LiveSourceSettings) => this.act(async () => {
    const put = (revision: number, value: LiveSourceSettings) => this.request<unknown>(sourcePath(mark.sourceId, '/configuration'), 'PUT', {expectedRevision: revision, operationId: this.operationId(), settings: value});
    let current = mark;
    try { await put(current.revision, draft); }
    catch (error) {
      if(JSON.stringify(current.settings.remote)!==JSON.stringify(draft.remote))throw error;
      const fresh = await this.document(current.sourceId).catch(() => {throw error;});
      const rebased = rebaseDraft(current.settings, draft, fresh.settings);
      if (rebased.conflict) throw error;
      await put(fresh.revision, rebased.merged);
      current = {sourceId: fresh.sourceId, revision: fresh.revision, settings: fresh.settings};
    }
    await this.load(true);
    return current;
  }, () => undefined);
  channelMap = async (id: string, cursor = '') => parseChannelMapPage(await this.request<unknown>(sourcePath(id, `/channel-map?limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`)), this.lifetime.authority.serverId);
  saveChannelMap = async (id: string, revision: number, edits: readonly Readonly<{channelId: string; override: ChannelMapEdit}>[]) => this.act(async () => {
    const batches: typeof edits[number][][] = [];
    for (let index = 0; index < edits.length; index += 200) batches.push(edits.slice(index, index + 200));
    let current = revision, saved = 0;
    for (const batch of batches) {
      try {
        const page = await this.request<unknown>(sourcePath(id, '/channel-map'), 'PUT', {expectedRevision: current, operationId: this.operationId(), overrides: batch.map(value => ({channelId: value.channelId, number: value.override.number.trim(), guideChannelId: value.override.guideChannelId.trim(), hidden: value.override.hidden}))});
        parseChannelMapPage(page, this.lifetime.authority.serverId);
        saved += batch.length; current += 1;
      } catch (error) {
        const partial = Object.assign(new Error('channel_map_partial'), {saved, total: edits.length, cause: error});
        throw partial;
      }
    }
    await this.load(true);
    return saved;
  }, () => undefined);
  choices = async (id: string, kind: 'groups' | 'channels', cursor = '', query = ''): Promise<LiveSourceChoicePage> => {
    this.lifetime.assert();
    const value = await liveSourceChoicesService(<T,>(path: string, method?: string, body?: unknown) => this.request<T>(path, method, body), id).list(kind, cursor, 50, query);
    this.lifetime.assert();
    return value;
  };
  discover = async (): Promise<TunerDiscovery> => parseTunerDiscovery(await this.request<unknown>('/v1/admin/live-sources/discover', 'POST', {windowMs: 5000}), this.lifetime.authority.serverId);
  liveDefaults = async () => parseLiveDefaults(await this.request<unknown>('/v1/admin/live/settings'), this.lifetime.authority.serverId);
  saveLiveDefaults = async (revision: number, value: LiveDefaults) => parseLiveDefaults(await this.request<unknown>('/v1/admin/live/settings', 'PUT', {expectedRevision: revision, operationId: this.operationId(), settings: value}), this.lifetime.authority.serverId);
  previewRemote = async (draft: RemoteSourceDraft): Promise<RemoteSourcePreview> => {
    const normalized = draft.kind === 'hdhomerun' ? {...draft, locator: normalizeHdHomerunLocator(draft.locator)} : draft;
    return previewRemoteSource({request: this.request}, this.lifetime.authority.serverId, normalized);
  };
  saveRemote = (previewId: string) => this.act(async () => parseSourceDocument(await this.request<unknown>('/v1/admin/live-sources/remote/save', 'POST', {previewId, requestId: this.operationId()}), this.lifetime.authority.serverId), () => undefined);
  private uploadRequest = <T,>(path: string, draft: LiveSourceInput, caller?: Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>) =>
    this.lifetime.request(signal => this.api.request<T>(path, 'POST', draft, signal), caller?.signal);
  previewFiles = async (draft: LiveSourceInput, caller?: Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>): Promise<UploadPreview> => {
    this.lifetime.assert();
    if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
    const raw = await this.uploadRequest<unknown>('/v1/admin/live-sources/preview', draft, caller);
    if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
    return parseUploadPreview(raw, this.lifetime.authority.serverId);
  };
  saveFiles = (draft: LiveSourceInput, caller?: Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>) =>
    this.act(async () => {
      if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
      const raw = await this.uploadRequest<unknown>('/v1/admin/live-sources', draft, caller);
      if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
      return parseLiveSourceReceipt(raw, this.lifetime.authority.serverId);
    }, () => undefined, caller);
  tuners = async (): Promise<TunerView> => parseTunerView(await this.request<unknown>('/v1/admin/dvr/tuners'), this.lifetime.authority.serverId);
  policy = async (cursor = ''): Promise<RecordingPolicy> => {
    const [owners, grants] = await Promise.all([this.request<unknown>('/v1/admin/dvr/recording-owners?' + new URLSearchParams({limit: '200', cursor})), this.request<unknown>('/v1/admin/dvr/recording-permissions')]);
    return parseRecordingPolicy(owners, grants, this.lifetime.authority.serverId);
  };
  setPermission = async (owner: RecordingOwner, enabled: boolean, revision: number) => {
    await this.request('/v1/admin/dvr/recording-permissions', 'PUT', {owner, enabled, revision});
  };
  rules = async (cursor = '') => parseAdminRulePage(await this.request<unknown>('/v1/admin/dvr/recording-groups?' + new URLSearchParams({limit: '100', cursor})), this.lifetime.authority.serverId);
  private async recordingMutation(draft: Parameters<typeof recordingRulePayload>[0], grants: readonly RecordingGrant[], caller?: NativeRecordingCommandLifetime): Promise<AdminRule> {
    const callerSignal=caller?.signal,callerCurrent=caller?.isCurrent;
    const current = () => {
      if (this.disposed || callerSignal?.aborted || !this.current() || this.disposed || callerSignal?.aborted) return false;
      if (callerCurrent && !callerCurrent()) return false;
      const ownerCurrent=this.current();
      // The final owner predicate may synchronously retire this original caller.
      return ownerCurrent && !this.disposed && !callerSignal?.aborted;
    };
    if (!current()) throw screenScopeEnded();
    const original = clonePortableDto(draft);
    const payload = clonePortableDto(recordingRulePayload(original, grants, this.operationId()));
    const expected = clonePortableDto(payload);
    if (!current()) throw screenScopeEnded();
    const path = original.id ? `/v1/admin/dvr/recording-groups/${encodeURIComponent(original.id)}` : '/v1/admin/dvr/recording-groups';
    const response = await this.lifetime.request(async signal => {
      if (!current()) throw screenScopeEnded();
      const value = await this.api.request<unknown>(path, original.id ? 'PUT' : 'POST', payload, signal);
      if (!current()) throw screenScopeEnded();
      return value;
    }, callerSignal);
    if (!current()) throw screenScopeEnded();
    const envelope = response as {serverId?: unknown; result?: unknown};
    const saved = parseAdminRulePage({serverId: envelope?.serverId, result: {items: [envelope?.result]}}, this.lifetime.authority.serverId).items[0]!;
    const options = expected.options as AdminRule['options'];
    if (!saved.id || original.id && (saved.id !== original.id || saved.revision !== expected.expectedRevision + 1)
      || saved.revision < 1 || recordingOwnerKey(saved.owner as RecordingOwner) !== recordingOwnerKey(expected.owner)
      || saved.sourceId !== expected.sourceId || saved.kind !== expected.kind || saved.match !== expected.match
      || saved.name !== expected.name || saved.enabled !== expected.enabled
      || options.prePaddingMs !== null && saved.options.prePaddingMs !== options.prePaddingMs
      || options.postPaddingMs !== null && saved.options.postPaddingMs !== options.postPaddingMs
      || options.keepPolicy !== null && JSON.stringify(saved.options.keepPolicy) !== JSON.stringify(options.keepPolicy)
      || saved.options.newEpisodesOnly !== options.newEpisodesOnly || saved.options.priority !== options.priority
      || saved.options.folderTemplate !== options.folderTemplate) throw new Error('invalid_response');
    if (!current()) throw screenScopeEnded();
    // This is a command receipt, not authority to refresh a shared source under
    // a successor page. The original admitted presenter owns its own reload.
    return saved;
  }
  saveRuleEnabled = (rule: AdminRule, grants: readonly RecordingGrant[], enabled: boolean, caller?: NativeRecordingCommandLifetime) => {
    if (needsRuleOwner(rule) || rule.owner.authority === '') return Promise.reject(new Error('invalid_rule_owner'));
    return this.recordingMutation({id: rule.id, revision: rule.revision, owner: rule.owner, anchor: rule.anchor, kind: rule.kind, name: rule.name, match: rule.match, sourceId: rule.sourceId, enabled, options: rule.options}, grants, caller);
  };
  deleteRule = async (rule: AdminRule) => {
    await this.request(`/v1/admin/dvr/recording-groups/${encodeURIComponent(rule.id)}?expectedRevision=${rule.revision}&operationId=${encodeURIComponent(this.operationId())}`, 'DELETE');
  };
  guideSeries = async (day: string, sourceId: string): Promise<readonly RecordingSeries[]> => {
    const route = recordingGuideRoute(day, sourceId);
    if (!route) throw new Error('invalid_guide_date');
    const query = new URLSearchParams({kind: route.kind, start: route.start, end: route.end, timezone: route.timezone, search: route.search, sourceId: route.sourceId, limit: '30'});
    const guide = parseChannelGuide(await this.request<unknown>(`/v1/guide?${query.toString()}`), this.lifetime.authority.serverId, route);
    return recordingSeries(guide, true);
  };
  createRule = (draft: Parameters<typeof recordingRulePayload>[0], grants: readonly RecordingGrant[], caller?: NativeRecordingCommandLifetime) => this.recordingMutation(draft, grants, caller);
  dvrSettings = async (): Promise<DVRDocument> => parseDVRDocument(await this.request<unknown>('/v1/admin/dvr/settings'), this.lifetime.authority.serverId);
  saveDvrSettings = async (revision: number, value: DVRDefaults) => parseDVRDocument(await this.request<unknown>('/v1/admin/dvr/settings', 'PUT', {expectedRevision: revision, operationId: this.operationId(), settings: value}), this.lifetime.authority.serverId);
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true; this.loadTicket++;
    this.clearListeners();
  }
}

/** The canonical draft a new remote source starts from, with this server's member audience as the default. */
export const newRemoteDraft = (id: string): RemoteSourceDraft => ({...remoteSourceDraft(id), viewerAccess: 'server-members'});

export {lanConfirmationRoots, withConfirmedLanRoots};
export type {RemoteSourceDraft, RemoteSourcePreview, SourceKind, ChannelMapEdit, ChannelMapPage, LiveDefaults, LiveSource, LiveSources, LiveSourceSettings, LogoImportOutcome, TunerDiscovery};
export {parseEnvelope, adminResult};
