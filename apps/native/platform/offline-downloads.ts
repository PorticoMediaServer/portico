import {downloadCopyReference, downloadPreparation, downloadedBundle} from '../../../packages/client-core/src/downloads-service';
import {downloadedSubtitleText, keepDownloadsInStep, readDownload, removeDownloadedTitle, reportDownloadPlay} from '../../../packages/client-core/src/device-downloads';
import {serverNow} from '../../../packages/client-core/src/server-clock';
import {openDownloadedBook, nativePublicationReadsAvailable, type OfflineReadingSession} from './offline-reading';
import {readingAnnotationBody, readingAnnotationEntry, readingPlacePosition, readingPlaceProgress, type ReadingLocation, type ReadingManifest, type ReadingProgress} from '../../../packages/client-core/src/reading';
import {callServer} from '../../../packages/client-core/src/query/server-api';
import {maxEditAgeMs} from '../../../packages/client-core/src/outbox-routes';
import type {AnnotationChange} from '../../../packages/contracts/src/next/api';
import {storedSubtitle, subtitleExtraFiles, subtitleExtraName, joinDownloadedVtt, type StoredSubtitle} from './offline-subtitle-files';
import {receiptTime, serverReceiptFloor} from './receipt-clock';
import {NativeEventEmitter, NativeModules, Platform} from 'react-native';

import {
  DownloadsService, downloadTitleLines, downloadReasonMessage, getDownloadRequest, parseDownloadTitle,
  type DownloadBundle, type DownloadPreparation, type DownloadReceipt, type DownloadTitle,
} from '../../../packages/client-core/src/index';
import {serviceText} from '../../../packages/client-core/src/presentation/service-text';
import {durationIntent} from '../../../packages/client-core/src/wire-units';
import type {MessageId} from '../../../packages/i18n/src/index';
import type {LocalMedia, LocalProgress} from './local-playback';
import {offlineCompletion} from './offline-completion';

/** Downloads kept on this phone or tablet.
 *
 * The native module transfers and verifies files. Playback requires a current
 * offline permission for this download's viewer.
 *
 * A copy on this device is the viewer's until they remove it or the server's owner takes their
 * access away. The server tidying up — its own copy running out its time, the file being
 * replaced, the title leaving the library — never removes it; a title that has left the server
 * is shown as such and still plays.
 *
 * Everything a download needs comes with it and is kept beside it: what it is and where it sits
 * in its show, album or book, its description, artwork, chapters, skip markers, audio tracks and
 * subtitles. Nothing on the Downloads list asks the server for anything.
 *
 * A transfer continues from where it stopped, by itself: after a dropped connection, a wait for
 * Wi-Fi, the app being closed, or the address it was fetched from running out.
 *
 * Each play made offline is remembered and sent to the server later, which records it like any
 * other play and keeps whatever is newer. */

export type OfflineState = 'downloading' | 'paused' | 'failed' | 'ready';
/** Why a download that should be moving is not. */
export type OfflineWaiting = '' | 'wifi' | 'network';
/** The most pages of a container request's members (100 each) read in one pass. */
const ENROLL_PAGES_PER_PASS = 50;
/** The most members left waiting to be collected before more of a container request is read. They
 * are kept in the bookkeeping document, which the device refuses to save past a megabyte. */
const ENROLL_BACKLOG = 5000;
/** Above this history budget, retain the latest unsent play of every title. */
const MAX_PLAYS = 400;
/** The least time between two automatic attempts at the same stopped download. */
const RETRY_EVERY_MS = 30 * 1000;
const ENTRY_STATES = ['downloading', 'paused', 'failed', 'ready'];
/** Stops that mend themselves: the app tries again without being asked. */
const RETRYABLE = new Set(['network', 'grant_expired', 'transfer_failed', 'server_refused']);
export type OfflineEntry = Readonly<{
  id: string; itemId: string; title: string; subtitle: string; quality: string;
  /** The poster or cover kept with the download (a file on this device), or ''. */
  artworkUrl: string;
  state: OfflineState; error: string; waiting: OfflineWaiting;
  /** The person paused it. Anything else that is paused or stopped did so by itself, and is
   * started again without being asked. */
  held: boolean;
  bytes: number; bytesDone: number; addedAt: number;
  artifact?: Readonly<{sha256: string; bytes: number}>; downloadId: string; libraryId?: string; kind?: string;
  /** Older consumer types still accept this field. The store only publishes rebuilt copies. */
  receipt?: DownloadReceipt;
  /** Owner recorded natively when the transfer started. */
  owner?: string;
  /** What the download is of, as the server last described it. */
  info?: DownloadTitle;
  /** False once the server has said the title is no longer in its library. */
  onServer: boolean;
  /** The server now holds a different file for this title. This copy still plays. */
  changedOnServer: boolean;
}>;
export type OfflineSettings = Readonly<{wifiOnly: boolean; deleteWatched: boolean; limitBytes: number}>;
export type OfflinePlayabilityReason = 'sign_in'|'not_finished'|'needs_setup'|'needs_check_in'|'other_profile';
export type OfflineEntryFacts = Readonly<{
  /** The viewer's item bookmark, not a per-artifact observation or a completion flag. */
  resumePositionSeconds?: number;
  backdropUrl?: string;
  /** Actual cached bundle facts, never a resolution inferred from a quality ID. */
  media?: Readonly<{container?:string;videoCodec?:string;audioCodec?:string;width?:number;height?:number}>;
}>;
/** Display facts of a downloaded publication and this viewer's saved place. */
export type OfflineReadingFacts = Readonly<{
  format: DownloadedBook['format'];
  pageCount?: number;
  location?: ReadingLocation;
}>;
export type OfflinePendingDownload = Readonly<{id: string; itemId: string; title: string; subtitle?: string; reason: string}>;
export type OfflineSnapshot = Readonly<{supported: boolean; ready: boolean; entries: readonly OfflineEntry[]; pendingDownloads?: readonly OfflinePendingDownload[]; settings: OfflineSettings; freeBytes: number; usedBytes: number; /** Actual OS total capacity of the download volume; omitted when the native module does not report it. */ totalBytes?: number; /** Logical regular-file bytes in the entire app-owned Downloads tree (all viewers, including partial files/extras). Symlinks contribute no logical target bytes. */ storedBytes?: number; /** Actual OS allocated blocks in that tree, including directories and symlinks' own reported allocation, counted once per inode. Link targets are never traversed. */ allocatedBytes?: number; pendingProgress: number; /** Space taken by other viewers' downloads on this device. */ otherBytes: number; /** Opaque revision of that other-viewer set; it changes whenever the set or its stored size changes. */ othersRevision?: number; error?: string}>;
/** The exact other-viewer set a confirm drew: its opaque revision and shown size. */
export type OfflineOthersTarget = Readonly<{revision: number; bytes: number}>;
/** One other-viewer member's published provenance, used for the confirm signature. */
type OtherMember = Readonly<{id: string; owner: string; artifact: string; generation: string; bytes: number}>;
export type Viewer = Readonly<{authority: string; accountId: string; profileId: string; serverId: string}>;
type Api = {baseUrl: string; mediaUrl(path: string): string; request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
type Play = {playId: string; itemId: string; downloadId: string; positionSeconds: number; observedAt: string; ended?: boolean; playedMs?: number};
export type OfflineDisplay = Readonly<{title: string; subtitle?: string; artworkUrl?: string; libraryId?: string; kind?: string}>;
type OfflineWant = OfflineDisplay & Readonly<{itemId: string}>;
type StartedTransfer = Readonly<{id: string; owner?: string; generation?: string}>;
/** What the player is given beside the file: everything that came with the download. Subtitle
 * files are on this device. */
export type OfflineExtras = Readonly<{
  bundle?: DownloadBundle;
  subtitles: readonly StoredSubtitle[];
  backdropUrl: string;
  /** The scrubbing preview sheets as files on this device, in order; empty unless every one is here. */
  trickplayTiles?: readonly string[];
  /** Chapter thumbnails as files on this device, by the chapter's index. */
  chapterImages?: Readonly<Record<number, string>>;
}>;

/** Raised when what is kept beside a download changes (3: a VobSub index is kept as `.idx`; 4:
 * chapter thumbnails and scrubbing previews come too), so a download marked complete under an
 * earlier rule is completed again by the next pass. */
const BUNDLE_COMPLETE = 4;
/** The names a download's previews are kept under beside it. */
const trickplayTileName = (tile: number) => `trickplay-${tile}`;
const chapterImageName = (index: number) => `chapter-${index}`;
type Meta = {
  downloadId?: string; libraryId?: string; kind?: string; owner?: string; title?: DownloadTitle;
  onServer?: boolean; changed?: boolean; extras?: string[]; bundle?: number; publication?: boolean;
  /** A book from the rebuilt server: the reader its file opens in (from the download's container),
   * the version, and the server's fingerprint of it, which places are made on. */
  book?: DownloadedBook;
  /** The server has been told this device holds the whole file, so it can delete the copy it made for it. */
  received?: true;
};
type DownloadedBook = Readonly<{format: 'epub' | 'pdf' | 'cbz'; versionId: string; revision: string}>;
/** The containers a downloaded book can be read from on this device: the server hands every book
 * over as one of these (a MOBI as EPUB, a CBR, CB7 or folder of pictures as CBZ). */
const offlineBookFormat = (container: string | undefined): DownloadedBook['format'] | undefined =>
  container === 'epub' || container === 'pdf' || container === 'cbz' ? container : undefined;
/** A place in a downloaded book this profile made with no server, by item: the state's position,
 * when it was made, and whether it has been handed to the server (or its outbox) yet. */
type BookPlace = Readonly<{position: string; madeAt: number; sent?: true}>;
/** A downloaded book's bookmarks, highlights and notes, by item: the server's rows as last read, and
 * the changes made here not yet taken by the server (an added or changed one's body, or a deletion),
 * each with when it was made. */
type BookNoteChange = Readonly<{body?: Readonly<Record<string, unknown>>; madeAt: number}>;
type BookNotes = Readonly<{rows: readonly Readonly<Record<string, unknown>>[]; pending: Readonly<Record<string, BookNoteChange>>}>;

/** Deferred work is filed under the viewer that produced it, never under whoever is signed in
 * when it is finally sent. */
type Owned<T> = Record<string, Record<string, T>>;
type Stored = {
  version: 3;
  settings: OfflineSettings;
  /** Plays made offline and not yet accepted by the server, by play id. */
  plays: Owned<Play>;
  resume: Owned<number>;
  /** Accepted preparations, keyed by preparation id, never by title alone. */
  wanted: Owned<OfflineWant>;
  /** Actual explicitly confirmed command follow; never a query or member list. */
  wantedPublications: Owned<OfflineDisplay>;
  /** Members already accepted from each request, including copies later removed. */
  enrolledMembers: Owned<Record<string, true>>;
  /** The floor keeps a device clock change from extending offline permission. */
  clockFloor: number;
  permissions: Record<string, number>;
  removed: Owned<{downloadId: string; itemId: string}>;
  places: Owned<BookPlace>;
  notes: Owned<BookNotes>;
};

const native = NativeModules.PorticoDownloads as undefined | {
  list(): Promise<any[]>; start(request: Record<string, unknown>): Promise<any>; pause(id: string): Promise<void>; remove(id: string): Promise<void>;
  /** Continues only the exact native entry captured by the caller. */
  resume?(request: Record<string, unknown>): Promise<any>;
  /** Present from the C9 conditional-removal build: removes only while that entry's published
   * native generation is still current at the owner's accepting turn. */
  removeGeneration?(id: string, generation: string): Promise<void>;
  setMeta(id: string, meta: string): Promise<void>;
  storage(): Promise<{freeBytes: number; usedBytes: number; totalBytes?: number; storedBytes?: number; allocatedBytes?: number}>; setCellular(allowed: boolean): Promise<void>;
  readState(): Promise<string>; writeState(json: string): Promise<void>;
  localFile(id: string): Promise<{url: string; durationSeconds: number; parts?: LocalMedia['parts']}>;
  writeExtra(id: string, name: string, text: string): Promise<void>; readExtra(id: string, name: string): Promise<string>;
  /** 'saved', 'missing' (the server has none for this title), 'later' or 'invalid'. */
  fetchExtra(id: string, name: string, url: string): Promise<string>;
};
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const defaults: OfflineSettings = Object.freeze({wifiOnly: true, deleteWatched: false, limitBytes: 0});
const random = () => Math.random().toString(36).slice(2) + Date.now().toString(36);

/** One viewer on one server. Every local record and every native entry is filed
 * under this exact string, so "the same account on a different server" and "a
 * different profile on the same account" are different owners. */
export const offlineOwnerKey = (viewer: Viewer) =>
  JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId, viewer.serverId]);

const safeInteger = (value: unknown, minimum = 0): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= minimum;
/** Marks a document read from an earlier layout, so open() rewrites it once. */
const upgradedLayout = Symbol('upgradedLayout');
const emptyStored = (): Stored => ({version: 3, settings: defaults, plays: {}, resume: {}, wanted: {}, wantedPublications: {}, enrolledMembers: {}, clockFloor: 0, permissions: {}, removed: {}, places: {}, notes: {}});

/** A present document must be readable in full. An unsupported or damaged record
 * is never replaced with an empty outbox on the next settings/progress write. */
function readStored(parsed: any): Stored {
  const out = emptyStored();
  const invalid = () => {throw new Error('downloads_state_unreadable');};
  if (!object(parsed)) invalid();
  const record = (value: unknown) => {if (!object(value)) invalid(); return value as Record<string, any>;};
  // A readable document from an earlier layout keeps its settings. The rest
  // was shaped for another contract.
  if (Number.isSafeInteger(parsed.version) && parsed.version >= 1 && parsed.version < 3) {
    const settings = object(parsed.settings) ? parsed.settings : {};
    out.settings = {...defaults, ...Object.fromEntries(['wifiOnly', 'deleteWatched'].filter(key => typeof settings[key] === 'boolean').map(key => [key, settings[key]])),
      ...(safeInteger(settings.limitBytes) ? {limitBytes: settings.limitBytes} : {})};
    return Object.assign(out, {[upgradedLayout]: true});
  }
  if (parsed.version !== 3) invalid();
  const owned = (value: unknown) => {const values = record(value); for (const entries of Object.values(values)) record(entries); return values;};
  if (parsed.settings !== undefined) {
    const settings = record(parsed.settings);
    for (const key of ['wifiOnly', 'deleteWatched']) if (settings[key] !== undefined && typeof settings[key] !== 'boolean') invalid();
    if (settings.limitBytes !== undefined && !safeInteger(settings.limitBytes)) invalid();
  }
  const settings = record(parsed.settings ?? {});
  out.settings = {wifiOnly: settings.wifiOnly ?? defaults.wifiOnly, deleteWatched: settings.deleteWatched ?? defaults.deleteWatched,
    limitBytes: settings.limitBytes ?? defaults.limitBytes};
  // The device-wide next-episode wish named no show or count. Keep the server's
  // per-show Keep requests and all local files; retire only the obsolete wish.
  if ('autoNext' in settings || 'nextAfter' in parsed) Object.assign(out, {[upgradedLayout]: true});
  out.plays = owned(parsed.plays ?? {});
  for (const values of Object.values(out.plays)) for (const [id, play] of Object.entries(values)) {
    if (object(play) && play.downloadId === undefined) {delete values[id]; continue;}
    if (!object(play) || play.playId !== id || typeof play.downloadId !== 'string' || !play.downloadId || typeof play.itemId !== 'string' || typeof play.positionSeconds !== 'number' || !Number.isFinite(play.positionSeconds) || play.positionSeconds < 0 || typeof play.observedAt !== 'string' || !Number.isFinite(Date.parse(play.observedAt)) || play.ended !== undefined && typeof play.ended !== 'boolean') invalid();
  }
  out.resume = owned(parsed.resume ?? {});
  for (const values of Object.values(out.resume)) if (Object.values(values).some(value => typeof value !== 'number' || !Number.isFinite(value) || value < 0)) invalid();
  out.wanted = owned(parsed.wanted ?? {});
  for (const values of Object.values(out.wanted)) for (const [id, value] of Object.entries(values)) {
    // An intent from the earlier title-keyed layout names no accepted copy. Drop
    // it; its server request is still followed and re-enrols the exact copy.
    if (object(value) && typeof value.title === 'string' && value.itemId === undefined) {delete values[id]; continue;}
    if (!object(value) || typeof value.title !== 'string' || typeof value.itemId !== 'string' || !value.itemId) invalid();
  }
  // Ignore pre-launch whole-view download markers when reading old state.
  out.wantedPublications = owned(parsed.wantedPublications ?? {});
  for (const values of Object.values(out.wantedPublications)) for (const value of Object.values(values)) if (!object(value) || typeof value.title !== 'string' || value.kind !== 'ebook' && value.kind !== 'comic') invalid();
  out.enrolledMembers = owned(parsed.enrolledMembers ?? {});
  for (const requests of Object.values(out.enrolledMembers)) for (const members of Object.values(requests)) if (Object.values(record(members)).some(value => value !== true)) invalid();
  if (parsed.clockFloor !== undefined && !safeInteger(parsed.clockFloor)) invalid();
  out.removed = owned(parsed.removed ?? {});
  for (const values of Object.values(out.removed)) for (const value of Object.values(values)) if (!object(value) || typeof value.downloadId !== 'string' || typeof value.itemId !== 'string') invalid();
  out.places = owned(parsed.places ?? {});
  for (const values of Object.values(out.places)) for (const value of Object.values(values)) if (!object(value) || typeof value.position !== 'string' || !safeInteger(value.madeAt, 1) || value.sent !== undefined && value.sent !== true) invalid();
  out.notes = owned(parsed.notes ?? {});
  for (const values of Object.values(out.notes)) for (const value of Object.values(values)) {
    if (!object(value) || !Array.isArray(value.rows) || value.rows.some(row => !object(row)) || !object(value.pending)) invalid();
    for (const change of Object.values(value.pending)) if (!object(change) || !safeInteger(change.madeAt, 1) || change.body !== undefined && !object(change.body)) invalid();
  }
  out.permissions = record(parsed.permissions ?? {});
  if (Object.values(out.permissions).some(value => !safeInteger(value, 1))) invalid();
  out.clockFloor = typeof parsed.clockFloor === 'number' ? parsed.clockFloor : 0;
  return out;
}

const bucket = <T,>(owned: Owned<T>, owner: string): Record<string, T> => (owned[owner] ??= {});
export const offlineFailure = (code: string) => (code ? serviceText((['grant_expired', 'server_refused', 'verification_failed', 'storage_full', 'network', 'transfer_failed'].includes(code) ? 'downloads.failure.' + code : 'downloads.failure.other') as MessageId) : '');
export const offlineWaiting = (waiting: OfflineWaiting) => (waiting ? serviceText(('downloads.waiting.' + waiting) as MessageId) : '');

export class OfflineDownloads {
  /** Identity from the native outbound clone, excluded from enumerable app/guest/persisted data. */
  private readonly publicationStateGenerations = new WeakMap<OfflineEntry, string>();
  private state: OfflineSnapshot = Object.freeze({supported: !!native && !Platform.isTV, ready: false, entries: [], settings: defaults, freeBytes: 0, usedBytes: 0, pendingProgress: 0, otherBytes: 0, othersRevision: 0});
  private listeners = new Set<() => void>();
  /** Everything on the device. `state.entries` is the subset the connected
   * viewer may see; this is the list ownership decisions are made against. */
  private all: OfflineEntry[] = [];
  /** What is kept with each entry, as last read from the device. */
  private metas = new Map<string, Meta>();
  /** Each entry's folder on this device, as the native module reported it this launch. */
  private dirs = new Map<string, string>();
  private stored: Stored = emptyStored();
  private stateReadable = false;
  private storageMeasured = false;
  private writes: Promise<boolean> = Promise.resolve(true);
  /** Invalidated by pause/removal even while a grant is in flight. */
  private incarnations = new Map<string, object>();
  private entriesScheduled = false;
  private disposed = false;
  private subscriptions: {remove(): void}[] = [];
  private api?: Api; private service?: DownloadsService; private viewer?: Viewer;
  private unfollow?: () => void;
  private collecting = new Set<string>();
  private collection?: {generation: number; again: boolean; task: Promise<void>};
  private starts = new Map<string, Promise<StartedTransfer | undefined>>();
  private startingRequests = new Map<string, {meta: string; itemId: string; current: () => boolean}>();
  /** Per container request, the reading of its members past the first page: the member the next
   * page starts after, how many have been read, the counts the request had at the last look, and
   * whether the last page was reached (it is read again, from the same place, when they change). */
  private enrolled = new Map<string, {after: string; read: number; mark: string; tail: boolean; busy: boolean; retryAt: number; unprepared?: boolean}>();
  private enrolledPreparations = new Map<string, DownloadPreparation>();
  private requestChecks = new Map<string, number>();
  private preparationChecks = new Map<string, number>();
  private enrollTimer?: ReturnType<typeof setTimeout>;
  /** Bumped by every connect and disconnect. Work started under an earlier
   * generation may finish, but may not write anything back. */
  private generation = 0;
  private opening?: Promise<void>;
  private progressSync?: {generation: number; task: Promise<void>};
  /** When each stopped download was last tried again by itself. */
  private retried = new Map<string, number>();
  private tending?: ReturnType<typeof setInterval>;
  private bundling = new Set<string>();
  private sendingNotes = false;
  /** The play under way for each entry (one id for as long as the file stays open). */
  private playing = new Map<string, string>();
  private observationTimes = new Map<string, number>();
  /** The other viewers' set, as the last published snapshot weighed it: its opaque
   * revision changes whenever any member or its stored bytes change. */
  private othersRevision = 0;
  private othersSignature = '';
  private downloadSync?: {generation: number; task: Promise<void>};
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  private publish(patch: Partial<OfflineSnapshot>) { if (this.disposed) return; this.state = Object.freeze({...this.state, ...patch}); this.listeners.forEach(f => f()); }

  private entryOwner(entry: OfflineEntry): string | undefined {
    return entry.owner;
  }
  private ownerKey() { return this.viewer ? offlineOwnerKey(this.viewer) : undefined; }
  /** Entries belonging to one owner, the only entries that owner can play or sync. */
  private ownedEntries(owner: string) { return this.all.filter(e => this.entryOwner(e) === owner); }

  /** What the connected viewer may see: their own downloads. With no viewer connected the
   * device list is not published at all. */
  private visibleEntries(): OfflineEntry[] {
    const owner = this.ownerKey();
    return owner ? this.ownedEntries(owner) : [];
  }
  private publishEntries(next = this.all, deferred = false) {
    if (this.disposed) return;
    this.all = sort(next);
    // Native metadata writes can emit several events in one turn. Publish their
    // combined snapshot once, while keeping the internal list current.
    if (deferred) {
      if (!this.entriesScheduled) {
        this.entriesScheduled = true;
        queueMicrotask(() => { if (this.entriesScheduled) this.publishEntries(); });
      }
      return;
    }
    this.entriesScheduled = false;
    const owner = this.ownerKey();
    if (!owner) {this.othersSignature = ''; this.othersRevision = 0; this.publish({entries: [], pendingDownloads: [], otherBytes: 0, othersRevision: 0}); return;}
    // Other viewers' downloads are never named, only weighed: the space they take is this
    // device's, and whoever is using it can see it and give it back. The opaque revision
    // lets a confirm keep the exact set it drew without exposing any other viewer's ids.
    let otherBytes = 0; const signatures: string[] = [];
    for (const e of this.all) if (this.entryOwner(e) !== owner) {
      const member = this.otherMember(e);
      otherBytes += member.bytes; signatures.push(`${member.id}:${member.owner}:${member.artifact}:${member.generation}:${member.bytes}`);
    }
    const signature = signatures.sort().join('\n');
    if (signature !== this.othersSignature) {this.othersSignature = signature; this.othersRevision++;}
    this.publish({entries: this.visibleEntries(), otherBytes, othersRevision: this.othersRevision});
    this.publishPending();
  }

  /** Reads what is on the device. Works with no server and no sign-in. */
  open(): Promise<void> {
    if (this.disposed || !native || !this.state.supported || this.state.ready) return Promise.resolve();
    if (!this.opening) this.opening = this.readDevice().finally(() => { this.opening = undefined; });
    return this.opening;
  }
  private async readDevice(): Promise<void> {
    if (!native) return;
    try {
      const raw = await native.readState();
      this.stored = raw === '' ? emptyStored() : readStored(JSON.parse(raw));
      this.stateReadable = true;
      if (upgradedLayout in this.stored) {delete (this.stored as Stored & {[upgradedLayout]?: true})[upgradedLayout]; await this.persist();}
    } catch (error) {
      this.stateReadable = false;
      this.publish({error: serviceText('downloads.deviceUnreadable')});
      throw error;
    }
    if (this.disposed) return;
    const emitter = new NativeEventEmitter(NativeModules.PorticoDownloads);
    const pending = new Map<string, any>();
    let listing = true;
    const subscription = emitter.addListener('PorticoDownloadChanged', entry => {
      if (this.disposed) return;
      if (listing) { if (typeof entry?.id === 'string' && (entry.state === 'removed' || ENTRY_STATES.includes(entry.state))) pending.set(entry.id, entry); }
      else this.accept(entry);
    });
    this.subscriptions.push(subscription);
    try {
      const listed = await native.list();
      if (this.disposed) return;
      const latest = new Map<string, any>();
      for (const entry of listed) if (typeof entry?.id === 'string') latest.set(entry.id, entry);
      for (const [id, entry] of pending) {
        if (entry.state === 'removed') latest.delete(id);
        else latest.set(id, entry);
      }
      // Pre-rebuild copies have no server download ID. Remove them before reporting ready.
      const entries: OfflineEntry[] = [], obsolete: string[] = [];
      for (const raw of latest.values()) {
        const entry = this.read(raw);
        if (entry) entries.push(entry);
        else if (ENTRY_STATES.includes(raw.state)) obsolete.push(raw.id);
      }
      this.all = sort(entries);
      listing = false;
      await Promise.all(obsolete.map(id => native.remove(id)));
      if (this.disposed) return;
      this.publish({ready: true, settings: this.stored.settings, error: undefined});
      this.publishEntries();
    } catch (error) {
      subscription.remove();
      this.subscriptions = this.subscriptions.filter(value => value !== subscription);
      this.stateReadable = false;
      this.publish({error: serviceText('downloads.deviceUnreadable')});
      throw error;
    }
    await this.measure();
  }
  /** Names the viewer these downloads belong to, without binding a server.
   *
   * This is the half of `connect` that works with no network: it decides which
   * entries are listed and can be played. A cold offline launch restores the saved viewer through this, so the downloads
   * screen shows that viewer's files and only theirs. */
  adopt(viewer: Viewer | undefined) {
    if (this.disposed) return;
    const next = viewer && offlineOwnerKey(viewer);
    if (next === (this.viewer && offlineOwnerKey(this.viewer))) return;
    // An offline adoption/sign-out also invalidates online work immediately.
    this.disconnect();
    this.viewer = viewer;
    // Cold auth screens need the device list before their downloads route exists.
    if (viewer) void this.open().catch(() => this.publish({error: serviceText('downloads.deviceUnreadable')}));
    this.publishEntries();
    this.publish({pendingProgress: this.pendingCount()});
  }
  /** Binds the signed-in server to refresh offline permission and send offline viewing. */
  connect(api: Api, service: DownloadsService, viewer: Viewer) {
    if (this.disposed) return;
    this.adopt(viewer);
    const generation = ++this.generation;
    this.api = api; this.service = service;
    this.unfollow?.(); this.unfollow = service.subscribe(() => {this.publishPending(); void this.collect();});
    // While the server is bound, stopped transfers and missing extras are retried.
    clearInterval(this.tending); this.tending = setInterval(() => void this.tend(generation), 60 * 1000);
    void this.open().then(async () => {
      if (this.generation !== generation) return;
      await this.syncDownloads(generation);
      if (this.generation !== generation) return;
      void this.sendBookPlaces().then(() => this.sendBookNotes());
      this.publishEntries();
      this.publish({pendingProgress: this.pendingCount()});
      void this.syncProgress(generation);
      void this.collect();
      void this.tend(generation);
    }).catch(() => {if (this.generation === generation) this.publish({error: serviceText('downloads.deviceUnreadable')});});
  }
  /** The app came to the front, or the network changed: look again now rather than at the next tick. */
  wake() { if (this.api) void this.tend(this.generation); }
  /** Drops the server binding while preserving the saved viewer for authorized
   * offline access. Sign-out clears it explicitly through `adopt(undefined)`. */
  disconnect() { this.generation++; this.unfollow?.(); this.unfollow = undefined; this.api = undefined; this.service = undefined; clearInterval(this.tending); this.tending = undefined; this.publishPending(); }
  dispose() { this.disposed = true; this.entriesScheduled = false; this.disconnect(); clearTimeout(this.enrollTimer); this.enrollTimer = undefined; this.subscriptions.forEach(s => s.remove()); this.subscriptions = []; this.listeners.clear(); clearInterval(this.tending); }

  private read(raw: any): OfflineEntry | undefined {
    if (!raw || typeof raw.id !== 'string' || !ENTRY_STATES.includes(raw.state)) return undefined;
    let meta: Meta | undefined;
    try {meta = JSON.parse(raw.meta);} catch {}
    if (!meta || typeof meta.downloadId !== 'string' || !meta.downloadId) return undefined;
    let info: DownloadTitle | undefined;
    // Meta.title is the internal model serialized by transfer/patch, not an API response.
    try { info = meta.title ? parseDownloadTitle(durationIntent({...meta.title}, {durationSeconds: 'durationMs'})) : undefined; } catch { info = undefined; }
    const dir = typeof raw.dir === 'string' ? raw.dir : this.dirs.get(raw.id) ?? '';
    if (dir) this.dirs.set(raw.id, dir);
    this.metas.set(raw.id, {...meta, title: info});
    const lines = info ? downloadTitleLines(info) : undefined;
    const art = (meta.extras ?? []).find(name => name === 'poster' || name === 'cover');
    const entry: OfflineEntry = Object.freeze({
      id: raw.id, itemId: String(raw.itemId ?? ''), title: lines?.title || String(raw.title ?? ''), subtitle: lines?.subtitle ?? String(raw.subtitle ?? ''), quality: String(raw.quality ?? ''),
      artworkUrl: art && dir ? dir + 'extras/' + art : '',
      state: raw.state, error: String(raw.error ?? ''), waiting: raw.waiting === 'wifi' || raw.waiting === 'network' ? raw.waiting : '', held: raw.intent === 'hold',
      bytes: Number(raw.bytes) || 0, bytesDone: Number(raw.bytesDone) || 0, addedAt: Number(raw.addedAt) || 0,
      libraryId: meta.libraryId || undefined, kind: info?.kind || meta.kind || undefined,
      ...(typeof raw.sha256 === 'string' && /^[a-f0-9]{64}$/.test(raw.sha256) && safeInteger(raw.bytes, 1) ? {artifact: {sha256: raw.sha256, bytes: raw.bytes}} : {}),
      downloadId: meta.downloadId,
      owner: typeof meta.owner === 'string' && meta.owner ? meta.owner : undefined,
      info, onServer: meta.onServer !== false, changedOnServer: meta.changed === true,
    });
    if (typeof raw.publicationGeneration === 'string') this.publicationStateGenerations.set(entry, raw.publicationGeneration);
    return entry;
  }
  private accept(raw: any) {
    if (this.disposed) return;
    if (raw?.state === 'removed') { this.incarnations.delete(raw.id); this.metas.delete(raw.id); this.dirs.delete(raw.id); this.publishEntries(this.all.filter(e => e.id !== raw.id), true); void this.measure(); return; }
    const before = this.all.find(e => e.id === raw?.id);
    const next = this.read(raw); if (!next) return;
    const starting = this.startingRequests.get(next.id);
    const ownStart = starting?.current() && raw.meta === starting.meta && next.itemId === starting.itemId && !next.held;
    if (before && !ownStart && this.publicationStateGenerations.get(before) !== this.publicationStateGenerations.get(next)) this.incarnations.delete(next.id);
    this.publishEntries([...this.all.filter(e => e.id !== next.id), next], true);
    if (next.state === 'ready' && before?.state !== 'ready') {
      void this.measure();
      void this.sendReceived();
    }
    // A download that stopped by itself is started again without anybody having to notice.
    if (next.state !== before?.state && (next.state === 'failed' || next.state === 'paused')) void this.tend(this.generation);
  }
  private async measure() {
    if (!native) return;
    try {
      const s = await native.storage();
      const totalBytes = typeof s.totalBytes === 'number' && Number.isSafeInteger(s.totalBytes) && s.totalBytes > 0 ? s.totalBytes : undefined;
      const bytes = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
      if (bytes(s.freeBytes) === undefined || bytes(s.usedBytes) === undefined) return;
      this.storageMeasured = true;
      this.publish({freeBytes: s.freeBytes, usedBytes: s.usedBytes, totalBytes, storedBytes:bytes(s.storedBytes), allocatedBytes:bytes(s.allocatedBytes)});
      this.publishPending();
    } catch {}
  }
  /** Unsent plays for the connected viewer. Another viewer's queue is not
   * this viewer's business and is not counted into their badge. */
  private pendingCount() {
    const owner = this.ownerKey();
    return owner ? Object.keys(this.stored.plays[owner] ?? {}).length : 0;
  }
  /** Writes the bookkeeping document. A failed write is reported rather than published as
   * saved: the Downloads screen shows it until a later write succeeds. */
  private async persist(): Promise<boolean> {
    if (!this.stateReadable) {this.publish({error: serviceText('downloads.deviceUnreadable')}); return false;}
    const json = JSON.stringify(this.stored);
    const write = this.writes.then(() => native ? native.writeState(json).then(() => true, () => false) : true);
    this.writes = write;
    const saved = await write;
    const unwritable = serviceText('downloads.deviceUnwritable');
    const error = saved ? (this.state.error === unwritable ? undefined : this.state.error) : unwritable;
    this.publish({settings: this.stored.settings, pendingProgress: this.pendingCount(), error});
    this.publishPending();
    return saved;
  }
  /** Tells the server, for each download from the rebuilt server that is whole on this device, that it
   * has arrived, so the copy the server made for it (a smaller video, a book's EPUB or CBZ) can go
   * (owner's ruling, 16 Nov). Native marks a download ready only once every file is whole: its size
   * and, where the listing gave one, its SHA-256 checked. With no answer it is tried again at the
   * next contact; a title the server no longer has needs no telling. */
  private async sendReceived() {
    const api = this.api, owner = this.ownerKey(), generation = this.generation;
    if (!native || !api || !owner || this.sendingReceived) return;
    this.sendingReceived = true;
    try {
      for (const entry of this.ownedEntries(owner)) {
        const meta = this.metas.get(entry.id);
        if (entry.state !== 'ready' || meta?.received) continue;
        try {await callServer(api, 'POST /v1/downloads/{id}/items/{itemId}/received',
          {id: entry.downloadId, ids: {itemId: entry.itemId}});}
        catch (error) {if ((error as {status?: unknown} | null)?.status !== 404) return;}
        if (this.api !== api || this.ownerKey() !== owner || this.generation !== generation) return;
        const next = {...(this.metas.get(entry.id) ?? {}), received: true as const};
        try {await native.setMeta(entry.id, JSON.stringify(next));} catch {return;}
        if (this.api !== api || this.ownerKey() !== owner || this.generation !== generation) return;
        this.metas.set(entry.id, {...(this.metas.get(entry.id) ?? {}), received: true});
      }
    } finally {this.sendingReceived = false;}
  }
  private sendingReceived = false;
  /** Changes what is kept with one entry, leaving the rest of it as it was. */
  private async patch(id: string, change: Partial<Meta>): Promise<void> {
    if (!native || !this.all.some(e => e.id === id)) return;
    const next = {...(this.metas.get(id) ?? {}), ...change};
    this.metas.set(id, next);
    await native.setMeta(id, JSON.stringify(next));
  }

  /** Remembers the exact accepted copy, so another quality of the same title
   * cannot satisfy this device's intent first. */
  async want(preparations: readonly DownloadPreparation[], display?: OfflineDisplay, originatingService?: DownloadsService) {
    if (originatingService && originatingService !== this.service) return;
    const owner = this.ownerKey(), generation = this.generation;
    if (!this.state.supported || !owner) return;
    const mine = bucket(this.stored.wanted, owner);
    for (const preparation of preparations) {
      mine[preparation.id] = {...(preparations.length === 1 && display ? display : this.display(preparation)), itemId: preparation.itemId};
      this.enrolledPreparations.set(JSON.stringify([owner, preparation.id]), preparation);
    }
    if (!await this.persist()) throw new Error(serviceText('downloads.deviceUnwritable'));
    if (this.generation === generation) void this.collect(generation);
  }
  async wantPublication(preparation: DownloadPreparation, originatingService: DownloadsService) {
    const owner = this.ownerKey(), generation = this.generation;
    if (!owner || !this.state.supported || originatingService !== this.service || !preparation.versionId || (preparation.title.kind !== 'ebook' && preparation.title.kind !== 'comic')) throw new Error('reading_unavailable');
    bucket(this.stored.wantedPublications, owner)[preparation.id] = this.display(preparation);
    this.enrolledPreparations.set(JSON.stringify([owner, preparation.id]), preparation);
    if (!await this.persist()) throw new Error('reading_progress_not_saved');
    if (generation === this.generation) void this.collect(generation);
  }
  /** How a claim on the server is named: the claim carries its own title. */
  display(preparation: DownloadPreparation): OfflineDisplay {
    const lines = downloadTitleLines(preparation.title);
    return {title: lines.title, subtitle: lines.subtitle, libraryId: preparation.libraryId, kind: preparation.title.kind};
  }
  private preparations() {
    const snapshot = this.service?.getSnapshot();
    const owner = this.ownerKey(), items = new Map<string, DownloadPreparation>();
    if (owner) for (const id of new Set([...Object.keys(this.stored.wanted[owner] ?? {}), ...Object.keys(this.stored.wantedPublications[owner] ?? {})])) {
      const preparation = this.enrolledPreparations.get(JSON.stringify([owner, id]));
      if (preparation) items.set(id, preparation);
    }
    for (const value of snapshot?.items ?? []) items.set(value.id, value);
    for (const tracked of snapshot?.requests ?? []) for (const member of tracked.request.items) if (member.preparation) items.set(member.preparation.id, member.preparation);
    return items;
  }
  /** Accepted work remains visible even before a native file can be admitted. */
  private publishPending() {
    for (const key of this.enrolledPreparations.keys()) {
      const [storedOwner, id] = JSON.parse(key) as [string, string];
      if (!this.stored.wanted[storedOwner]?.[id] && !this.stored.wantedPublications[storedOwner]?.[id]) this.enrolledPreparations.delete(key);
    }
    const owner = this.ownerKey(), preparations = this.preparations();
    const wanted: Record<string, OfflineDisplay> = owner ? {...this.stored.wanted[owner], ...this.stored.wantedPublications[owner]} : {};
    const pending = Object.entries(wanted).filter(([id]) => !this.all.some(entry => entry.id === id && this.entryOwner(entry) === owner)).map(([id, value]) => {
      const preparation = preparations.get(id), display = preparation ? this.display(preparation) : value;
      const reason = !this.service ? serviceText('downloads.connectToStart') : preparation
        ? (preparation.reason && downloadReasonMessage(preparation.reason)) || this.blocked(preparation) || serviceText('downloads.checking')
        : serviceText('downloads.blocked.preparing');
      return Object.freeze({id, itemId: preparation?.itemId ?? this.stored.wanted[owner!]?.[id]?.itemId ?? '', title: display.title, subtitle: display.subtitle, reason});
    });
    if (JSON.stringify(pending) !== JSON.stringify(this.state.pendingDownloads ?? [])) this.publish({pendingDownloads: Object.freeze(pending)});
  }
  /** Cancels this viewer's device request; the server's shared copy can still serve others. */
  async cancelPending(id: string) {
    const owner = this.ownerKey(); if (!owner) return;
    const wanted = this.stored.wanted[owner]?.[id], publication = this.stored.wantedPublications[owner]?.[id];
    if (!wanted && !publication) return;
    this.incarnations.delete(id);
    const starting = this.starts.get(id);
    const entry = this.all.find(value => value.id === id && this.entryOwner(value) === owner);
    if (entry) {await this.remove(entry); return;}
    const previousRemoval = this.stored.removed[owner]?.[id];
    try {bucket(this.stored.removed, owner)[id] = downloadCopyReference(id);} catch { /* Older preparations have no rebuilt request reference. */ }
    delete this.stored.wanted[owner]?.[id]; delete this.stored.wantedPublications[owner]?.[id];
    if (!await this.persist()) {
      if (previousRemoval) bucket(this.stored.removed, owner)[id] = previousRemoval; else delete this.stored.removed[owner]?.[id];
      if (wanted) bucket(this.stored.wanted, owner)[id] = wanted;
      if (publication) bucket(this.stored.wantedPublications, owner)[id] = publication;
      this.publishPending(); throw new Error(serviceText('downloads.deviceUnwritable'));
    }
    void this.syncDownloads();
    if (starting) {
      const started = await starting;
      if (started && started.owner === owner) {
        const generation = started.generation;
        if (!generation || !native?.removeGeneration) throw Object.assign(new Error('generation_changed'), {code: 'generation_changed'});
        try {await native.removeGeneration(id, generation);}
        catch (error) {if ((error as {code?: string} | null)?.code !== 'generation_changed') throw error;}
      }
    }
  }
  /** Fetches whatever the connected viewer asked for from this device and is now ready. One
   * that cannot be fetched (no space, over the limit) stays on the list with the reason and a
   * button. Another viewer's outstanding wants are left untouched until they connect. */
  private collect(generation = this.generation): Promise<void> {
    if (this.collection?.generation === generation) {this.collection.again = true; return this.collection.task;}
    const flight = {generation, again: false, task: Promise.resolve()};
    this.collection = flight;
    flight.task = (async () => {
      do {flight.again = false; await this.collectWanted(generation);} while (flight.again && generation === this.generation);
    })().finally(() => {if (this.collection === flight) this.collection = undefined;});
    return flight.task;
  }
  private async collectWanted(generation: number) {
    const owner = this.ownerKey(), service = this.service;
    if (generation !== this.generation || !service || !this.state.ready || !owner) return;
    try {await this.enrollRequests(owner, service, generation);} catch {return;}
    if (generation !== this.generation) return;
    const mine = this.stored.wanted[owner] ?? {}, publications = this.stored.wantedPublications[owner] ?? {};
    if (!Object.keys(mine).length && !Object.keys(publications).length) return;
    // A previous enrollment write may have failed after changing the in-memory
    // markers. No pass may transfer until those markers are durable.
    if (!await this.writes && !await this.persist()) return;
    await this.findPreparations(owner, generation);
    if (generation !== this.generation) return;
    let collected = false;
    this.publishPending();
    // A title that came to the device is no longer wanted. The markers are written once for the whole pass (a request of
    // thousands is one write, not one per title); one already held clears its own marker, so a pass cut short leaves none behind.
    const unwant = (id: string) => {delete this.stored.wanted[owner]?.[id]; delete this.stored.wantedPublications[owner]?.[id]; changed = true;};
    let changed = false;
    for (const p of this.preparations().values()) {
      if (this.generation !== generation) break;
      if ((!mine[p.id] && !publications[p.id]) || this.collecting.has(p.id)) continue;
      if (this.all.some(e => e.id === p.id)) {unwant(p.id); continue;}
      if (p.state !== 'ready' || this.blocked(p)) continue;
      this.collecting.add(p.id);
      try {
        await this.fetch(p);
        if (this.generation !== generation) break;
        unwant(p.id);
        collected = true;
      } catch (error) {
        // A book the server cannot copy (book_unreadable) is not asked for again; anything else is offered by hand on the Downloads page.
        if ((error as {code?: unknown} | null)?.code === 'book_unreadable') unwant(p.id);
      } finally { this.collecting.delete(p.id); }
    }
    if (changed) await this.persist();
    if (this.generation !== generation) return;
    // Room on the list: a large request that was waiting for it reads on.
    if (collected && [...this.enrolled.values()].some(reading => !reading.tail)) this.enrollSoon(generation);
  }
  /** A first-page server list need not contain an older accepted preparation.
   * Resolve only this device's exact pending ids, in bounded retryable passes. */
  private async findPreparations(owner: string, generation: number) {
    const service = this.service; if (!service) return;
    const visible = new Set(service.getSnapshot().items.map(value => value.id));
    const wanted = new Set([...Object.keys(this.stored.wanted[owner] ?? {}), ...Object.keys(this.stored.wantedPublications[owner] ?? {})]);
    const due: string[] = []; let more = false;
    for (const id of wanted) {
      const key = JSON.stringify([owner, id]), cached = this.enrolledPreparations.get(key), now = Date.now();
      if (visible.has(id) || cached?.state === 'ready' || now - (this.preparationChecks.get(key) ?? 0) < RETRY_EVERY_MS) continue;
      if (due.length === 200) {more = true; break;}
      due.push(id); this.preparationChecks.set(key, now);
    }
    if (!due.length) return;
    // OFF-04: one bounded read of this device's exact pending preparation ids.
    let found: readonly DownloadPreparation[];
    try {found = await service.lookupPreparations(due);}
    catch { return; } // Keep every exact intent; the next pass after the retry floor asks again.
    if (generation !== this.generation || this.service !== service) return;
    for (const preparation of found) {
      const key = JSON.stringify([owner, preparation.id]), want = this.stored.wanted[owner]?.[preparation.id];
      if (want && want.itemId !== preparation.itemId) continue;
      if (want || this.stored.wantedPublications[owner]?.[preparation.id]) this.enrolledPreparations.set(key, preparation);
    }
    if (more) this.enrollSoon(generation);
  }
  /** One more pass, shortly: a fresh one, so no single pass does unbounded work. */
  private enrollSoon(generation: number) {
    if (generation !== this.generation || this.enrollTimer) return;
    this.enrollTimer = setTimeout(() => { this.enrollTimer = undefined; void this.collect(generation); }, 0);
  }
  /** A whole season, show, album or book asked for from this device comes to this device like a
   * single title does: each member is wanted as the request lists it, however many there are.
   * The requests themselves are the server's to remember (the service finds this device's again
   * after the app was closed), so the rest of a show still arrives after a restart.
   * Members past the first page (100) are read a bounded batch of pages at a time. Each batch
   * carries on after the last member read, and what it enrolled is saved before the next one
   * starts, so a very large request is neither cut short nor read from the start again. Reading
   * waits while the list of members still to be collected is full, and goes on as they arrive, so
   * that list stays bounded however large the request is. Once the last page is reached it is
   * looked at again only when the request's counts change, which is how members the server lists
   * later are found. */
  private async enrollRequests(owner: string, service: DownloadsService, generation: number) {
    if (generation !== this.generation) return;
    const mine = bucket(this.stored.wanted, owner), enrolled = bucket(this.stored.enrolledMembers, owner), api = this.api;
    let added = false, more = false;
    for (const tracked of service.getSnapshot().requests) {
      const r = tracked.request;
      const members = enrolled[r.requestId] ??= {};
      const add = (items: readonly {itemId: string; preparation?: DownloadPreparation}[]) => {
        for (const it of items) {
          // A captured member is not yet an accepted preparation. Wait for its
          // exact id rather than borrowing a ready copy of a different quality.
          const preparation = it.preparation;
          if (!preparation || preparation.itemId !== it.itemId) continue;
          const key = JSON.stringify([owner, preparation.id]);
          if (mine[preparation.id]) this.enrolledPreparations.set(key, preparation);
          if (members[it.itemId]) continue;
          if (!mine[preparation.id] && !this.all.some(e => e.id === preparation.id && this.entryOwner(e) === owner)) {
            if (Object.keys(mine).length >= ENROLL_BACKLOG) return false;
            mine[preparation.id] = {...this.display(preparation), itemId: it.itemId};
            this.enrolledPreparations.set(key, preparation);
          }
          members[it.itemId] = true; added = true;
        }
        return true;
      };
      if (!add(r.items)) break;
      if (!r.nextCursor || !api) continue;
      const mark = `${r.total}/${r.ready}/${r.preparing}/${r.queued}/${r.failed}/${r.paused}`;
      const requestKey = JSON.stringify([owner, r.requestId]);
      const reading = this.enrolled.get(requestKey) ?? {after: r.nextCursor, read: 0, mark: '', tail: false, busy: false, retryAt: 0, unprepared: false};
      this.enrolled.set(requestKey, reading);
      // Another pass is reading this request, or it has been read to its end (or to a page that
      // could not be read) and nothing about it has changed since.
      if (reading.busy || (reading.tail && reading.mark === mark) || (reading.mark === mark && Date.now() < reading.retryAt)) continue;
      if (reading.tail && reading.mark !== mark && reading.unprepared) {reading.after = r.nextCursor; reading.read = 0; reading.unprepared = false;}
      const restart = () => { reading.after = r.nextCursor; reading.read = 0; reading.tail = true; };
      reading.mark = mark; reading.tail = false; reading.busy = true; reading.retryAt = 0;
      let held = false;
      try {
        for (let i = 0; i < ENROLL_PAGES_PER_PASS && !reading.tail; i++) {
          if (Object.keys(mine).length >= ENROLL_BACKLOG) { held = true; break; }
          const page = await getDownloadRequest(api, r.requestId, {cursor: reading.after});
          if (generation !== this.generation) return;
          if (!add(page.items)) { held = true; break; }
          if (page.items.some(item => !item.preparation)) reading.unprepared = true;
          reading.read += page.items.length;
          // More members than the request has: its list changed under the reading (or the server
          // is repeating itself). Stop, and read from the first page when it next changes.
          if (r.items.length + reading.read > page.total) restart();
          else if (!page.nextCursor || page.nextCursor === reading.after || !page.items.length) { reading.read -= page.items.length; reading.tail = true; }
          else reading.after = page.nextCursor;
        }
        if (!reading.tail && !held) more = true;
      } catch (e) {
        // A transient refusal is not the end of the list. Retry from here after a short floor,
        // even when counts are unchanged; an expired cursor starts from the first page.
        if ((e as {status?: unknown} | null)?.status === 400) { reading.after = r.nextCursor; reading.read = 0; }
        reading.retryAt = Date.now() + RETRY_EVERY_MS;
      } finally { reading.busy = false; }
    }
    // An empty initial service snapshot is not proof a request disappeared.
    // Retire its durable member markers only after an authenticated 404.
    if (api) {
      const tracked = new Set(service.getSnapshot().requests.map(value => value.request.requestId));
      let checked = 0;
      for (const requestId of Object.keys(enrolled)) {
        const key = JSON.stringify([owner, requestId]), now = Date.now();
        if (tracked.has(requestId) || now - (this.requestChecks.get(key) ?? 0) < RETRY_EVERY_MS) continue;
        if (checked++ === 20) break;
        this.requestChecks.set(key, now);
        // The counts alone answer whether the request still exists.
        try {await getDownloadRequest(api, requestId, {limit: 0});}
        catch (error) {
          if (generation !== this.generation) return;
          if ((error as {status?: number} | null)?.status === 404) {delete enrolled[requestId]; this.enrolled.delete(key); added = true;}
        }
        if (generation !== this.generation) return;
      }
    }
    if (added && !await this.persist()) throw new Error(serviceText('downloads.deviceUnwritable'));
    // The rest of a large request is the next batch's.
    if (more) this.enrollSoon(generation);
  }
  /** Bytes promised to transfers that haven't finished: counted against the limit (whole files)
   * and against free space (what is still to arrive), so starting several at once can't overshoot. */
  private starting = new Map<string, number>();
  private inFlight() {
    let whole = 0, remaining = 0;
    for (const e of this.all) if (e.state !== 'ready') { whole += e.bytes; remaining += Math.max(0, e.bytes - e.bytesDone); }
    for (const [id, bytes] of this.starting) if (!this.all.some(e => e.id === id)) { whole += bytes; remaining += bytes; }
    return {whole, remaining};
  }
  /** Why a server-prepared file cannot be fetched right now, or '' when it can. */
  blocked(preparation: DownloadPreparation): string {
    if (preparation.state !== 'ready') return serviceText('downloads.blocked.preparing');
    return this.admission(preparation.artifact.bytes);
  }
  /** The same device-space calculation used by collection and both download sheets. */
  admission(size: number): string {
    if (!this.state.ready || !this.storageMeasured || !Number.isSafeInteger(size) || size < 0) return serviceText('downloads.checking');
    const limit = this.state.settings.limitBytes, pending = this.inFlight();
    if (limit > 0 && this.state.usedBytes + pending.whole + size > limit) return serviceText('downloads.blocked.limit');
    if (size + pending.remaining + 500 * 1048576 > this.state.freeBytes) return serviceText('downloads.blocked.space');
    return '';
  }
  async fetch(preparation: DownloadPreparation): Promise<void> {
    const owner = this.ownerKey(), api = this.api, service = this.service, generation = this.generation;
    if (!native || !service || !api || !owner || !preparation.downloadId) throw new Error(serviceText('downloads.connectToStart'));
    const reason = this.blocked(preparation); if (reason) throw new Error(reason);
    this.starting.set(preparation.id, preparation.artifact.bytes);
    try { await this.transfer({id: preparation.id, itemId: preparation.itemId, quality: preparation.quality, downloadId: preparation.downloadId}, owner, api, service, generation); }
    finally { this.starting.delete(preparation.id); this.publishPending(); }
  }
  /** Starts or continues moving a download's bytes. */
  private async transfer(target: {id: string; itemId: string; quality: string; downloadId: string}, owner: string, api: Api, service: DownloadsService, generation: number, continuing = false, explicit = false) {
    if (!native) throw new Error(serviceText('downloads.unavailableOnDevice'));
    const incarnation = this.incarnations.get(target.id) ?? {};
    this.incarnations.set(target.id, incarnation);
    const current = () => {
      const entry = this.all.find(value => value.id === target.id);
      // A changed server or viewer keeps the existing "connect to start" message.
      if (this.generation !== generation || this.api !== api || this.service !== service || this.ownerKey() !== owner) throw new Error(serviceText('downloads.connectToStart'));
      if (this.incarnations.get(target.id) !== incarnation
        || continuing && (!entry || this.entryOwner(entry) !== owner || entry.itemId !== target.itemId || entry.held && !explicit)) throw Object.assign(new Error('cancelled'), {code: 'cancelled'});
      return entry;
    };
    current();
    const held = this.metas.get(target.id);
    await this.transferDownload(target, {...held, downloadId: target.downloadId}, owner, api, service, generation, continuing, current, () => this.incarnations.get(target.id) === incarnation);
  }
  private async syncDownloads(generation = this.generation): Promise<void> {
    const api = this.api, service = this.service, owner = this.ownerKey();
    if (!api || !owner || !service || generation !== this.generation) return;
    if (this.downloadSync?.generation === generation) return this.downloadSync.task;
    const current = () => generation === this.generation && this.api === api && this.ownerKey() === owner;
    const task = (async () => {
      for (const [id, ref] of Object.entries(this.stored.removed[owner] ?? {})) {
        try {await removeDownloadedTitle(api, ref.downloadId, ref.itemId);} catch (e) {if ((e as {status?: number})?.status !== 404) return;}
        if (!current()) return;
        if (this.stored.removed[owner]?.[id] === ref) {delete this.stored.removed[owner][id]; if (!await this.persist()) return;}
      }
      await keepDownloadsInStep(api, {
      held: async () => this.ownedEntries(owner).map(e => ({downloadId: e.downloadId, itemId: e.itemId})),
      remove: async ref => {if (!current()) return; for (const entry of this.ownedEntries(owner).filter(e => e.downloadId === ref.downloadId && e.itemId === ref.itemId)) await this.remove(entry, true);},
      fetch: async ref => {
        const removed = () => Object.values(this.stored.removed[owner] ?? {}).some(r => r.downloadId === ref.downloadId && r.itemId === ref.itemId);
        if (!current() || removed() || this.all.some(e => this.entryOwner(e) === owner && e.downloadId === ref.downloadId && e.itemId === ref.itemId)) return;
        const request = await readDownload(api, ref.downloadId); if (!current() || removed()) return;
        const title = request.items?.find(t => t.itemId === ref.itemId);
        if (title) await this.want([downloadPreparation(request, title)], undefined, service);
      },
      renew: async permission => {
        if (!current()) return;
        const until = Date.parse(permission.playableUntil), duration = permission.playableForMs;
        if (!Number.isFinite(until) || !safeInteger(duration, 1) || duration > 30 * 24 * 60 * 60 * 1000) throw new Error(serviceText('downloads.error.load'));
        this.trustServerTime([new Date(until - duration).toISOString()]);
        // Written when it moves earlier, or later by more than a minute (as the clock floor): a renewal on every
        // title of a large request would otherwise rewrite the whole record each time for a few seconds' change.
        const was = this.stored.permissions[owner], next = Math.min(until, this.permissionNow() + duration);
        this.stored.permissions[owner] = next;
        if ((was === undefined || next < was || next - was > 60_000) && !await this.persist()) throw new Error(serviceText('downloads.deviceUnwritable'));
      },
      });
    })().catch(() => {});
    this.downloadSync = {generation, task};
    try {await task;} finally {if (this.downloadSync?.task === task) this.downloadSync = undefined;}
  }
  private async transferDownload(target: {id: string; itemId: string; quality: string}, held: Meta, owner: string, api: Api, service: DownloadsService, generation: number, continuing: boolean, current: () => OfflineEntry | undefined, incarnationCurrent: () => boolean) {
    const described = await service.downloaded(target.id); current();
    if (described.item.id !== target.itemId || !described.files.length) throw new Error(downloadReasonMessage('several_files'));
    await this.syncDownloads(generation); current();
    if (!this.stored.permissions[owner] || this.stored.permissions[owner] <= this.permissionNow()) throw new Error(serviceText('downloads.offline.needsCheckIn'));
    const grant = await service.grant(target); const entry = current();
    const reason = this.admission(grant.artifact.bytes); if (!continuing && reason) throw new Error(reason);
    const bundle = downloadedBundle(target.id, described);
    const meta: Meta = {...held, owner, title: bundle.title, libraryId: described.item.libraryId, kind: bundle.title.kind, onServer: true};
    if (!Object.values(this.stored.plays[owner] ?? {}).some(p => p.itemId === target.itemId)) bucket(this.stored.resume, owner)[target.itemId] = (described.item.state.positionMs ?? 0) / 1000;
    await this.startTransfer(target, meta, grant, api, continuing ? entry : undefined, incarnationCurrent);
    if (generation === this.generation && incarnationCurrent()) void this.downloadBundle(target.id, api, service, generation);
  }
  private async downloadBundle(id: string, api: Api, service: DownloadsService, generation: number) {
    const meta = this.metas.get(id), reading = meta?.kind === 'ebook' || meta?.kind === 'comic';
    if (!native || this.bundling.has(id) || (meta?.bundle ?? 0) >= BUNDLE_COMPLETE && (!reading || meta?.publication === true)) return;
    this.bundling.add(id);
    try {
      const described = await service.downloaded(id), bundle = downloadedBundle(id, described);
      if (generation !== this.generation || !this.all.some(e => e.id === id)) return;
      const cached = {...bundle, subtitles: bundle.subtitles.map(track => ({...track, partFiles: undefined,
        files: Object.fromEntries(Object.entries(track.files).map(([form, file]) => [form, {...file, path: `extras/${subtitleExtraName(track.id, track.revision, form)}`}]))}))};
      await native.writeExtra(id, 'bundle.json', JSON.stringify(cached));
      const saved = new Set(this.metas.get(id)?.extras ?? []);
      const files: [string, string][] = Object.entries(described.item.images).filter(([role]) => ['poster', 'cover', 'backdrop'].includes(role));
      let complete = true;
      for (const [name, url] of files) {
        if (saved.has(name)) continue;
        const outcome = await native.fetchExtra(id, name, url).catch(() => 'later');
        if (generation !== this.generation || !this.all.some(e => e.id === id)) return;
        if (outcome === 'saved') saved.add(name); else if (outcome !== 'missing') complete = false;
      }
      // The existing subtitle cache names its files by track identity.
      for (const track of bundle.subtitles) {
        for (const [name, url] of subtitleExtraFiles(track)) {
          if (track.partFiles?.length) {
            const captions: {text: string; offsetSeconds: number}[] = [];
            for (const [part, file] of track.partFiles.entries()) {
              const stored = `${name}-part-${part + 1}`;
              const outcome = await native.fetchExtra(id, stored, file.path).catch(() => 'later');
              if (generation !== this.generation || !this.all.some(e => e.id === id)) return;
              if (outcome !== 'saved') {complete = false; break;}
              captions.push({text: await native.readExtra(id, stored), offsetSeconds: file.offsetSeconds});
            }
            if (captions.length === track.partFiles.length) {await native.writeExtra(id, name, joinDownloadedVtt(captions)); saved.add(name);}
            continue;
          }
          const outcome = await native.fetchExtra(id, name, url).catch(() => 'later');
          if (generation !== this.generation || !this.all.some(e => e.id === id)) return;
          if (outcome === 'saved') {
            const source = described.subtitles.find(s => s.id === track.id);
            if (source?.format === 'srt') {try {await native.writeExtra(id, name, downloadedSubtitleText('srt', await native.readExtra(id, name)));} catch {complete = false; continue;}}
            saved.add(name);
          } else if (outcome !== 'missing') complete = false;
        }
      }
      // A book also keeps what reading it with no server needs: which reader opens its file and the
      // server's fingerprint of it, and the profile's place as the server had it.
      let book: DownloadedBook | undefined, reading: Partial<Meta> = {};
      if (bundle.title.kind === 'ebook' || bundle.title.kind === 'comic') {
        const format = offlineBookFormat(described.files[0]?.container), owner = this.metas.get(id)?.owner;
        if (format && described.files.length === 1) {
          // The listing names the exact file the download is (or was made from): places are made on it.
          if (!/^[0-9a-f]{16}$/.test(described.revision ?? '')) throw new Error('reading_unavailable');
          book = {format, versionId: described.versionId, revision: described.revision!};
          const position = described.item.state?.position;
          if (owner && typeof position === 'string' && position && !this.stored.places[owner]?.[described.item.id]) {
            bucket(this.stored.places, owner)[described.item.id] = {position, madeAt: Date.parse(described.item.state?.updatedAt ?? '') || Date.now(), sent: true};
            await this.persist();
          }
          if (owner) await this.refreshBookNotes(api, owner, described.item.id);
        }
        // Checked either way: a file this device cannot read is not asked about again.
        reading = {publication: true, ...(book ? {book} : {})};
      }
      await this.patch(id, {title: bundle.title, kind: bundle.title.kind, extras: [...saved], bundle: complete ? BUNDLE_COMPLETE : 1, ...reading});
    } catch { /* Completed on a later pass. */ }
    finally {this.bundling.delete(id);}
  }
  private async startTransfer(target: {id: string; itemId: string; quality: string}, meta: Meta, grant: Awaited<ReturnType<DownloadsService['grant']>>, api: Api, continuing?: OfflineEntry, current: () => boolean = () => true): Promise<StartedTransfer | undefined> {
    if (!native) return;
    const lines = meta.title ? downloadTitleLines(meta.title) : {title: '', subtitle: ''};
    this.metas.set(target.id, meta);
    const request = {
      id: target.id, url: grant.files ? grant.url : api.mediaUrl(grant.url),
      ...(grant.files && grant.files.length > 1 ? {files: grant.files.map((file, index) => ({url: file.url, bytes: file.artifact.bytes, sha256: file.artifact.sha256, fileName: file.artifact.fileName,
        sourceId: `${file.artifact.ref}_${index + 1}`,
        ...(file.offsetSeconds !== undefined ? {offsetSeconds: file.offsetSeconds} : {}),
        ...(file.durationSeconds !== undefined ? {durationSeconds: file.durationSeconds} : {}),
        ...(file.startSeconds !== undefined ? {startSeconds: file.startSeconds} : {}),
        ...(file.endSeconds !== undefined ? {endSeconds: file.endSeconds} : {})}))} : {}),
      fileName: grant.artifact.fileName, bytes: grant.artifact.bytes, sha256: grant.artifact.sha256,
      sourceId: grant.artifact.ref, contentType: grant.artifact.contentType, itemId: target.itemId, title: lines.title, subtitle: lines.subtitle, quality: target.quality, meta: JSON.stringify(meta),
      ...(continuing ? {expectedGeneration: this.publicationStateGenerations.get(continuing)} : {}),
    };
    // A successful native start rotates its generation too. Its own exact
    // publication must not cancel the JS work still fetching this copy's extras.
    const starting = {meta: request.meta, itemId: target.itemId, current};
    this.startingRequests.set(target.id, starting);
    const task = (continuing && native.resume ? native.resume(request) : native.start(request)).then(raw => {
      // Both native owners return their published row. The promise may arrive
      // before the change event, so publish it before importing the bookmark.
      if (raw?.id === target.id && current()) this.accept(raw);
      const entry = this.all.find(value => value.id === target.id);
      const generation = typeof raw?.publicationGeneration === 'string' ? raw.publicationGeneration : entry && this.publicationStateGenerations.get(entry);
      return {id: target.id, owner: meta.owner, generation};
    });
    this.starts.set(target.id, task);
    try {return await task;} finally {
      if (this.starts.get(target.id) === task) this.starts.delete(target.id);
      if (this.startingRequests.get(target.id) === starting) this.startingRequests.delete(target.id);
    }
  }
  /** Looks after downloads that need something only a reachable server can give: one that stopped
   * by itself is started again and one that arrived without its extras gets them. Nothing here touches a download the person paused. */
  private async tend(generation: number) {
    const owner = this.ownerKey(), api = this.api, service = this.service;
    if (generation !== this.generation || !owner || !api || !service || !this.state.ready) return;
    await this.measure();
    if (generation !== this.generation) return;
    await this.syncDownloads(generation);
    if (generation !== this.generation) return;
    void this.collect(generation);
    void this.sendReceived();
    void this.syncProgress(generation);
    const now = Date.now();
    for (const entry of this.ownedEntries(owner)) {
      if (this.generation !== generation) return;
      const meta = this.metas.get(entry.id) ?? {};
      const stopped = !entry.held && ((entry.state === 'failed' && RETRYABLE.has(entry.error)) || entry.state === 'paused');
      if (stopped) {
        if (now - (this.retried.get(entry.id) ?? 0) < RETRY_EVERY_MS) continue;
        this.retried.set(entry.id, now);
        await this.transfer({id: entry.id, itemId: entry.itemId, quality: entry.quality, downloadId: entry.downloadId}, owner, api, service, generation, true).catch(e => {
          // The server no longer has this to hand over (its claim ended while the transfer was
          // stopped). Asking every half minute would change nothing: it is left for a quarter of
          // an hour, and Try again says why straight away.
          const status = (e as {status?: unknown} | null)?.status;
          if (status === 404 || status === 409 || status === 410) this.retried.set(entry.id, now + 15 * 60 * 1000);
        });
      } else if (entry.state === 'ready' && ((meta.bundle ?? 0) < BUNDLE_COMPLETE || (entry.kind === 'ebook' || entry.kind === 'comic') && meta.publication !== true) && entry.onServer) {
        if (now - (this.retried.get('bundle:' + entry.id) ?? 0) < 10 * 60 * 1000) continue;
        this.retried.set('bundle:' + entry.id, now);
        await this.downloadBundle(entry.id, api, service, generation);
      }
    }
  }
  /** Continues a paused or stopped transfer from the bytes already on the device. */
  async resume(entry: OfflineEntry) {
    const owner = this.ownerKey(), api = this.api, service = this.service;
    if (!owner || !api || !service || this.entryOwner(entry) !== owner) throw new Error(serviceText('downloads.connectToContinue'));
    this.retried.delete(entry.id);
    const live = this.all.find(value => value.id === entry.id);
    if (!live || this.entryOwner(live) !== owner || live.itemId !== entry.itemId || this.publicationStateGenerations.get(live) !== this.publicationStateGenerations.get(entry)) throw Object.assign(new Error('cancelled'), {code: 'cancelled'});
    await this.transfer({id: entry.id, itemId: entry.itemId, quality: entry.quality, downloadId: entry.downloadId}, owner, api, service, this.generation, true, true);
  }
  /** The person paused it: it stays paused until they continue it. */
  pause(entry: OfflineEntry) { this.incarnations.delete(entry.id); return native?.pause(entry.id) ?? Promise.resolve(); }
  /** Removes a download from this device. The server is told this device no longer holds the
   * copy (unless it was the server that withdrew it); its own claim on the title is left for the
   * viewer's other devices. */
  async remove(entry: OfflineEntry, withdrawn = false) {
    const owner = this.entryOwner(entry), generation = this.generation, api = this.api;
    if (!withdrawn && owner) {
      bucket(this.stored.removed, owner)[entry.id] = {downloadId: entry.downloadId, itemId: entry.itemId};
      if (!await this.persist()) throw new Error(serviceText('downloads.deviceUnwritable'));
    }
    this.incarnations.delete(entry.id);
    await native?.remove(entry.id);
    this.retried.delete(entry.id);
    if (owner) {
      delete this.stored.resume[owner]?.[entry.itemId];
      delete this.stored.wanted[owner]?.[entry.id];
      delete this.stored.wantedPublications[owner]?.[entry.id];
    }
    if (!await this.persist()) throw Object.assign(new Error('reading_progress_not_saved'), {code: 'persistence_error'});
    if (!withdrawn && api && generation === this.generation && owner === this.ownerKey()) void this.syncDownloads(generation);
  }
  /** Removes every download on this device that is not the connected viewer's. They are never
   * shown; this is how the space they take is given back. */
  async removeOthers() {
    const owner = this.ownerKey();
    if (!owner || !native) return;
    const generation = this.generation;
    const entries = this.all.filter(e => this.entryOwner(e) !== owner);
    for (const entry of entries) {
      if (generation !== this.generation || this.ownerKey() !== owner) return;
      this.incarnations.delete(entry.id);
      await native.remove(entry.id); if (generation !== this.generation || this.ownerKey() !== owner) return;
      const erasedOwner = this.entryOwner(entry);
      if (erasedOwner) {delete this.stored.wanted[erasedOwner]?.[entry.id]; delete this.stored.wantedPublications[erasedOwner]?.[entry.id];}
    }
    if (!await this.persist()) throw Object.assign(new Error('reading_progress_not_saved'), {code: 'persistence_error'});
    void this.measure();
  }

  /** How one other-viewer member was published. */
  private otherMember(entry: OfflineEntry): OtherMember {
    return Object.freeze({id: entry.id, owner: this.entryOwner(entry) ?? '',
      artifact: entry.artifact?.sha256 ?? '',
      generation: this.publicationStateGenerations.get(entry) ?? '',
      bytes: entry.state === 'ready' ? entry.bytes : entry.bytesDone});
  }
  private sameOtherMember(entry: OfflineEntry, captured: OtherMember): boolean {
    const live = this.otherMember(entry);
    return live.id === captured.id && live.owner === captured.owner && live.artifact === captured.artifact
      && live.generation === captured.generation && live.bytes === captured.bytes;
  }
  private changedOthers() {return Object.assign(new Error('downloads_others_changed'), {code: 'others_changed'});}
  /** Removes exactly the other viewers' downloads the caller confirmed. The published
   * opaque revision and stored size identify that set; every member is captured with its
   * owner, artifact and native generation. A changed member is refused instead of
   * being replaced by a later file, and the removal is conditional at the native owner's
   * accepting turn. Members are the original captured ones: our own accepted removals let
   * the remaining originals continue, while an externally added member is never adopted. */
  async removeOthersConfirmed(expected: OfflineOthersTarget) {
    const owner = this.ownerKey();
    if (!owner || !native) return;
    if (!Number.isSafeInteger(expected.revision) || expected.revision < 1
      || !Number.isSafeInteger(expected.bytes) || expected.bytes < 1
      || expected.revision !== this.othersRevision || expected.bytes !== this.state.otherBytes)
      throw this.changedOthers();
    // Only the conditional native removal can keep this target to the accepting turn;
    // without it nothing is deleted (there is no remove(id) fallback).
    const removal = native.removeGeneration;
    if (!removal) throw this.changedOthers();
    const captured = this.all.filter(e => this.entryOwner(e) !== owner).map(e => this.otherMember(e));
    if (!captured.length || captured.some(member => !member.generation)) throw this.changedOthers();
    const generation = this.generation;
    for (const member of captured) {
      if (generation !== this.generation || this.ownerKey() !== owner) return;
      const live = this.all.find(e => e.id === member.id);
      if (!live || !this.sameOtherMember(live, member)) throw this.changedOthers();
      this.incarnations.delete(member.id);
      try {await removal.call(native, member.id, member.generation);}
      catch (error) {
        const code = (error as {code?: unknown} | null)?.code;
        if (code === 'generation_changed' || code === 'others_changed') throw this.changedOthers();
        throw error;
      }
      if (generation !== this.generation || this.ownerKey() !== owner) return;
      if (member.owner) {delete this.stored.wanted[member.owner]?.[member.id]; delete this.stored.wantedPublications[member.owner]?.[member.id];}
    }
    if (!await this.persist()) throw Object.assign(new Error('reading_progress_not_saved'), {code: 'persistence_error'});
    void this.measure();
  }

  /** The time offline permission is judged by: never earlier than the
   * latest time this device has trusted. The floor is written when it moves by more than a minute. */
  private permissionNow(): number {
    // No client-clock requirements: the server's time as this device knows it (its offset is kept
    // across restarts), never the bare device clock; the floor still stops a clock turned back.
    const {nowMs, floorMs} = receiptTime({floorMs: this.stored.clockFloor}, serverNow());
    if (floorMs - this.stored.clockFloor > 60_000) { this.stored.clockFloor = floorMs; void this.persist(); }
    return nowMs;
  }
  /** The server supplied a new offline permission using its own clock. */
  private trustServerTime(issuedAt: readonly string[]) {
    const floor = serverReceiptFloor({floorMs: this.stored.clockFloor}, issuedAt);
    // Held at once; written when the server sets it back, or forward by more than a minute (as in permissionNow).
    if (floor !== this.stored.clockFloor) { const write = floor < this.stored.clockFloor || floor - this.stored.clockFloor > 60_000; this.stored.clockFloor = floor; if (write) void this.persist(); }
  }
  playabilityReason(entry: OfflineEntry, viewer: Viewer | undefined = this.viewer): OfflinePlayabilityReason|null {
    if (!viewer) return 'sign_in';
    if (entry.state !== 'ready') return 'not_finished';
    if (this.entryOwner(entry) !== offlineOwnerKey(viewer)) return 'other_profile';
    const until = this.stored.permissions[offlineOwnerKey(viewer)];
    if (!until) return 'needs_setup';
    if (this.permissionNow() < until) return null;
    // Remember expiry even inside the clock floor's usual one-minute write interval.
    if (this.stored.clockFloor < until) {this.stored.clockFloor = until; void this.persist();}
    return 'needs_check_in';
  }
  playable(entry: OfflineEntry, viewer: Viewer | undefined = this.viewer): string {
    const reason = this.playabilityReason(entry,viewer);
    const messages = {sign_in:'downloads.offline.signIn',not_finished:'downloads.offline.notFinished',needs_setup:'downloads.offline.needsSetup',needs_check_in:'downloads.offline.needsCheckIn',other_profile:'downloads.offline.otherProfile'} as const;
    return reason ? serviceText(messages[reason]) : '';
  }
  /** A line to show under a download that plays but has something worth saying about it. */
  note(entry: OfflineEntry): string {
    if (!entry.onServer) return serviceText('downloads.offline.goneFromServer');
    if (entry.changedOnServer) return serviceText('downloads.offline.changedOnServer');
    return '';
  }
  private resumePoint(entry: OfflineEntry): number {
    const owner = this.entryOwner(entry);
    return Math.max(0, (owner ? this.stored.resume[owner]?.[entry.itemId] : 0) ?? 0);
  }
  /** A local file read cannot escape the viewer/generation/artifact that admitted it. */
  private localRead(entry: OfflineEntry): () => OfflineEntry {
    const generation=this.generation, owner=this.ownerKey();
    const identity=(value:OfflineEntry)=>JSON.stringify([value.id,value.itemId,this.entryOwner(value),value.downloadId,value.artifact?.sha256,value.bytes]);
    const expected=identity(entry);
    const current=()=>{
      const live=this.all.find(value=>value.id===entry.id);
      if(!owner||generation!==this.generation||owner!==this.ownerKey()||!live||this.entryOwner(live)!==owner||identity(live)!==expected)throw new Error(serviceText('downloads.offline.otherProfile'));
      return live;
    };
    current();return current;
  }
  async displayFacts(entry: OfflineEntry): Promise<OfflineEntryFacts> {
    const current=this.localRead(entry);
    let bundle:unknown;
    try { const raw=await native?.readExtra(entry.id,'bundle.json');bundle=raw?JSON.parse(raw):undefined; } catch { /* Unknown cached fields stay absent. */ }
    const live=current(),owner=this.ownerKey()!,result:{resumePositionSeconds?:number;backdropUrl?:string;media?:OfflineEntryFacts['media']}={};
    const position=this.stored.resume[owner]?.[live.itemId];
    if(typeof position==='number'&&Number.isFinite(position)&&position>=0)result.resumePositionSeconds=position;
    const dir=this.dirs.get(live.id),meta=this.metas.get(live.id);
    if(dir&&meta?.extras?.includes('backdrop'))result.backdropUrl=dir+'extras/backdrop';
    const value=bundle!==null&&typeof bundle==='object'?bundle as Record<string,unknown>:undefined;
    const raw=value?.itemId===live.itemId&&value?.preparationId===live.id?value.media:undefined;
    if(raw!==null&&typeof raw==='object') {
      const source=raw as Record<string,unknown>,media:{container?:string;videoCodec?:string;audioCodec?:string;width?:number;height?:number}={};
      for(const key of ['container','videoCodec','audioCodec'] as const)if(typeof source[key]==='string'&&source[key].length>0&&source[key].length<=128&&!/[\u0000-\u001f\u007f]/.test(source[key]))media[key]=source[key];
      for(const key of ['width','height'] as const)if(typeof source[key]==='number'&&Number.isSafeInteger(source[key])&&source[key]>0)media[key]=source[key];
      if(Object.keys(media).length)result.media=Object.freeze(media);
    }
    return Object.freeze(result);
  }
  publicationSource(entry: OfflineEntry): Readonly<{sourceId: string; revision: string}> {
    const current = this.localRead(entry), live = current();
    if (!nativePublicationReadsAvailable() || this.playabilityReason(live)) throw new Error('reading_unavailable');
    const book = this.downloadedBook(live);
    return {sourceId: book.versionId, revision: book.revision};
  }
  /** A book downloaded from the rebuilt server, once what reading it needs is kept beside it. */
  private downloadedBook(entry: OfflineEntry): DownloadedBook {
    const book = this.metas.get(entry.id)?.book;
    if (!book || !entry.artifact) throw new Error('reading_unavailable');
    return book;
  }
  /** The profile's place in a downloaded book, as this device last knew it. */
  private bookProgress(owner: string, itemId: string, book: DownloadedBook): ReadingProgress {
    const place = this.stored.places[owner]?.[itemId], manifest = {itemId, sourceId: book.versionId, revision: book.revision, format: book.format, wholeFile: true} as unknown as ReadingManifest;
    return place ? readingPlaceProgress(place.position, new Date(place.madeAt).toISOString(), manifest) : {progress: null};
  }
  /** Opens a book downloaded from the rebuilt server, with no server. Its place is kept on this
   * device with when it was made, and sent when the reader closes or the server is next reached
   * (sendBookPlaces); with history paused nothing is kept (10-reading rules 15, 16, 46). */
  async readingSession(entry: OfflineEntry, scope: Readonly<{signal: AbortSignal; current: () => boolean; paused: () => boolean}>): Promise<OfflineReadingSession> {
    const read = this.localRead(entry), owner = this.ownerKey(), live = read(), api = this.api, itemId = live.itemId;
    if (!native || !owner || this.playabilityReason(live)) throw new Error('reading_unavailable');
    const book = this.downloadedBook(live), artifact = live.artifact!;
    const current = () => {try {return scope.current() && !this.playabilityReason(read());} catch {return false;}};
    return openDownloadedBook({id: live.id, owner, digest: artifact.sha256, bytes: artifact.bytes, itemId, versionId: book.versionId, revision: book.revision, format: book.format,
      title: live.info?.title || live.title, current, signal: scope.signal, onClose: () => {void this.sendBookPlaces(); void this.sendBookNotes();},
      progress: async () => {
        // A place this device has not sent yet is the newest it knows. Otherwise the server is asked,
        // briefly, for one made on another device; the book's notes are read again the same way.
        if (api) {
          const ask = new AbortController(), timer = setTimeout(() => ask.abort(), 1500);
          try {
            const known = this.stored.places[owner]?.[itemId];
            const [item] = await Promise.all([!known || known.sent ? callServer(api, 'GET /v1/items/{id}', {id: itemId, signal: ask.signal}) : undefined,
              this.refreshBookNotes(api, owner, itemId, ask.signal)]);
            const state = item?.state, before = this.stored.places[owner]?.[itemId];
            if (typeof state?.position === 'string' && state.position && (!before || before.sent) && current()) {
              bucket(this.stored.places, owner)[itemId] = {position: state.position, madeAt: Date.parse(state.updatedAt ?? '') || Date.now(), sent: true};
              await this.persist();
            }
          } catch { /* Out of reach: what this device knows stands. */ } finally {clearTimeout(timer);}
        }
        return this.bookProgress(owner, itemId, book);
      },
      save: async (request, manifest) => {
        if (!current()) throw new Error('reading_retired');
        if (scope.paused()) return {saved: false, progress: null};
        const madeAt = Date.now(), previous = this.stored.places[owner]?.[itemId];
        bucket(this.stored.places, owner)[itemId] = {position: readingPlacePosition(request.location, manifest), madeAt};
        if (!await this.persist()) {if (previous) bucket(this.stored.places, owner)[itemId] = previous; else delete this.stored.places[owner]?.[itemId]; throw new Error('reading_progress_not_saved');}
        return {saved: true, progress: {location: request.location, observedAt: madeAt, revision: request.expectedRevision + 1, sourceId: book.versionId, sourceRevision: book.revision, stale: false}};
      },
      notes: {
        list: manifest => this.bookNoteRows(owner, itemId).flatMap(row => {const entry = readingAnnotationEntry(row, manifest); return entry ? [entry] : [];}),
        change: async (mutation, manifest) => {
          if (!current()) throw new Error('reading_retired');
          const notes = this.stored.notes[owner]?.[itemId] ?? {rows: [], pending: {}}, previous = this.stored.notes[owner]?.[itemId];
          const change: BookNoteChange = {madeAt: Date.now(), ...(mutation.operation === 'upsert' ? {body: readingAnnotationBody(mutation, manifest)} : {})};
          bucket(this.stored.notes, owner)[itemId] = {rows: notes.rows, pending: {...notes.pending, [mutation.id]: change}};
          if (!await this.persist()) {if (previous) bucket(this.stored.notes, owner)[itemId] = previous; else delete this.stored.notes[owner]?.[itemId]; throw new Error('reading_annotation_not_saved');}
          void this.sendBookNotes();
        },
      },
    });
  }
  /** A downloaded book's notes as this device knows them: the server's rows with this device's
   * changes not yet sent laid over them. */
  private bookNoteRows(owner: string, itemId: string): readonly Readonly<Record<string, unknown>>[] {
    const notes = this.stored.notes[owner]?.[itemId]; if (!notes) return [];
    const rows = new Map(notes.rows.map(row => [String(row.id), row]));
    for (const [id, change] of Object.entries(notes.pending)) {
      if (!change.body) {rows.delete(id); continue;}
      const at = new Date(change.madeAt).toISOString();
      rows.set(id, {...change.body, id, createdAt: rows.get(id)?.createdAt ?? at, updatedAt: at});
    }
    return [...rows.values()];
  }
  /** Reads a book's notes from the server for this device to keep (at most 5,000, rule 25). */
  private async refreshBookNotes(api: Api, owner: string, itemId: string, signal?: AbortSignal) {
    const rows: Readonly<Record<string, unknown>>[] = []; let cursor: string | undefined;
    for (let page = 0; page < 100; page++) {
      const answer = await callServer(api, 'GET /v1/items/{id}/annotations', {id: itemId, query: {cursor}, signal});
      rows.push(...answer.items.map(row => ({...row}) as Record<string, unknown>));
      cursor = answer.nextCursor || undefined; if (!cursor) break;
    }
    if (this.api !== api || this.ownerKey() !== owner) return;
    bucket(this.stored.notes, owner)[itemId] = {rows, pending: this.stored.notes[owner]?.[itemId]?.pending ?? {}};
    await this.persist();
  }
  /** Sends the notes changed here with no server, at most 500 at once, each with how long ago it was
   * made: the later change to the same one wins (10-reading rule 28). One the server refuses for good
   * (its book is gone) is dropped; with no answer they all wait for the next contact. */
  private async sendBookNotes() {
    const api = this.api, owner = this.ownerKey();
    if (!api || !owner || !this.stored.notes[owner] || this.sendingNotes) return;
    this.sendingNotes = true;
    try {for (let batch = 0; batch < 20; batch++) {
      const sent = Object.entries(this.stored.notes[owner] ?? {}).flatMap(([itemId, notes]) => Object.entries(notes.pending).map(([id, change]) => ({itemId, id, change}))).slice(0, 500);
      if (!sent.length) return;
      const now = Date.now(), empty = {kind: '', color: '', location: '', madeOn: '', progress: 0, chapter: '', text: '', note: '', extra: null};
      const answer = await callServer(api, 'POST /v1/annotations:batch', {body: {changes: sent.map(({itemId, id, change}) => ({...empty, ...change.body, itemId, id, delete: !change.body,
        madeAgoMs: Math.min(maxEditAgeMs, Math.max(0, now - change.madeAt))}) as AnnotationChange)}}).catch(() => undefined);
      if (!answer || this.api !== api || this.ownerKey() !== owner || answer.results.length !== sent.length) return;
      sent.forEach(({itemId, id, change}, index) => {
        const notes = this.stored.notes[owner]?.[itemId], result = answer.results[index]!;
        if (!notes || result.id !== id) return;
        // The server's answer stands; a refused change leaves the server's row as it was.
        const deleted = !change.body && (!result.error || result.error.code === 'annotation_deleted');
        const rows = result.annotation || deleted ? notes.rows.filter(row => row.id !== id) : [...notes.rows];
        if (result.annotation) rows.push({...result.annotation});
        const pending = {...notes.pending}; if (pending[id] === change && !result.error?.retry) delete pending[id];
        bucket(this.stored.notes, owner)[itemId] = {rows, pending};
      });
      // One the server asks to have sent again waits for the next contact.
      if (!await this.persist() || answer.results.some(result => result.error?.retry)) return;
    }} finally {this.sendingNotes = false;}
  }
  /** Hands this profile's places made with no server to the server. One it cannot reach yet is kept
   * by the outbox, which says how long ago it was made, so the later place wins wherever it came from. */
  private async sendBookPlaces() {
    const api = this.api, owner = this.ownerKey(), places = owner ? this.stored.places[owner] : undefined;
    if (!api || !owner || !places) return;
    for (const [itemId, place] of Object.entries(places)) {
      if (place.sent) continue;
      try {await callServer(api, 'PUT /v1/items/{id}/state', {id: itemId, body: {position: place.position, madeAgoMs: Math.min(maxEditAgeMs, Math.max(0, Date.now() - place.madeAt))}});}
      catch (error) {
        // Kept by the outbox, or refused for good (the book is gone): either way this device is done with it.
        const failure = error as {code?: unknown; status?: unknown} | null, status = Number(failure?.status);
        if (failure?.code !== 'sync_pending' && !(status >= 400 && status < 500 && ![401, 408, 429].includes(status))) return;
      }
      if (this.api !== api || this.ownerKey() !== owner) return;
      if (places[itemId] === place) {places[itemId] = {...place, sent: true}; await this.persist();}
    }
  }
  async readingFacts(entry: OfflineEntry): Promise<OfflineReadingFacts> {
    const owner = this.ownerKey(), live = this.localRead(entry)();
    if (!native || !owner || !nativePublicationReadsAvailable() || this.playabilityReason(live)) throw new Error('reading_unavailable');
    const book = this.downloadedBook(live), progress = this.bookProgress(owner, live.itemId, book).progress;
    return Object.freeze({format: book.format, ...(progress && !progress.stale ? {location: Object.freeze({...progress.location})} : {})});
  }
  async localMedia(entry: OfflineEntry): Promise<LocalMedia> {
    const reason = this.playable(entry); if (reason) throw new Error(reason);
    const current=this.localRead(entry);
    if (!native?.localFile) throw new Error(serviceText('downloads.offline.notPlayableHere'));
    const file = await native.localFile(entry.id);
    current();
    let extras=await this.extras(entry); const live=current();
    if (extras.bundle && file.parts?.some(p => p.sourceOffsetSeconds !== undefined && p.sourceOffsetSeconds !== p.offsetSeconds)) {
      // Chapters and markers arrive on the original clock; a converted file has its own length.
      const parts = file.parts;
      const position = (seconds: number) => {
        let part = parts[0]!;
        for (const candidate of parts) {if (seconds < (candidate.sourceOffsetSeconds ?? candidate.offsetSeconds)) break; part = candidate;}
        return part.offsetSeconds + Math.max(0, Math.min(part.durationSeconds, seconds - (part.sourceOffsetSeconds ?? part.offsetSeconds)));
      };
      extras = {...extras, bundle: {...extras.bundle, title: {...extras.bundle.title, durationSeconds: file.durationSeconds},
        chapters: extras.bundle.chapters.map(c => ({...c, startSeconds: position(c.startSeconds), endSeconds: position(c.endSeconds)})),
        markers: extras.bundle.markers.map(m => ({...m, startSeconds: position(m.startSeconds), endSeconds: position(m.endSeconds)}))}};
    }
    const latestReason=this.playable(live);if(latestReason)throw new Error(latestReason);
    const playId = 'play_' + random();
    this.playing.set(entry.id, playId);
    return {entryId: live.id, itemId: live.itemId, playId, fileUrl: file.url, ...(file.parts?.length ? {parts: file.parts} : {}), durationSeconds: Math.max(0, Number(file.durationSeconds) || 0), startSeconds: this.resumePoint(live), extras};
  }
  /** What came with a download: its description, chapters, markers and audio tracks as the server
   * described them, and its subtitle tracks as files on this device. */
  async extras(entry: OfflineEntry): Promise<OfflineExtras> {
    const dir = this.dirs.get(entry.id) ?? '', saved = new Set(this.metas.get(entry.id)?.extras ?? []);
    let bundle: DownloadBundle | undefined;
    try { const raw = await native?.readExtra(entry.id, 'bundle.json'); bundle = raw ? (JSON.parse(raw) as DownloadBundle) : undefined; } catch { bundle = undefined; }
    // Each track in the form closest to how it was authored that is whole on this device (a
    // VobSub track with both its files, a styled script with the fonts that came with it); text last.
    const subtitles = (bundle?.subtitles ?? []).flatMap(s => { const stored = dir ? storedSubtitle(s, saved, dir + 'extras/') : undefined; return stored ? [stored] : []; });
    const tiles = dir ? (bundle?.trickplay?.tiles ?? []).map((_, index) => trickplayTileName(index)) : [];
    const trickplayTiles = tiles.length && tiles.every(name => saved.has(name)) ? tiles.map(name => dir + 'extras/' + name) : [];
    const chapterImages: Record<number, string> = {};
    if (dir) for (const c of bundle?.chapters ?? []) if (c.image && saved.has(chapterImageName(c.index))) chapterImages[c.index] = dir + 'extras/' + chapterImageName(c.index);
    return Object.freeze({bundle, subtitles: Object.freeze(subtitles), backdropUrl: saved.has('backdrop') && dir ? dir + 'extras/backdrop' : '', trickplayTiles: Object.freeze(trickplayTiles), chapterImages: Object.freeze(chapterImages)});
  }
  private lastLocalWrite = new Map<string, number>();
  private localPlayTime = new Map<string, {entryId: string; at: number; playing: boolean; playedMs: number}>();
  /** Progress from Portico's player for a downloaded title. A playing position is written at most
   * every 10 seconds; pauses, stops and the end always are. */
  recordProgress(progress: LocalProgress): void {
    const now = Date.now(), last = this.lastLocalWrite.get(progress.entryId) ?? 0;
    const previous = this.localPlayTime.get(progress.playId);
    const playedMs = (previous?.playedMs ?? 0) + (previous?.playing ? Math.max(0, Math.min(30_000, now - previous.at)) : 0);
    this.localPlayTime.set(progress.playId, {entryId: progress.entryId, at: now, playing: progress.state === 'playing', playedMs});
    if (this.localPlayTime.size > MAX_PLAYS) this.localPlayTime.delete(this.localPlayTime.keys().next().value!);
    if (progress.state === 'playing' && now - last < 10000) return;
    this.lastLocalWrite.set(progress.entryId, now);
    if (this.lastLocalWrite.size > 32) this.lastLocalWrite.delete(this.lastLocalWrite.keys().next().value!);
    void this.observed({id: progress.entryId, playId: progress.playId, positionSeconds: progress.positionSeconds, durationSeconds: progress.durationSeconds, ended: progress.ended, final: progress.state !== 'playing', playedMs});
  }
  /** One observation of a play. Every play of a download is its own entry in the journal, kept
   * until the server has answered for it; its latest position replaces its earlier ones. */
  private async observed(event: {id: string; playId?: string; positionSeconds: number; durationSeconds: number; ended: boolean; final: boolean; playedMs?: number}) {
    const entry = this.all.find(e => e.id === event.id); if (!entry) return;
    // Journal this play under the download's owner, even if it is sent after reconnecting.
    const owner = this.entryOwner(entry); if (!owner) return;
    const playId = event.playId ?? this.playing.get(entry.id) ?? 'play_' + random();
    this.playing.set(entry.id, playId);
    const position = Math.max(0, event.positionSeconds);
    const completion = offlineCompletion(entry.kind ?? entry.info?.kind, position, event.durationSeconds, event.ended);
    bucket(this.stored.resume, owner)[entry.itemId] = completion.resumeSeconds;
    const plays = bucket(this.stored.plays, owner);
    const clockKey = JSON.stringify([owner, entry.itemId]);
    const pendingTimes = Object.values(plays).filter(p => p.itemId === entry.itemId).map(p => Date.parse(p.observedAt)).filter(Number.isFinite);
    const observedAt = Math.max(Date.now(), Math.max(this.observationTimes.get(clockKey) ?? 0, ...pendingTimes) + 1);
    this.observationTimes.set(clockKey, observedAt);
    if (this.observationTimes.size > MAX_PLAYS) this.observationTimes.delete(this.observationTimes.keys().next().value!);
    plays[playId] = {playId, itemId: entry.itemId, downloadId: entry.downloadId, playedMs: event.playedMs ?? 0, positionSeconds: position, ...(event.ended ? {ended: true} : {}), observedAt: new Date(observedAt).toISOString()};
    if (Object.keys(plays).length > MAX_PLAYS) {
      const latest = new Map<string, Play>();
      for (const play of Object.values(plays)) {
        const previous = latest.get(play.itemId);
        if (!previous || Date.parse(play.observedAt) >= Date.parse(previous.observedAt)) latest.set(play.itemId, play);
      }
      // Distinct titles are never evicted: resume is a local cache, not an outbox.
      // A failed durable write remains visible instead of silently dropping a title.
      for (const [id, play] of Object.entries(plays)) if (latest.get(play.itemId) !== play) delete plays[id];
    }
    await this.persist();
    if (!event.final) return;
    void this.syncProgress();
    if (!completion.finished) return;
    this.playing.delete(entry.id);
  }
  /** Sends plays made offline, for the connected viewer only. The server records each one and
   * keeps whichever state is newer. A play made by a different viewer stays queued until that
   * viewer connects; it is never posted under this one. */
  syncProgress(generation = this.generation): Promise<void> {
    if (this.progressSync?.generation === generation) return this.progressSync.task;
    const task = this.drainProgress(generation);
    this.progressSync = {generation, task};
    void task.finally(() => { if (this.progressSync?.task === task) this.progressSync = undefined; }).catch(() => {});
    return task;
  }
  private async removeConfirmedFinished(itemId: string, owner: string) {
    if (!this.stored.settings.deleteWatched || owner !== this.ownerKey() || Object.values(this.stored.plays[owner] ?? {}).some(play => play.itemId === itemId)) return;
    for (const entry of this.ownedEntries(owner).filter(entry => entry.itemId === itemId)) {
      if (owner !== this.ownerKey()) return;
      if ([...this.localPlayTime.values()].some(play => play.entryId === entry.id && play.playing)) continue;
      await this.remove(entry);
    }
  }
  private async drainProgress(generation: number) {
    const api = this.api, owner = this.ownerKey();
    if (generation !== this.generation || !api || !owner) return;
    while (this.generation === generation) {
      const entries = Object.values(this.stored.plays[owner] ?? {}).slice(0, 100);
      if (!entries.length) return;
      try {
        for (const play of entries) {
          const receipt = await reportDownloadPlay(api, {itemId: play.itemId, playId: play.playId, positionMs: Math.round(play.positionSeconds * 1000), playedMs: play.playedMs ?? 0, madeAgoMs: Math.max(0, Date.now() - Date.parse(play.observedAt))});
          if (this.generation !== generation) return;
          const result = receipt?.plays?.find(result => result.itemId === play.itemId && result.playId === play.playId);
          if (!result) return;
          if (this.stored.plays[owner]?.[play.playId] !== play) continue;
          delete this.stored.plays[owner][play.playId];
          if (!result.error && result.finished) await this.removeConfirmedFinished(play.itemId, owner);
        }
        await this.persist();
      } catch { return; /* Keep this and later batches for the next trigger. */ }
    }
  }
  /** Rejects, with the previous settings back in place, when the change can't be kept. */
  async setSettings(next: Partial<OfflineSettings>) {
    await this.open();
    const previous = this.stored.settings;
    this.stored.settings = {...previous, ...next};
    if (next.wifiOnly !== undefined) await native?.setCellular(!next.wifiOnly).catch(() => {});
    if (await this.persist()) {void this.collect(); return;}
    this.stored.settings = previous;
    if (next.wifiOnly !== undefined) await native?.setCellular(!previous.wifiOnly).catch(() => {});
    this.publish({settings: previous});
    throw new Error(serviceText('downloads.settingFailed'));
  }
}

const sort = (entries: OfflineEntry[]) => entries.sort((a, b) => b.addedAt - a.addedAt);
export const offlineDownloads = new OfflineDownloads();

/** Downloads as a person looks for them: an episode under its show, a song under its album, a
 * part under its book, each group in its own order; everything else on its own. Groups and single
 * titles are ordered by what was added most recently. Built from what is on the device alone. */
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
