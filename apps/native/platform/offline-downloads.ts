import {AppState, NativeEventEmitter, NativeModules} from 'react-native';
import {keepDownloadsInStep, listDownloads, readDownload, readDownloadedTitle, removeDownload, removeDownloadedTitle, reportDownloadPlays, askForDownload, type DownloadHoldings, type OfflinePermission} from '../../../packages/client-core/src/device-downloads';
import {clockAtContact, freshOfflineClock, mayPlayOffline, type OfflineClock} from '../../../packages/client-core/src/offline-clock';
import {downloadTitleLines, type DownloadBundleSubtitle, type DownloadBundleTrickplay, type DownloadTitle} from '../../../packages/client-core/src/downloads';
import {serviceText} from '../../../packages/client-core/src/presentation/service-text';
import type {Download, DownloadAsk, DownloadedItem, HeldRemoval, HeldTitle, OfflinePlay, PlaybackChapter, PlaybackMarker} from '../../../packages/contracts/src/next/api';
import type {MessageId} from '../../../packages/i18n/src/index';
import type {NativeOfflinePublicationLease} from './offline-reading-authority';
import type {OfflineReadingSession, PublicationBinding} from './offline-reading';
import type {ReadingLocation} from '../../../packages/client-core/src/reading';
import {storedSubtitle, subtitleExtraName, type StoredSubtitle} from './offline-subtitle-files';
import type {LocalMedia, LocalProgress} from './local-playback';
import {offlineCompletion} from './offline-completion';

/** Downloads kept on this phone or tablet, on the new server.
 *
 * The server decides what this device holds: at every contact (the app opening or coming to the
 * front, the network coming back, a new download asked for) the device says what it holds, and
 * the server answers what to remove, what is ready to fetch, and until when everything plays
 * without reaching it again (client-core `keepDownloadsInStep`). This file only handles files:
 * the native module moves, checks and stores them, and the title's details, artwork and
 * subtitles are kept beside them, so nothing on the Downloads list asks the server for anything.
 *
 * A download plays offline while the last contact's permission lasts (30 days), judged by a clock
 * that cannot be turned back (client-core `offline-clock`). Plays made offline are kept and sent
 * through the state batch at the next contact, each once by its play id. */

export type OfflineState = 'downloading' | 'paused' | 'failed' | 'ready';
export type OfflineWaiting = '' | 'wifi' | 'network';
/** The file a download holds: its request, and the size and digest it was checked against. */
export type OfflineCopy = Readonly<{downloadId: string; sha256: string; bytes: number}>;
export type OfflineEntry = Readonly<{
  id: string; itemId: string; title: string; subtitle: string; quality: string;
  /** The poster or cover kept with the download (a file on this device), or ''. */
  artworkUrl: string;
  state: OfflineState; error: string; waiting: OfflineWaiting;
  /** The person paused it. Anything else that stopped is started again at the next contact. */
  held: boolean;
  bytes: number; bytesDone: number; addedAt: number;
  copy?: OfflineCopy; libraryId?: string; kind?: string;
  /** The viewer (`offlineOwnerKey`) this download belongs to. */
  owner?: string;
  /** What the download is of, as the server described it when it was fetched. */
  info?: DownloadTitle;
  /** Kept for the screens: the new server removes what a viewer may no longer see. */
  onServer: boolean;
  changedOnServer: boolean;
}>;
export type OfflineSettings = Readonly<{wifiOnly: boolean; deleteWatched: boolean; autoNext: boolean; limitBytes: number}>;
export type OfflinePlayabilityReason = 'sign_in'|'not_finished'|'needs_setup'|'needs_check_in'|'other_profile';
export type OfflineEntryFacts = Readonly<{
  resumePositionSeconds?: number;
  backdropUrl?: string;
  media?: Readonly<{container?: string; videoCodec?: string; audioCodec?: string; width?: number; height?: number}>;
}>;
export type OfflineReadingFacts = Readonly<{format: PublicationBinding['format']; pageCount?: number; location?: ReadingLocation}>;
export type OfflinePendingDownload = Readonly<{id: string; itemId: string; title: string; subtitle?: string; reason: string}>;
export type OfflineSnapshot = Readonly<{supported: boolean; ready: boolean; entries: readonly OfflineEntry[]; pendingDownloads?: readonly OfflinePendingDownload[]; settings: OfflineSettings;
  freeBytes: number; usedBytes: number; totalBytes?: number; storedBytes?: number; allocatedBytes?: number; pendingProgress: number;
  /** Space taken by other viewers' downloads on this device, and an opaque revision of that set. */
  otherBytes: number; othersRevision?: number; error?: string}>;
export type OfflineOthersTarget = Readonly<{revision: number; bytes: number}>;
export type Viewer = Readonly<{authority: string; accountId: string; profileId: string; serverId: string}>;
type Api = {baseUrl: string; mediaUrl(path: string): string; request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
/** What the server said about a title, kept beside its file for the player and the screens. */
export type OfflineBundle = Readonly<{item: DownloadedItem['item']; quality: string; media: Readonly<{container: string}>;
  chapters: readonly PlaybackChapter[]; markers: readonly PlaybackMarker[]; subtitles: readonly DownloadBundleSubtitle[]; trickplay?: DownloadBundleTrickplay}>;
export type OfflineExtras = Readonly<{bundle?: OfflineBundle; subtitles: readonly StoredSubtitle[]; backdropUrl: string; trickplayTiles?: readonly string[]; chapterImages?: Readonly<Record<number, string>>}>;

type Meta = {owner?: string; downloadId?: string; single?: boolean; title?: DownloadTitle; kind?: string; libraryId?: string; sha256?: string; extras?: string[]};
type Owned<T> = Record<string, Record<string, T>>;
type KeptPlay = {itemId: string; positionMs: number; playedMs: number; startMs: number; at: number};
type Stored = {
  version: 4;
  settings: OfflineSettings;
  /** The permission each viewer was given at its last contact, and the one clock it is judged by. */
  permissions: Record<string, OfflinePermission>;
  clock: OfflineClock;
  /** Plays not yet answered by the server, by viewer and play id. */
  plays: Owned<KeptPlay>;
  resume: Owned<number>;
  /** Removals made here while out of reach, told to the server at the next contact: a request's id, or
   * `downloadId/itemId` for one title of a show, season, album or playlist. */
  removals: Record<string, string[]>;
};

const native = NativeModules.PorticoDownloads as undefined | {
  list(): Promise<any[]>; start(request: Record<string, unknown>): Promise<any>; pause(id: string): Promise<void>; remove(id: string): Promise<void>;
  setMeta(id: string, meta: string): Promise<void>;
  storage(): Promise<{freeBytes: number; usedBytes: number; totalBytes?: number; storedBytes?: number; allocatedBytes?: number}>; setCellular(allowed: boolean): Promise<void>;
  readState(): Promise<string>; writeState(json: string): Promise<void>;
  localFile(id: string): Promise<{url: string; durationSeconds: number}>;
  writeExtra(id: string, name: string, text: string): Promise<void>; readExtra(id: string, name: string): Promise<string>;
  /** 'saved', 'missing', 'later' or 'invalid'. */
  fetchExtra(id: string, name: string, url: string): Promise<string>;
};
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const defaults: OfflineSettings = Object.freeze({wifiOnly: true, deleteWatched: false, autoNext: false, limitBytes: 0});
const random = () => Math.random().toString(36).slice(2) + Date.now().toString(36);
const emptyStored = (settings: OfflineSettings = defaults): Stored => ({version: 4, settings, permissions: {}, clock: freshOfflineClock, plays: {}, resume: {}, removals: {}});
function readStored(raw: unknown): Stored {
  if (!object(raw)) return emptyStored();
  const settings = object(raw.settings) ? {...defaults, ...raw.settings} as OfflineSettings : defaults;
  // A device that held the old server's downloads starts again, keeping only its settings.
  if (raw.version !== 4) return emptyStored(settings);
  const owned = <T,>(value: unknown) => (object(value) ? value : {}) as Owned<T>;
  return {version: 4, settings, permissions: (object(raw.permissions) ? raw.permissions : {}) as Record<string, OfflinePermission>,
    clock: object(raw.clock) ? raw.clock as OfflineClock : freshOfflineClock, plays: owned(raw.plays), resume: owned(raw.resume),
    removals: (object(raw.removals) ? raw.removals : {}) as Record<string, string[]>};
}
const bucket = <T,>(owned: Owned<T>, owner: string) => (owned[owner] ??= {});
/** Unsent plays kept per viewer: beyond this the latest play of each title stays. */
const MAX_PLAYS = 400;
/** While the server is still making copies for this device, it is asked again this often. */
const PREPARING_CONTACT_MS = 60 * 1000;

/** One viewer on one server; every record and native entry is filed under this exact string. */
export const offlineOwnerKey = (viewer: Viewer) => JSON.stringify([viewer.authority, viewer.serverId, viewer.accountId, viewer.profileId]);
/** The native entry for one title of a request. */
const entryId = (title: HeldTitle) => `dl-${title.downloadId}-${title.itemId}`.replace(/[^A-Za-z0-9_-]/g, '_');
const sort = (entries: OfflineEntry[]) => entries.sort((a, b) => b.addedAt - a.addedAt);

/** The screens' title facts, from the server's item and its request. */
function titleInfo(item: DownloadedItem['item'], download: Download): DownloadTitle {
  const kind = item.kind === 'track' ? 'song' : item.kind;
  const parent = item.kind === 'episode' ? item.root ?? item.parent : item.parent;
  return Object.freeze({kind, title: item.title, year: item.year ?? 0, durationSeconds: Math.round((item.durationMs ?? 0) / 1000),
    parentId: parent?.id ?? '', parentTitle: parent?.title ?? (download.target.id === item.id ? '' : download.target.title),
    groupId: item.kind === 'episode' ? item.parentId ?? '' : '', groupTitle: item.kind === 'episode' ? item.parent?.title ?? '' : '',
    groupNumber: item.kind === 'episode' ? item.parentIndex ?? null : null, number: item.index ?? null, artist: item.kind === 'track' ? item.root?.title ?? '' : '', onServer: true});
}
/** The subtitles that come as their own files; those inside the file play from it. */
const sideSubtitles = (item: DownloadedItem) => item.subtitles.filter(subtitle => !!subtitle.url && (!subtitle.part || subtitle.part === 1));
function bundleOf(item: DownloadedItem): OfflineBundle {
  return Object.freeze({item: item.item, quality: item.quality, media: {container: item.files[0]?.container ?? ''}, chapters: item.chapters, markers: item.markers,
    subtitles: sideSubtitles(item).map(subtitle => Object.freeze({id: subtitle.id, revision: 0, language: subtitle.language ?? '', title: subtitle.title ?? '',
      default: subtitle.default, forced: subtitle.forced, files: {[subtitle.format]: {path: subtitle.url!, contentType: ''}}}))});
}

export class OfflineDownloads {
  private state: OfflineSnapshot = Object.freeze({supported: !!native, ready: false, entries: [], settings: defaults, freeBytes: 0, usedBytes: 0, pendingProgress: 0, otherBytes: 0});
  private listeners = new Set<() => void>();
  private stored: Stored = emptyStored();
  private all: OfflineEntry[] = [];
  private metas = new Map<string, Meta>();
  private dirs = new Map<string, string>();
  private viewer?: Viewer;
  private api?: Api;
  private generation = 0;
  private opening?: Promise<void>;
  private disposed = false;
  private storageMeasured = false;
  private contacting?: Promise<void>;
  private nextContact?: ReturnType<typeof setTimeout>;
  private subscriptions: {remove(): void}[] = [];
  private playing = new Map<string, {playId: string; startMs: number}>();
  private lastLocalWrite = new Map<string, number>();

  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  private publish(patch: Partial<OfflineSnapshot>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private ownerKey() { return this.viewer && offlineOwnerKey(this.viewer); }
  private publishEntries() {
    const owner = this.ownerKey(), mine = this.all.filter(entry => entry.owner === owner), others = this.all.filter(entry => entry.owner !== owner);
    const otherBytes = others.reduce((sum, entry) => sum + Math.max(entry.bytes, entry.bytesDone), 0);
    let revision = 0;
    for (const char of JSON.stringify(others.map(entry => [entry.id, entry.bytes]))) revision = (revision * 31 + char.charCodeAt(0)) | 0;
    this.publish({entries: Object.freeze(mine), otherBytes, othersRevision: revision, pendingProgress: owner ? Object.keys(this.stored.plays[owner] ?? {}).length : 0});
  }
  private persist(): Promise<boolean> {
    return native ? native.writeState(JSON.stringify(this.stored)).then(() => true, () => false) : Promise.resolve(true);
  }

  open(): Promise<void> {
    if (this.disposed || !native || this.state.ready) return Promise.resolve();
    this.opening ??= this.readDevice().finally(() => { this.opening = undefined; });
    return this.opening;
  }
  private async readDevice(): Promise<void> {
    try {
      const raw = await native!.readState();
      this.stored = raw === '' ? emptyStored() : readStored(JSON.parse(raw));
      const emitter = new NativeEventEmitter(NativeModules.PorticoDownloads);
      this.subscriptions.push(emitter.addListener('PorticoDownloadChanged', entry => this.accept(entry)));
      this.subscriptions.push(AppState.addEventListener('change', value => { if (value === 'active') this.wake(); }));
      for (const entry of await native!.list()) this.accept(entry, false);
      this.publish({ready: true, settings: this.stored.settings, error: undefined});
      this.publishEntries();
      void native!.setCellular(!this.stored.settings.wifiOnly).catch(() => {});
      await this.measure();
    } catch (error) {
      this.publish({error: serviceText('downloads.deviceUnreadable')});
      throw error;
    }
  }
  private accept(raw: any, publish = true) {
    if (this.disposed || !raw || typeof raw.id !== 'string') return;
    if (raw.state === 'removed') { this.all = this.all.filter(entry => entry.id !== raw.id); this.metas.delete(raw.id); this.dirs.delete(raw.id); }
    else if (['downloading', 'paused', 'failed', 'ready'].includes(raw.state)) {
      let meta: Meta = {}; try { meta = raw.meta ? JSON.parse(raw.meta) : {}; } catch { meta = {}; }
      const dir = typeof raw.dir === 'string' ? raw.dir : this.dirs.get(raw.id) ?? '';
      if (dir) this.dirs.set(raw.id, dir);
      this.metas.set(raw.id, meta);
      const lines = meta.title ? downloadTitleLines(meta.title) : {title: String(raw.title ?? ''), subtitle: String(raw.subtitle ?? '')};
      const art = (meta.extras ?? []).includes('poster') ? 'poster' : undefined;
      const entry: OfflineEntry = Object.freeze({id: raw.id, itemId: String(raw.itemId ?? ''), title: lines.title, subtitle: lines.subtitle, quality: String(raw.quality ?? ''),
        artworkUrl: art && dir ? dir + 'extras/' + art : '', state: raw.state, error: String(raw.error ?? ''),
        waiting: raw.waiting === 'wifi' || raw.waiting === 'network' ? raw.waiting : '', held: raw.intent === 'hold',
        bytes: Number(raw.bytes) || 0, bytesDone: Number(raw.bytesDone) || 0, addedAt: Number(raw.addedAt) || 0,
        ...(meta.downloadId ? {copy: Object.freeze({downloadId: meta.downloadId, sha256: meta.sha256 ?? '', bytes: Number(raw.bytes) || 0})} : {}),
        libraryId: meta.libraryId, kind: meta.kind, owner: meta.owner, info: meta.title, onServer: true, changedOnServer: false});
      this.all = sort([...this.all.filter(value => value.id !== entry.id), entry]);
    }
    if (publish) { this.publishEntries(); if (raw.state === 'ready' || raw.state === 'removed') void this.measure(); }
  }
  private async measure() {
    try {
      const s = await native!.storage();
      this.storageMeasured = true;
      // Only a measured whole number is shown; a total the system did not report stays unknown, never made up.
      const count = (value: unknown, least: number) => Number.isSafeInteger(value) && (value as number) >= least ? value as number : undefined;
      const total = count(s.totalBytes, 1), stored = count(s.storedBytes, 0), allocated = count(s.allocatedBytes, 0);
      this.publish({freeBytes: count(s.freeBytes, 0) ?? 0, usedBytes: count(s.usedBytes, 0) ?? 0, totalBytes: total, storedBytes: stored, allocatedBytes: allocated});
    } catch { /* measured again after the next change */ }
  }

  /** Names the viewer whose downloads are listed and may play, with no server: a cold offline launch. */
  adopt(viewer: Viewer | undefined) {
    if (this.disposed) return;
    if ((viewer && offlineOwnerKey(viewer)) === this.ownerKey()) return;
    this.disconnect();
    this.viewer = viewer;
    if (viewer) void this.open().catch(() => {});
    this.publishEntries();
  }
  /** Binds the signed-in server: the device is brought in step with it now. */
  connect(api: Api, viewer: Viewer) {
    if (this.disposed) return;
    this.adopt(viewer);
    this.generation++;
    this.api = api;
    this.wake();
  }
  /** The app came to the front, the network came back, or something was asked for: a contact. */
  wake() {
    if (!this.api || this.disposed) return;
    const generation = this.generation;
    this.contacting ??= this.open().then(() => this.contact(generation)).catch(() => {}).finally(() => { this.contacting = undefined; });
  }
  disconnect() { this.generation++; this.api = undefined; clearTimeout(this.nextContact); this.nextContact = undefined; this.publish({pendingDownloads: undefined}); }
  dispose() { this.disposed = true; this.disconnect(); this.subscriptions.forEach(s => s.remove()); this.subscriptions = []; this.listeners.clear(); }

  private async contact(generation: number) {
    const api = this.api, owner = this.ownerKey();
    if (!api || !owner || !native || generation !== this.generation) return;
    const current = () => generation === this.generation && this.ownerKey() === owner;
    // Requests removed here while out of reach go first, so they are not fetched again.
    for (const id of [...this.stored.removals[owner] ?? []]) {
      await this.tellRemoved(api, id);
      this.stored.removals[owner] = (this.stored.removals[owner] ?? []).filter(value => value !== id);
      await this.persist();
    }
    if (!current()) return;
    await keepDownloadsInStep(api, this.holdings(owner, api, current));
    if (!current()) return;
    // What stopped by itself (not paused by the person) is started again.
    for (const entry of this.all) if (entry.owner === owner && entry.state === 'failed' && !entry.held && entry.copy) await this.fetchTitle({downloadId: entry.copy.downloadId, itemId: entry.itemId}, owner, api, current, true).catch(() => {});
    await this.sendPlays(owner, api).catch(() => {});
    // The server may still be making copies for this device: ask again in a while.
    const mine = await listDownloads(api).catch(() => undefined);
    if (!current() || !mine) return;
    const held = new Set(this.all.filter(entry => entry.owner === owner).map(entry => entry.copy?.downloadId));
    this.publish({pendingDownloads: Object.freeze(mine.filter(download => download.counts.waiting > 0 || !held.has(download.id) && download.counts.ready === 0)
      .map(download => Object.freeze({id: download.id, itemId: download.target.id, title: download.target.title, reason: serviceText('downloads.blocked.preparing')})))});
    clearTimeout(this.nextContact);
    if (mine.some(download => download.counts.waiting > 0 && !download.paused)) this.nextContact = setTimeout(() => this.wake(), PREPARING_CONTACT_MS);
  }
  private holdings(owner: string, api: Api, current: () => boolean): DownloadHoldings {
    return {
      held: async () => this.all.filter(entry => entry.owner === owner && entry.copy).map(entry => ({downloadId: entry.copy!.downloadId, itemId: entry.itemId})),
      remove: async (title: HeldRemoval) => {
        const entry = this.all.find(value => value.id === entryId(title) && value.owner === owner);
        if (entry) await this.forget(entry);
      },
      fetch: (title: HeldTitle) => this.fetchTitle(title, owner, api, current, false),
      renew: async (permission: OfflinePermission) => {
        if (!current()) return;
        this.stored.permissions[owner] = permission;
        this.stored.clock = clockAtContact(this.stored.clock, permission);
        await this.persist();
        this.publishEntries();
      },
    };
  }
  /** Starts fetching one title: its file by the native module, then its details, artwork and subtitles beside it. */
  private async fetchTitle(title: HeldTitle, owner: string, api: Api, current: () => boolean, again: boolean): Promise<void> {
    const id = entryId(title), live = this.all.find(entry => entry.id === id);
    if (live && !(again && live.state === 'failed')) return;
    const [download, item] = await Promise.all([readDownload(api, title.downloadId), readDownloadedTitle(api, title.downloadId, title.itemId)]);
    if (!current()) return;
    // A title in several files is not fetched yet: the player plays one file.
    if (item.files.length !== 1) throw new Error('several_files');
    if (this.admission(item.files[0]!.sizeBytes)) throw new Error('no_room');
    const file = item.files[0]!, info = titleInfo(item.item, download), lines = downloadTitleLines(info);
    const meta: Meta = {owner, downloadId: title.downloadId, single: download.target.id === title.itemId, title: info, kind: info.kind,
      libraryId: item.item.libraryId, sha256: file.sha256 ?? '', extras: []};
    const raw = await native!.start({id, url: file.url, fileName: `${title.itemId}.${file.container || 'media'}`, bytes: file.sizeBytes,
      sha256: file.sha256 ?? '', contentType: '', itemId: title.itemId, title: lines.title, subtitle: lines.subtitle, quality: item.quality, meta: JSON.stringify(meta)});
    if (!raw) throw new Error('transfer_failed');
    this.accept(raw);
    await native!.writeExtra(id, 'bundle.json', JSON.stringify(bundleOf(item)));
    const files: [string, string][] = [
      ...(['poster', 'backdrop'] as const).flatMap(role => item.item.images[role] ? [[role, item.item.images[role]!] as [string, string]] : []),
      ...sideSubtitles(item).map(subtitle => [subtitleExtraName(subtitle.id, 0, subtitle.format), subtitle.url!] as [string, string]),
    ];
    const saved: string[] = [];
    for (const [name, url] of files) if (await native!.fetchExtra(id, name, url).catch(() => 'later') === 'saved') saved.push(name);
    meta.extras = saved;
    this.metas.set(id, meta);
    await native!.setMeta(id, JSON.stringify(meta));
  }
  /** Deletes a title's file and what came with it, here only. */
  private async forget(entry: OfflineEntry) {
    await native?.remove(entry.id);
    if (entry.owner) delete this.stored.resume[entry.owner]?.[entry.itemId];
    await this.persist();
  }

  /** Asks for a download on this device, then brings the device in step so it starts at once. */
  async want(ask: DownloadAsk): Promise<Download> {
    const api = this.api;
    if (!api) throw new Error(serviceText('downloads.connectToStart'));
    const download = await askForDownload(api, ask);
    this.wake();
    return download;
  }
  /** Stops a request the server is still preparing for this device. */
  async cancelPending(id: string) {
    const api = this.api;
    if (!api) throw new Error(serviceText('downloads.connectToContinue'));
    await removeDownload(api, id);
    this.wake();
  }
  /** The person removed a download, and the server is told, now or at the next contact, so the title is not fetched
   * again: a request of that one title goes; one title of a show, season, album or playlist leaves its request. */
  async remove(entry: OfflineEntry, withdrawn = false) {
    const meta = this.metas.get(entry.id);
    await this.forget(entry);
    if (withdrawn || !meta?.downloadId || !entry.owner) return;
    const removal = meta.single ? meta.downloadId : meta.downloadId + '/' + entry.itemId;
    const api = entry.owner === this.ownerKey() ? this.api : undefined;
    const later = () => { (this.stored.removals[entry.owner!] ??= []).push(removal); return this.persist(); };
    if (!api) { await later(); return; }
    await this.tellRemoved(api, removal).catch(later);
  }
  /** Tells the server of one removal. Already gone is done; a server without the route for one title (405)
   * keeps today's behaviour, where only this device lets it go; the outbox keeping it is done here. */
  private async tellRemoved(api: Api, removal: string) {
    const [downloadId, itemId] = removal.split('/');
    try {
      if (itemId) await removeDownloadedTitle(api, downloadId!, itemId);
      else await removeDownload(api, downloadId!);
    } catch (error) {
      const status = (error as {status?: number}).status;
      if (status !== 404 && status !== 405 && (error as {code?: string}).code !== 'sync_pending') throw error;
    }
  }
  /** Removes every download on this device that is not the signed-in viewer's, exactly as the confirm drew them. */
  async removeOthersConfirmed(expected: OfflineOthersTarget) {
    if (expected.revision !== this.state.othersRevision || expected.bytes !== this.state.otherBytes) throw Object.assign(new Error('conflict'), {code: 'conflict'});
    await this.removeOthers();
  }
  async removeOthers() {
    const owner = this.ownerKey();
    if (!owner) return;
    for (const entry of this.all.filter(value => value.owner !== owner)) await this.forget(entry);
  }
  /** The person paused it: it stays paused until they continue it. */
  pause(entry: OfflineEntry) { return native?.pause(entry.id) ?? Promise.resolve(); }
  async resume(entry: OfflineEntry) {
    const owner = this.ownerKey(), api = this.api, generation = this.generation;
    if (!owner || !api || entry.owner !== owner || !entry.copy) throw new Error(serviceText('downloads.connectToContinue'));
    await this.fetchTitle({downloadId: entry.copy.downloadId, itemId: entry.itemId}, owner, api, () => generation === this.generation, true);
  }
  async setSettings(next: Partial<OfflineSettings>) {
    this.stored.settings = Object.freeze({...this.stored.settings, ...next});
    this.publish({settings: this.stored.settings});
    if (next.wifiOnly !== undefined) await native?.setCellular(!next.wifiOnly).catch(() => {});
    await this.persist();
  }

  /** Whether this device has room for a file of this size, under the person's own limit. */
  admission(size: number): string {
    if (!this.state.ready || !this.storageMeasured || !Number.isSafeInteger(size) || size < 0) return serviceText('downloads.checking');
    const moving = this.all.filter(entry => entry.state === 'downloading');
    const whole = moving.reduce((sum, entry) => sum + entry.bytes, 0), remaining = moving.reduce((sum, entry) => sum + Math.max(0, entry.bytes - entry.bytesDone), 0);
    if (this.state.settings.limitBytes > 0 && this.state.usedBytes + whole + size > this.state.settings.limitBytes) return serviceText('downloads.blocked.limit');
    if (size + remaining + 500 * 1048576 > this.state.freeBytes) return serviceText('downloads.blocked.space');
    return '';
  }
  playabilityReason(entry: OfflineEntry, viewer: Viewer | undefined = this.viewer): OfflinePlayabilityReason | null {
    if (!viewer) return 'sign_in';
    const owner = offlineOwnerKey(viewer);
    if (entry.owner !== owner) return 'other_profile';
    if (entry.state !== 'ready') return 'not_finished';
    if (!entry.copy) return 'needs_setup';
    const judged = mayPlayOffline(this.stored.permissions[owner], this.stored.clock, Date.now());
    if (judged.clock !== this.stored.clock) { this.stored.clock = judged.clock; void this.persist(); }
    return judged.playable ? null : 'needs_check_in';
  }
  playable(entry: OfflineEntry, viewer: Viewer | undefined = this.viewer): string {
    const reason = this.playabilityReason(entry, viewer);
    const messages = {sign_in: 'downloads.offline.signIn', not_finished: 'downloads.offline.notFinished', needs_setup: 'downloads.offline.needsSetup',
      needs_check_in: 'downloads.offline.needsCheckIn', other_profile: 'downloads.offline.otherProfile'} as const;
    return reason ? serviceText(messages[reason] as MessageId) : '';
  }
  note(_entry: OfflineEntry): string { return ''; }

  private async bundle(entry: OfflineEntry): Promise<OfflineBundle | undefined> {
    try { const raw = await native?.readExtra(entry.id, 'bundle.json'); return raw ? JSON.parse(raw) as OfflineBundle : undefined; } catch { return undefined; }
  }
  async displayFacts(entry: OfflineEntry): Promise<OfflineEntryFacts> {
    const bundle = await this.bundle(entry), dir = this.dirs.get(entry.id) ?? '', saved = this.metas.get(entry.id)?.extras ?? [];
    const resume = entry.owner ? this.stored.resume[entry.owner]?.[entry.itemId] : undefined;
    return Object.freeze({...(resume ? {resumePositionSeconds: resume} : {}), ...(dir && saved.includes('backdrop') ? {backdropUrl: dir + 'extras/backdrop'} : {}),
      ...(bundle?.media.container ? {media: {container: bundle.media.container}} : {})});
  }
  /** What came with a download: its details, chapters and markers, and its subtitles as files here. */
  async extras(entry: OfflineEntry): Promise<OfflineExtras> {
    const bundle = await this.bundle(entry), dir = this.dirs.get(entry.id) ?? '', saved = new Set(this.metas.get(entry.id)?.extras ?? []);
    const subtitles = (bundle?.subtitles ?? []).flatMap(track => { const stored = dir ? storedSubtitle(track, saved, dir + 'extras/') : undefined; return stored ? [stored] : []; });
    return Object.freeze({...(bundle ? {bundle} : {}), subtitles: Object.freeze(subtitles), backdropUrl: saved.has('backdrop') && dir ? dir + 'extras/backdrop' : ''});
  }
  async localMedia(entry: OfflineEntry): Promise<LocalMedia> {
    const reason = this.playable(entry); if (reason) throw new Error(reason);
    if (!native?.localFile) throw new Error(serviceText('downloads.offline.notPlayableHere'));
    const file = await native.localFile(entry.id), extras = await this.extras(entry);
    const latest = this.playable(entry); if (latest) throw new Error(latest);
    const startSeconds = Math.max(0, (entry.owner ? this.stored.resume[entry.owner]?.[entry.itemId] : 0) ?? 0);
    const playId = 'play_' + random();
    this.playing.set(entry.id, {playId, startMs: Math.round(startSeconds * 1000)});
    return {entryId: entry.id, itemId: entry.itemId, playId, fileUrl: file.url, durationSeconds: Math.max(0, Number(file.durationSeconds) || 0), startSeconds, extras};
  }
  /** Progress from Portico's player for a downloaded title: a playing position at most every 10 seconds; pauses, stops and the end always. */
  recordProgress(progress: LocalProgress): void {
    const now = Date.now(), last = this.lastLocalWrite.get(progress.entryId) ?? 0;
    if (progress.state === 'playing' && now - last < 10_000) return;
    this.lastLocalWrite.set(progress.entryId, now);
    void this.observed(progress, progress.state !== 'playing');
  }
  private async observed(progress: LocalProgress, final: boolean) {
    const entry = this.all.find(value => value.id === progress.entryId);
    if (!entry?.owner) return;
    const owner = entry.owner, position = Math.max(0, progress.positionSeconds);
    const completion = offlineCompletion(entry.kind, position, progress.durationSeconds, progress.ended);
    bucket(this.stored.resume, owner)[entry.itemId] = completion.resumeSeconds;
    const started = this.playing.get(entry.id), playId = progress.playId || started?.playId || 'play_' + random();
    const plays = bucket(this.stored.plays, owner), startMs = plays[playId]?.startMs ?? started?.startMs ?? 0, positionMs = Math.round(position * 1000);
    plays[playId] = {itemId: entry.itemId, positionMs, playedMs: Math.max(plays[playId]?.playedMs ?? 0, positionMs - startMs), startMs, at: Date.now()};
    const ids = Object.keys(plays);
    if (ids.length > MAX_PLAYS) {
      const latest = new Map<string, string>();
      for (const id of ids) { const kept = latest.get(plays[id]!.itemId); if (!kept || plays[id]!.at >= plays[kept]!.at) latest.set(plays[id]!.itemId, id); }
      for (const id of ids) if (latest.get(plays[id]!.itemId) !== id) delete plays[id];
    }
    await this.persist();
    this.publishEntries();
    if (!final) return;
    if (this.api && owner === this.ownerKey()) void this.sendPlays(owner, this.api).catch(() => {});
    if (!completion.finished) return;
    this.playing.delete(entry.id);
    if (this.stored.settings.deleteWatched) void this.remove(entry).catch(() => {});
  }
  /** Sends this viewer's kept plays through the state batch. Each is counted once by its play id;
   * one kept by the outbox is the outbox's to send. */
  private async sendPlays(owner: string, api: Api) {
    const plays = this.stored.plays[owner] ?? {}, ids = Object.keys(plays).slice(0, 200);
    if (!ids.length) return;
    const now = Date.now(), sending = ids.map((playId): OfflinePlay => {
      const play = plays[playId]!;
      return {itemId: play.itemId, playId, positionMs: play.positionMs, playedMs: play.playedMs, madeAgoMs: Math.max(0, now - play.at)};
    });
    try { await reportDownloadPlays(api, sending); }
    catch (error) { if ((error as {code?: string}).code !== 'sync_pending') throw error; }
    for (const id of ids) delete plays[id];
    await this.persist();
    this.publishEntries();
  }

  // Reading downloads and selection-action downloads were built on the old server's receipts and
  // bulk operations. Neither exists on the new server yet: these say so instead of acting.
  async followSelectionAction(_operationId: string, _deviceId: string): Promise<void> {}
  async wantPublication(..._args: unknown[]): Promise<void> { throw new Error('reading_unavailable'); }
  publicationSource(_entry: OfflineEntry): Readonly<{sourceId: string; revision: string}> { throw new Error('reading_unavailable'); }
  async readingFacts(_entry: OfflineEntry): Promise<OfflineReadingFacts> { throw new Error('reading_unavailable'); }
  publicationLease(_entry: OfflineEntry, _scope: Readonly<{signal: AbortSignal; current: () => boolean}>): NativeOfflinePublicationLease { throw new Error('reading_unavailable'); }
  async readingSession(_entry: OfflineEntry, _scope: Readonly<{signal: AbortSignal; current: () => boolean; paused: () => boolean}>): Promise<OfflineReadingSession> { throw new Error('reading_unavailable'); }
}

export const offlineDownloads = new OfflineDownloads();

/** Downloads gathered for the list: the episodes of one show, the songs of one album, together. */
export type OfflineGroup = Readonly<{key: string; title: string; artworkUrl: string; entries: readonly OfflineEntry[]}>;
export function groupOfflineEntries(entries: readonly OfflineEntry[]): readonly OfflineGroup[] {
  const groups = new Map<string, {title: string; entries: OfflineEntry[]}>();
  for (const entry of entries) {
    const info = entry.info;
    const grouped = !!info?.parentId && (info.kind === 'episode' || info.kind === 'song' || info.kind === 'audiobook_file');
    const key = grouped ? 'parent:' + info!.parentId : 'title:' + entry.id;
    const group = groups.get(key) ?? {title: grouped ? info!.parentTitle : '', entries: []};
    group.entries.push(entry);
    groups.set(key, group);
  }
  const place = (e: OfflineEntry) => (e.info?.groupNumber ?? 0) * 100000 + (e.info?.number ?? 0);
  return Object.freeze([...groups.entries()].map(([key, group]) => {
    const ordered = group.title ? [...group.entries].sort((a, b) => place(a) - place(b) || a.addedAt - b.addedAt) : group.entries;
    return Object.freeze({key, title: group.title, artworkUrl: ordered.find(e => e.artworkUrl)?.artworkUrl ?? '', entries: Object.freeze(ordered)});
  }).sort((a, b) => Math.max(...b.entries.map(e => e.addedAt)) - Math.max(...a.entries.map(e => e.addedAt))));
}
