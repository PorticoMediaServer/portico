import {parseEnvelope} from '../../../../../packages/client-core/src/administration';
import {lanConfirmationRoots, remoteSourceDraft, withConfirmedLanRoots, type RemoteSourceDraft, type RemoteSourcePreview, type SourceKind} from '../../../../../packages/client-core/src/linear-api';
import type {LiveSourceInput} from '../../../../../packages/client-core/src/live-source-draft';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from './lifetime';
import {importSourceLogos, readSourceChannels, readSourceChoices, readSourceSettings, saveSourceChannels, saveSourceSettings} from '../../../../../packages/client-core/src/server-admin/live-source-settings';
import type {LiveSourceChannel} from '../../../../../packages/contracts/src/next/api';
import {readOwnerDvrSettings, saveOwnerDvrSettings, type OwnerDvrDocument} from '../../../../../packages/client-core/src/server-admin/owner-dvr-settings';
import {readLiveDefaultsDocument, saveLiveDefaultsDocument, type LiveDefaultsDocument} from '../../../../../packages/client-core/src/server-admin/live-defaults';
import {changeOwnerSource, createOwnerSource, discoverLiveTuners, previewOwnerSource, readOwnerSources, remoteSourceSave, uploadSourceSave, type OwnerSource, type SourceSave} from '../../../../../packages/client-core/src/server-admin/live-sources';
import {adminResult, liveDefaultsView, rebuiltLiveDefaults, liveSourceFromOwner, type UploadPreview, type ChannelMapEdit, type ChannelMapPage, type LiveDefaults, type LiveSource, type LiveSources, type LiveSourceSettings, type LogoImportOutcome, type TunerDiscovery} from './live-owner-model';
import {dvrDocumentView, type DVRDefaults, type DVRDocument} from './recording-owner-model';
import {routePath} from '../../../../../packages/contracts/src/route-paths';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
export type LiveOwnerSnapshot = Readonly<{
  list?: LiveSources; loading: boolean; busy: boolean; checkedAt?: number; error?: unknown; notice?: string;
}>;

/** The screen's transport; `requestRaw` (the app's header-sending call, guarded by the screen) carries a source's revision. */
export type LiveOwnerApi = Readonly<{request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>}> & Partial<Pick<LibraryContentApi, 'requestRaw'>>;
export type NativeRecordingCommandLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;


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
  readonly current = () => !this.disposed && this.lifetime.current() && !this.disposed;
  load(quiet = false): Promise<void> {
    if (!this.current()) return Promise.resolve();
    const ticket = ++this.loadTicket;
    if (!quiet) this.publish({...this.getSnapshot(), loading: true, error: undefined});
    return this.readSources().then(sources => {
      if (!this.current() || ticket !== this.loadTicket) return;
      this.publish({list: {sources: sources.map(liveSourceFromOwner), refresh: []}, loading: false, busy: this.getSnapshot().busy, checkedAt: Date.now(), error: undefined});
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
  /** Hides or shows one channel in everyone's guide: the channel's own `hidden` on the rebuilt source (PATCH .../channels). */
  channelHidden = async (sourceId: string, channelId: string, hidden: boolean, caller: NativeRecordingCommandLifetime, expected?: Readonly<{revision: number; hidden: boolean}>) => {
    if (!this.current() || !caller.isCurrent() || caller.signal.aborted) throw screenScopeEnded();
    await this.lifetime.request(signal => saveSourceChannels(this.rebuiltApi(), sourceId, [{id: channelId, hidden}], signal), caller.signal);
    return {revision: 0, hidden, previousHidden: expected ? expected.hidden : !hidden};
  };
  nextId = (): string => this.operationId();
  /** The rebuilt sources, kept as read so a change is sent against the revision the row shows. */
  private owned = new Map<string, OwnerSource>();
  private rebuiltApi = (): LibraryContentApi => ({request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.lifetime.request(next => this.api.request<T>(path, method, body, next), signal), ...(this.api.requestRaw ? {requestRaw: this.api.requestRaw} : {})}) as LibraryContentApi;
  private async readSources(): Promise<readonly OwnerSource[]> {
    const {sources} = await readOwnerSources(this.rebuiltApi());
    this.owned = new Map(sources.map(source => [source.id, source]));
    return sources;
  }
  private change = (id: string, action: 'refresh' | 'toggle' | 'remove') => this.act(async () => {
    const source = this.owned.get(id); if (!source) throw new Error('missing_source');
    await changeOwnerSource(this.rebuiltApi(), source, action);
    await this.load(true);
  }, () => undefined);
  refreshSource = (id: string) => this.change(id, 'refresh');
  /** Pausing and resuming is one switch on the rebuilt server; a source already as asked is left alone. */
  setEnabled = (id: string, enabled: boolean) => {const source = this.owned.get(id); return source && source.paused === enabled ? this.change(id, 'toggle') : Promise.resolve(undefined);};
  removeSource = (id: string) => this.change(id, 'remove');
  /** The rebuilt source's channels (GET /v1/admin/live/sources/{id}/channels) as the rows the channel map draws; kept by
   * id so a save sends only what the owner changed. */
  private channelRows = new Map<string, LiveSourceChannel>();
  channelMap = async (id: string, cursor = ''): Promise<ChannelMapPage> => {
    const page = await this.lifetime.request(signal => readSourceChannels(this.rebuiltApi(), id, '', cursor || undefined, signal));
    for (const row of page.items) this.channelRows.set(row.id, row);
    return Object.freeze({sourceId: id, generation: '', nextCursor: page.nextCursor ?? '', items: Object.freeze(page.items.map((row, position) => Object.freeze({
      channelId: row.id, name: row.name, sourceNumber: row.sourceNumber ?? '', number: row.number, group: row.group ?? '', guideChannelId: row.guideKey ?? '', hidden: row.hidden,
      position, overridden: (row.edited?.length ?? 0) > 0})))});
  };
  saveChannelMap = async (id: string, edits: readonly Readonly<{channelId: string; override: ChannelMapEdit}>[]) => this.act(async () => {
    const changes = edits.map(({channelId, override}) => {
      const row = this.channelRows.get(channelId); if (!row) throw new Error('missing_channel');
      const number = override.number.trim();
      return {id: channelId, ...(number !== row.number ? {number} : {}), ...(override.hidden !== row.hidden ? {hidden: override.hidden} : {}),
        ...(override.guideChannelId !== (row.guideKey ?? '') ? {guideKey: override.guideChannelId} : {})};
    });
    const saved = await this.lifetime.request(signal => saveSourceChannels(this.rebuiltApi(), id, changes, signal));
    await this.load(true);
    return saved;
  }, () => undefined);
  discover = async (): Promise<TunerDiscovery> => {
    const found = await this.lifetime.request(signal => discoverLiveTuners(this.rebuiltApi(), signal));
    return Object.freeze({discoveryEnabled: found.discoveryEnabled, message: found.message ?? '', devices: Object.freeze(found.devices.map(device => Object.freeze({deviceId: device.deviceId, model: device.model,
      friendlyName: device.friendlyName, address: device.address, baseUrl: device.baseUrl, tunerCount: device.tunerCount, configured: device.configured})))});
  };
  /** Live defaults are the rebuilt `live` settings document, saved at the revision this screen read. */
  private liveDocument?: LiveDefaultsDocument;
  liveDefaults = async () => {
    const document = await this.lifetime.request(signal => readLiveDefaultsDocument(this.rebuiltApi(), signal));
    this.liveDocument = document; return liveDefaultsView(document);
  };
  saveLiveDefaults = async (_revision: number, value: LiveDefaults) => {
    const current = this.liveDocument; if (!current) throw new Error('missing_live_defaults');
    const next = await this.lifetime.request(signal => saveLiveDefaultsDocument(this.rebuiltApi(), current, rebuiltLiveDefaults(current.value, value), signal));
    this.liveDocument = next; return liveDefaultsView(next);
  };
  /** A checked remote source waits here, by an id only this screen knows, until the owner saves it: the rebuilt
   * server previews a body and creates from the same body (no stored preview). */
  private checked = new Map<string, SourceSave>();
  previewRemote = async (draft: RemoteSourceDraft): Promise<RemoteSourcePreview> => {
    const body = remoteSourceSave({...draft, tunerCount: draft.ownerLimit > 0 ? draft.ownerLimit : 1});
    const preview = await this.lifetime.request(signal => previewOwnerSource(this.rebuiltApi(), body, signal));
    // A tuner reports how many it has; that is what the source keeps.
    const kept = draft.kind === 'hdhomerun' && preview.tuners > 0 ? {...body, tunerCount: preview.tuners} : body;
    const previewId = this.operationId(); this.checked.set(previewId, kept);
    return Object.freeze({previewId, expiresAt: '', mappingKeys: [], discoveredCapacity: preview.tuners, capacityMode: preview.tuners > 0 ? 'discovered' : 'defaulted',
      preview: {diagnostics: {skippedChannels: preview.report.skippedChannels, skippedProgrammes: preview.report.skippedProgrammes, adjustedProgrammes: 0, examples: preview.report.examples ?? []},
        channels: preview.report.channels, programmes: preview.report.programmes, availableStart: '', availableEnd: preview.report.guideThrough ?? '', names: [...preview.names]}});
  };
  saveRemote = (previewId: string) => this.act(async () => {
    const body = this.checked.get(previewId); if (!body) throw new Error('missing_preview');
    const source = await this.lifetime.request(signal => createOwnerSource(this.rebuiltApi(), body, previewId, signal));
    this.checked.delete(previewId);
    return liveSourceFromOwner(source);
  }, () => undefined);
  previewFiles = async (draft: LiveSourceInput, caller?: Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>): Promise<UploadPreview> => {
    this.lifetime.assert();
    if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
    const preview = await this.lifetime.request(signal => previewOwnerSource(this.rebuiltApi(), uploadSourceSave(draft), signal), caller?.signal);
    if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
    return Object.freeze({channels: preview.report.channels, programmes: preview.report.programmes, names: Object.freeze(preview.names.slice(0, 100))});
  };
  saveFiles = (draft: LiveSourceInput, caller?: Readonly<{isCurrent: () => boolean; signal?: AbortSignal}>) =>
    this.act(async () => {
      if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
      const source = await this.lifetime.request(signal => createOwnerSource(this.rebuiltApi(), uploadSourceSave(draft), draft.requestId, signal), caller?.signal);
      if (caller && (!caller.isCurrent() || caller.signal?.aborted === true)) throw new Error('cancelled');
      return liveSourceFromOwner(source);
    }, () => undefined, caller);
  /** A source's own settings as the rebuilt server stores them, with the revision (ETag) a save must carry. */
  sourceSettings = (id: string) => this.lifetime.request(signal => readSourceSettings(this.rebuiltApi(), id, signal));
  saveSourceSettings = async (id: string, body: SourceSave, etag: string) => {
    const saved = await this.lifetime.request(signal => saveSourceSettings(this.rebuiltApi(), id, body, etag, signal));
    void this.load(true);
    return saved;
  };
  importSourceLogos = (id: string, replaceExisting: boolean) => this.lifetime.request(signal => importSourceLogos(this.rebuiltApi(), id, replaceExisting, signal));
  sourceChoices = (id: string, kind: 'groups' | 'channels', query: string, cursor?: string) => this.lifetime.request(signal => readSourceChoices(this.rebuiltApi(), id, kind, query, cursor, signal));
  /** The rebuilt `dvr` document as the guide's Record series sheet reads its defaults (padding and keep). Fields the
   * rebuilt server does not have stay neutral. */
  private dvrDocument?: OwnerDvrDocument;
  dvrSettings = async (): Promise<DVRDocument> => {
    const document = await this.lifetime.request(signal => readOwnerDvrSettings(this.rebuiltApi(), signal));
    this.dvrDocument = document; return dvrDocumentView(document);
  };
  saveDvrSettings = async (_revision: number, value: DVRDefaults) => {
    const current = this.dvrDocument; if (!current) throw new Error('missing_dvr_settings');
    const policy = value.keepPolicy;
    const next = await this.lifetime.request(signal => saveOwnerDvrSettings(this.rebuiltApi(), current, {beforeMs: value.prePaddingMs, afterMs: value.postPaddingMs,
      keepNewest: policy.mode === 'keep-count' ? policy.keepCount : 0, keepDays: policy.mode === 'keep-days' ? policy.keepDays : 0}, signal));
    this.dvrDocument = next; return dvrDocumentView(next);
  };
  /** The folder explorer's listings for one opening: GET filesystem reads only, within this
   * owner's lifetime and retired with the opening's signal. */
  folderBrowser = (opening: AbortSignal): LibraryContentApi => ({request: <T,>(path: string, method?: string, _body?: unknown, caller?: AbortSignal): Promise<T> => {
    if (opening.aborted || path.split('?')[0] !== routePath('get_admin_filesystem') || method && method !== 'GET') return Promise.reject(screenScopeEnded());
    const round = new AbortController(), stop = () => round.abort();
    opening.addEventListener('abort', stop, {once: true}); caller?.addEventListener('abort', stop, {once: true});
    if (caller?.aborted) stop();
    return this.lifetime.request(next => this.api.request<T>(path, 'GET', undefined, next), round.signal)
      .finally(() => {opening.removeEventListener('abort', stop); caller?.removeEventListener('abort', stop);});
  }});
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
