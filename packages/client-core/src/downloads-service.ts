import type {Download, DownloadedItem, DownloadTitle as ServerTitle} from '../../contracts/src/next/api.ts';
import type {GetContainerDownloadOptionsResponse} from '../../contracts/src/types.ts';
import {parseDownloadOptions} from './downloads.ts';
import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {qualityPresentation} from './playback-v1/quality-presentation.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {randomId} from './random-id.ts';
import {serviceProblem} from './presentation/service-text.ts';
import {downloadReasonMessage, originalQuality, type DownloadAction, type DownloadBatch, type DownloadBundle, type DownloadGrant, type DownloadOptionsView, type DownloadPreparation, type DownloadQualityChoice, type DownloadTitle, type DownloadUsage} from './downloads.ts';
import type {DownloadEpisodes, DownloadRequest, DownloadRequestKind} from './download-requests.ts';
import {askForDownload, changeDownload, listDownloads, readDownload, readDownloadedItem, readDownloadOptions, readDownloadUsage, removeDownloadedTitle} from './device-downloads.ts';

export type DownloadsApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
export type DownloadTarget = Readonly<{versionId?: string; mediaId?: string; mediaIds?: readonly string[]; nextAfterMediaId?: string}>;
export type ContainerDownloadTarget = Readonly<{kind: DownloadRequestKind; id: string; title?: string}>;
export type ContainerDownloadPolicy = Readonly<{episodes: DownloadEpisodes; keepNext?: number}>;
export type TrackedDownloadRequest = Readonly<{request: DownloadRequest; title: string; error?: string}>;
export type DownloadsSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; items: readonly DownloadPreparation[]; usage?: DownloadUsage; busy: readonly string[]; error?: string; failure?: unknown; unavailable?: boolean; nextCursor: string; loadingMore: boolean; requests: readonly TrackedDownloadRequest[]}>;

/** Stable local identity for one title of a request. The length makes both ids reversible. */
export const downloadCopyId = (downloadId: string, itemId: string) => `${downloadId.length}_${downloadId}${itemId}`;
export function downloadCopyReference(id: string): {downloadId: string; itemId: string} {
  const end = id.indexOf('_'), length = Number(id.slice(0, end)), rest = id.slice(end + 1);
  if (end < 1 || !Number.isSafeInteger(length) || length < 1 || length >= rest.length || !/^[A-Za-z0-9_-]+$/.test(rest)) throw new Error(unreadableServerResponse);
  return {downloadId: rest.slice(0, length), itemId: rest.slice(length)};
}
const titleFacts = (title: ServerTitle): DownloadTitle => ({kind: title.kind === 'book' ? 'ebook' : title.kind === 'audiobook' ? 'book' : title.kind === 'track' ? 'song' : title.kind === 'issue' ? 'comic' : title.kind, title: title.title, parentTitle: title.parentTitle ?? '', parentId: '', groupId: '', groupTitle: '', groupNumber: title.seasonNumber ?? null, number: title.episodeNumber ?? null, artist: title.artist ?? '', year: 0, durationSeconds: 0, onServer: true});
export function downloadPreparation(download: Download, title: ServerTitle, profileId = ''): DownloadPreparation {
  const state = download.paused ? 'paused' : title.state === 'waiting' ? 'queued' : title.state === 'preparing' ? 'running' : title.state === 'leftOut' ? 'unavailable' : title.state === 'ready' || title.state === 'expired' || title.state === 'failed' ? title.state : 'queued';
  const total = title.sizeBytes ?? title.totalBytes ?? 0, done = title.doneBytes ?? 0;
  return Object.freeze<DownloadPreparation>({id: downloadCopyId(download.id, title.itemId), downloadId: download.id, itemId: title.itemId, versionId: download.versionId, libraryId: '', profileId,
    title: titleFacts(title), quality: download.quality, origin: download.counts.titles > 1 ? 'container' : 'item', batchId: download.id, state, reason: title.reason ?? '',
    artifact: {kind: download.quality === 'original' ? 'source' : 'prepared', ref: download.versionId ?? '', sha256: '', bytes: total, estimated: state !== 'ready', container: '', contentType: '', fileName: ''},
    progress: {bytesDone: done, bytesTotal: total, bytesTotalEstimated: state !== 'ready', percent: total > 0 ? Math.min(100, done / total * 100) : 0, etaSeconds: null},
    actions: state === 'ready' ? ['remove'] : state === 'paused' ? ['resume', 'remove'] : state === 'failed' || state === 'expired' ? ['retry', 'remove'] : ['pause', 'remove'],
    revision: 0, createdAt: download.createdAt, updatedAt: download.createdAt, readyAt: state === 'ready' ? download.createdAt : '', expiresAt: title.keptUntil ?? ''});
}
function requestFacts(download: Download, profileId: string): DownloadRequest {
  const c = download.counts;
  return {requestId: download.id, total: c.titles, totalKnown: true, state: c.waiting ? 'preparing' : 'complete', ready: c.ready, preparing: download.items?.filter(t => t.state === 'preparing').length ?? 0,
    queued: Math.max(0, c.waiting - (download.items?.filter(t => t.state === 'preparing').length ?? 0)), failed: c.failed, paused: download.paused ? c.waiting : 0,
    items: (download.items ?? []).map(title => ({itemId: title.itemId, reason: title.reason, preparation: downloadPreparation(download, title, profileId)})), nextCursor: ''};
}
export function downloadedBundle(copyId: string, downloaded: DownloadedItem): DownloadBundle {
  const item = downloaded.item;
  return {preparationId: copyId, itemId: item.id, quality: downloaded.quality, qualityLabel: downloaded.quality,
    title: {...titleFacts({itemId: item.id, kind: item.kind, title: item.title, state: 'ready'}), year: item.year ?? 0, durationSeconds: (item.durationMs ?? 0) / 1000, parentId: item.parentId ?? '', parentTitle: item.parent?.title ?? ''}, overview: item.summary ?? '',
    media: {container: downloaded.files[0]?.container ?? '', videoCodec: '', audioCodec: '', width: 0, height: 0}, artwork: [],
    chapters: downloaded.chapters.map((c, index) => ({index, title: c.title ?? '', startSeconds: c.startMs / 1000, endSeconds: (downloaded.chapters[index + 1]?.startMs ?? item.durationMs ?? c.startMs) / 1000})),
    markers: downloaded.markers.map(m => ({kind: m.type, startSeconds: m.startMs / 1000, endSeconds: m.endMs / 1000, automatic: m.automaticSafe})),
    audio: downloaded.audio.map((a, index) => ({index: Number(a.id) || index, language: a.language ?? '', title: a.title ?? '', codec: a.codec, channels: a.channels ?? 0, default: a.default ?? false})),
    subtitles: downloaded.subtitles.filter(s => s.url).map(s => {
      const form = s.format === 'srt' ? 'vtt' : s.format === 'ssa' ? 'ass' : s.format === 'sup' ? 'pgs' : s.format;
      return {id: s.id, revision: 1, language: s.language ?? '', title: s.title ?? '', default: s.default, forced: s.forced, files: {[form]: {path: s.url!, contentType: 'application/octet-stream'}}};
    })};
}

/** The rebuilt server's downloads, adapted once to the existing phone/web presentation model. */
export class DownloadsService {
  private state: DownloadsSnapshot = Object.freeze({phase: 'idle', items: [], busy: [], nextCursor: '', loadingMore: false, requests: []});
  private listeners = new Set<() => void>();
  private lifetime = new AbortController();
  private timer?: ReturnType<typeof setTimeout>;
  private watching = false;
  private disposed = false;
  private generation = 0;
  private refreshing?: Promise<void>;
  private deviceId = '';
  private downloads = new Map<string, Download>();
  private api: DownloadsApi; private operationId: () => string; private pollMs: number; private profileId: string; private everyDevice: boolean;
  constructor(api: DownloadsApi, operationId: () => string = randomId, pollMs = 5000, profileId = '', everyDevice = true) {this.api = api; this.operationId = operationId; this.pollMs = pollMs; this.profileId = profileId; this.everyDevice = everyDevice;}
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {this.listeners.add(fn); return () => {this.listeners.delete(fn);};};
  private publish(patch: Partial<DownloadsSnapshot>) {if (this.disposed) return; this.state = Object.freeze({...this.state, ...patch}); this.listeners.forEach(fn => fn());}
  dispose() {this.disposed = true; this.generation++; this.stop(); this.lifetime.abort(); this.listeners.clear();}
  start() {if (this.watching) return; this.watching = true; void this.refresh();}
  stop() {this.watching = false; clearTimeout(this.timer);}
  followDevice(deviceId: string) {if (this.deviceId === deviceId) return; this.deviceId = deviceId; if (this.watching) void this.refresh();}
  private show(downloads: readonly Download[]) {
    this.downloads = new Map(downloads.map(d => [d.id, d]));
    this.publish({phase: 'ready', error: undefined, failure: undefined, unavailable: false,
      items: downloads.flatMap(d => (d.items ?? []).map(t => downloadPreparation(d, t, this.profileId))),
      requests: downloads.filter(d => this.deviceId && d.deviceId === this.deviceId && (d.counts.titles > 1 || ['show', 'season', 'album', 'artist', 'playlist', 'book'].includes(d.target.kind))).map(d => ({title: d.target.title, request: requestFacts(d, this.profileId)}))});
  }
  refresh(): Promise<void> {
    if (this.disposed) return Promise.resolve();
    if (this.refreshing) return this.refreshing;
    const generation = this.generation;
    clearTimeout(this.timer);
    if (this.state.phase === 'idle') this.publish({phase: 'loading'});
    const task = (async () => {
      try {
        const downloads = await listDownloads(this.api, this.everyDevice, this.lifetime.signal);
        if (generation !== this.generation || this.disposed) return;
        this.show(downloads);
        void readDownloadUsage(this.api, this.lifetime.signal).then(u => {if (generation === this.generation) this.publish({usage: {profileId: this.profileId, profileBytes: u.copiesBytes, profileCount: downloads.length, serverBytes: u.copiesBytes, serverCount: downloads.length, distinctArtifactBytes: u.copiesBytes, maxPreparedBytes: 0, remainingBytes: u.roomBytes ?? null, retentionDays: 0, profiles: []}});}, () => {});
      } catch (e) {
        if (generation !== this.generation || this.disposed) return;
        const unavailable = (e as {status?: number; code?: string})?.status === 404 || (e as {code?: string})?.code === 'downloads_unavailable';
        this.publish(unavailable ? {phase: 'ready', unavailable: true, error: undefined, failure: undefined} : {phase: this.state.items.length ? 'ready' : 'error', unavailable: false, failure: e, error: serviceProblem(e, 'downloads', 'downloads.error.load', 'load')});
      } finally {
        if (this.watching && !this.disposed && generation === this.generation && (this.state.failure || this.state.items.some(p => p.state === 'queued' || p.state === 'running'))) this.timer = setTimeout(() => void this.refresh(), this.pollMs);
      }
    })();
    this.refreshing = task.finally(() => {if (this.refreshing === pending) this.refreshing = undefined;});
    const pending = this.refreshing; return pending;
  }
  revalidate = () => this.refresh();
  refreshRequests = () => this.refresh();
  async loadMore(): Promise<void> {} // The rebuilt server returns the complete device list.
  async lookupPreparations(ids: readonly string[], signal?: AbortSignal): Promise<readonly DownloadPreparation[]> {
    if (ids.length > 200) throw new Error('Read up to 200 preparation ids.');
    if (this.disposed || signal?.aborted) throw Object.assign(new Error('Download lookup cancelled.'), {name: 'AbortError'});
    const refs = [...new Set(ids)].map(id => ({id, ...downloadCopyReference(id)}));
    const controller = new AbortController(), abort = () => controller.abort();
    signal?.addEventListener('abort', abort, {once: true}); this.lifetime.signal.addEventListener('abort', abort, {once: true});
    try {
      const found: DownloadPreparation[] = [];
      for (const downloadId of new Set(refs.map(r => r.downloadId))) {
        const d = await readDownload(this.api, downloadId, controller.signal);
        const wanted = new Set(refs.filter(r => r.downloadId === downloadId).map(r => r.itemId));
        for (const title of d.items ?? []) if (wanted.has(title.itemId)) found.push(downloadPreparation(d, title, this.profileId));
      }
      return found;
    } finally {signal?.removeEventListener('abort', abort); this.lifetime.signal.removeEventListener('abort', abort);}
  }
  async qualities(_signal?: AbortSignal): Promise<readonly DownloadQualityChoice[]> {
    return canonicalQualityLadder.filter(q => q.id !== 'automatic').map(q => ({quality: q.id, label: qualityPresentation(q.id).label, kind: q.id === 'original' ? 'source' as const : 'optimized' as const, targetDisplayHeight: q.height}));
  }
  downloaded(id: string, signal?: AbortSignal): Promise<DownloadedItem> {const ref = downloadCopyReference(id); return readDownloadedItem(this.api, ref.downloadId, ref.itemId, signal ?? this.lifetime.signal);}
  async options(itemId: string, signal?: AbortSignal): Promise<DownloadOptionsView> {
    return parseDownloadOptions(await readDownloadOptions(this.api, itemId, {}, signal), itemId);
  }
  async containerOptions(target: ContainerDownloadTarget, _deviceId: string, policy: ContainerDownloadPolicy, signal?: AbortSignal): Promise<GetContainerDownloadOptionsResponse> {
    const menu = await readDownloadOptions(this.api, target.id, {selection: policy.episodes, ...(policy.episodes === 'next' ? {keepNext: policy.keepNext} : {})}, signal);
    return {target: {id: target.id, kind: target.kind}, total: menu.titles, options: menu.items.map(o => ({quality: o.quality, label: o.label, available: o.available, estimatedBytes: o.sizeBytes, estimated: o.estimated, sameAsOriginal: o.quality === 'original', unknownSizeCount: 0, unavailableCount: o.available ? 0 : menu.titles}))};
  }
  async request(target: DownloadTarget, quality = originalQuality, operationId?: string): Promise<DownloadBatch> {
    if (operationId !== undefined && !/^[A-Za-z0-9_-]{1,160}$/.test(operationId)) throw new Error('A download request needs a stable operationId.');
    const ids = target.mediaIds ?? (target.mediaId ? [target.mediaId] : []);
    if (!ids.length) throw new Error('Choose a title to download.');
    const items: DownloadPreparation[] = [], rejected: {itemId: string; code: string}[] = [];
    let batchId = '', duplicate = true;
    for (const [index, itemId] of ids.entries()) {
      const asked = await askForDownload(this.api, {itemId, versionId: target.versionId, quality, operationId: ids.length === 1 ? operationId ?? this.operationId() : `${operationId ?? this.operationId()}_${index}`}, this.lifetime.signal);
      const d = await readDownload(this.api, asked.id, this.lifetime.signal);
      batchId ||= d.id; duplicate &&= this.downloads.has(d.id); this.downloads.set(d.id, d);
      for (const t of d.items ?? []) t.state === 'leftOut' ? rejected.push({itemId: t.itemId, code: t.reason ?? 'source_unavailable'}) : items.push(downloadPreparation(d, t, this.profileId));
    }
    void (this.refreshing ?? Promise.resolve()).then(() => this.refresh()); return {batchId, items, rejected, accepted: items.length, duplicate};
  }
  async requestContainer(input: Readonly<{target: ContainerDownloadTarget; deviceId: string; quality?: string; policy: ContainerDownloadPolicy; operationId?: string}>): Promise<DownloadRequest> {
    const asked = await askForDownload(this.api, {...(input.target.kind === 'playlist' ? {listId: input.target.id} : {itemId: input.target.id}), operationId: input.operationId ?? this.operationId(), quality: input.quality ?? originalQuality, selection: input.policy.episodes, ...(input.policy.episodes === 'next' ? {keepNext: input.policy.keepNext} : {})}, this.lifetime.signal);
    const d = await readDownload(this.api, asked.id, this.lifetime.signal);
    this.downloads.set(d.id, d); void (this.refreshing ?? Promise.resolve()).then(() => this.refresh()); return requestFacts(d, this.profileId);
  }
  async act(preparation: DownloadPreparation, action: DownloadAction): Promise<void> {
    const {downloadId, itemId} = downloadCopyReference(preparation.id);
    this.publish({busy: [...this.state.busy, preparation.id], error: undefined, failure: undefined});
    try {
      if (action === 'remove' || action === 'cancel') await removeDownloadedTitle(this.api, downloadId, itemId, this.lifetime.signal);
      else await changeDownload(this.api, downloadId, action === 'retry' ? {retry: true} : {paused: action === 'pause'}, this.lifetime.signal);
      await this.refreshing; await this.refresh();
    } catch (e) {this.publish({failure: e, error: serviceProblem(e, 'downloads', 'downloads.error.action', 'action')});}
    finally {this.publish({busy: this.state.busy.filter(id => id !== preparation.id)});}
  }
  async grant(preparation: Pick<DownloadPreparation, 'id'>, _renew?: string): Promise<DownloadGrant> {
    const {downloadId, itemId} = downloadCopyReference(preparation.id), downloaded = await readDownloadedItem(this.api, downloadId, itemId, this.lifetime.signal);
    if (downloaded.files.length !== 1) throw Object.assign(new Error(downloadReasonMessage('several_files')), {code: 'several_files'});
    const f = downloaded.files[0];
    if (!Number.isSafeInteger(f.sizeBytes) || f.sizeBytes < 1 || !downloaded.versionId || downloaded.quality !== 'original' && !/^[a-f0-9]{64}$/.test(f.sha256 ?? '')) throw new Error(unreadableServerResponse);
    return {preparationId: preparation.id, itemId, url: f.url, token: '', issuedAt: '', expiresAt: '', renewAfterSeconds: 0, renewed: false,
      artifact: {kind: downloaded.quality === 'original' ? 'source' : 'prepared', ref: downloaded.versionId, sha256: f.sha256 ?? '', bytes: f.sizeBytes, estimated: false, container: f.container ?? '', fileName: `${downloaded.item.title}.${f.container ?? 'bin'}`, contentType: ''}};
  }
  async bundle(preparation: Pick<DownloadPreparation, 'id'>, signal?: AbortSignal): Promise<DownloadBundle> {
    const {downloadId, itemId} = downloadCopyReference(preparation.id); return downloadedBundle(preparation.id, await readDownloadedItem(this.api, downloadId, itemId, signal));
  }
  reason(preparation: DownloadPreparation): string {return preparation.reason ? downloadReasonMessage(preparation.reason) : '';}
}
