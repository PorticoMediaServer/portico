/**
 * Downloads in a browser, on the new server. A browser keeps no files of its own: the server
 * keeps the original, or makes a smaller copy, for each title asked for, and the person saves it
 * as a file. The Downloads page lists what this profile asked for on every device, with each
 * request's titles, and reads it again while a copy is being made.
 */
import type {Download, DownloadOptions, DownloadTitle} from '../../contracts/src/next/api.ts';
import {askForDownload, changeDownload, downloadsOff, listDownloads, readDownload, readDownloadedItem, readDownloadOptions, readDownloadUsage, removeDownload} from './device-downloads.ts';
import type {ServerRequester} from './query/server-api.ts';
export {downloadsOff};
import {canonicalQualityLadder} from '../../contracts/src/types.ts';

export type BrowserDownloadsApi = ServerRequester & Readonly<{mediaUrl(path: string): string}>;
/** `cancel` stops a copy being made and `remove` lets go of one: both remove the request. */
export type BrowserDownloadAction = 'pause' | 'resume' | 'cancel' | 'retry' | 'remove';
/** One title of a request. Actions are offered on a request of one title; a request of a show,
 * season, album or playlist is acted on as a whole, from its own row. */
export type BrowserDownloadRow = Readonly<{key: string; download: Download; title: DownloadTitle & DownloadTitlePlace; actions: readonly BrowserDownloadAction[]}>;
/** Where a title sits, when the server says: its show and place in it, or its artist. Absent from older answers. */
export type DownloadTitlePlace = Readonly<{parentTitle?: string; seasonNumber?: number; episodeNumber?: number; artist?: string}>;
export type BrowserDownloadsSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  /** Downloads are turned off on this server, for this profile, or on this device. */
  unavailable: boolean;
  rows: readonly BrowserDownloadRow[];
  /** Requests of a show, season, album, artist or playlist, with how far they have got. */
  requests: readonly Readonly<{download: Download; actions: readonly BrowserDownloadAction[]}>[];
  /** What the server's copies for this profile take. */
  copiesBytes?: number;
  /** Request ids being acted on. */
  busy: readonly string[];
  failure?: unknown;
}>;

const making = (title: DownloadTitle) => title.state === 'waiting' || title.state === 'preparing';
/** A request is of one title when that title is its target. */
const single = (download: Download) => download.items?.length === 1 && download.items[0]!.itemId === download.target.id;

function titleActions(download: Download, title: DownloadTitle): BrowserDownloadAction[] {
  if (!single(download)) return [];
  if (title.state === 'paused') return ['resume', 'cancel'];
  if (making(title)) return ['pause', 'cancel'];
  if (title.state === 'failed' || title.state === 'expired') return ['retry', 'remove'];
  return ['remove'];
}
function requestActions(download: Download): BrowserDownloadAction[] {
  return [download.paused ? 'resume' : download.counts.waiting > 0 ? 'pause' : undefined, download.counts.failed > 0 ? 'retry' : undefined, 'remove']
    .filter((action): action is BrowserDownloadAction => !!action);
}

/** Reads `items` with at most four requests at once. */
async function eachLimited<T, R>(items: readonly T[], read: (item: T) => Promise<R>): Promise<R[]> {
  const out: R[] = new Array(items.length);
  let next = 0;
  await Promise.all(Array.from({length: Math.min(4, items.length)}, async () => {
    while (next < items.length) { const at = next++; out[at] = await read(items[at]!); }
  }));
  return out;
}

export class BrowserDownloads {
  private state: BrowserDownloadsSnapshot = Object.freeze({phase: 'idle', unavailable: false, rows: [], requests: [], busy: []});
  private listeners = new Set<() => void>();
  private details = new Map<string, Download>();
  private timer?: ReturnType<typeof setTimeout>;
  private generation = 0;
  private watching = false;
  private disposed = false;
  private lifetime = new AbortController();

  constructor(private readonly api: ServerRequester, private readonly pollMs = 5000) {}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<BrowserDownloadsSnapshot>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }

  /** Follows the list: read now, and again every few seconds while a copy is being made. */
  start() { this.watching = true; void this.refresh(); }
  stop() { this.watching = false; clearTimeout(this.timer); }
  dispose() { this.disposed = true; this.stop(); this.generation++; this.lifetime.abort(); this.listeners.clear(); }

  /** Reads the list again. A failure keeps what is shown and is said once. */
  async refresh(): Promise<void> {
    const generation = ++this.generation, signal = this.lifetime.signal;
    clearTimeout(this.timer);
    if (this.state.phase === 'idle') this.publish({phase: 'loading'});
    try {
      const [list, usage] = await Promise.all([listDownloads(this.api, true, signal), readDownloadUsage(this.api, signal).catch(() => undefined)]);
      // A request is read again when its counts moved or a copy of it is still being made. A server that lists
      // each request with its titles answers it all in one read.
      const stale = list.filter(download => {
        if (download.items) return false;
        const known = this.details.get(download.id);
        return !known || known.paused !== download.paused || JSON.stringify(known.counts) !== JSON.stringify(download.counts) || download.counts.waiting > 0;
      });
      const read = await eachLimited(stale, download => readDownload(this.api, download.id, signal));
      if (generation !== this.generation) return;
      const details = new Map<string, Download>();
      for (const download of list) details.set(download.id, download.items ? download : this.details.get(download.id) ?? download);
      for (const download of read) details.set(download.id, download);
      this.details = details;
      this.publishList(usage?.copiesBytes);
    } catch (error) {
      if (generation !== this.generation) return;
      if (downloadsOff(error)) this.publish({phase: 'ready', unavailable: true, rows: [], requests: [], failure: undefined});
      else this.publish({phase: this.state.rows.length || this.state.requests.length ? 'ready' : 'error', failure: error});
    }
    this.schedule(generation);
  }
  private publishList(copiesBytes: number | undefined) {
    const downloads = [...this.details.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
    const rows = downloads.flatMap(download => (download.items ?? []).map(title => Object.freeze({key: download.id + '/' + title.itemId, download, title,
      actions: titleActions(download, title)})));
    const requests = downloads.filter(download => !single(download)).map(download => Object.freeze({download, actions: requestActions(download)}));
    this.publish({phase: 'ready', unavailable: false, rows, requests, failure: undefined, ...(copiesBytes === undefined ? {} : {copiesBytes})});
  }
  private schedule(generation: number) {
    const busy = this.state.rows.some(row => making(row.title) && !row.download.paused);
    if (!this.watching || this.disposed || !busy) return;
    this.timer = setTimeout(() => { if (generation === this.generation) void this.refresh(); }, this.pollMs);
  }

  /** Acts on a whole request. Rejects with the server's refusal; the list is read again either way. */
  async act(downloadId: string, action: BrowserDownloadAction): Promise<void> {
    if (this.state.busy.includes(downloadId)) return;
    this.publish({busy: [...this.state.busy, downloadId]});
    try {
      const signal = this.lifetime.signal;
      if (action === 'remove' || action === 'cancel') await removeDownload(this.api, downloadId, signal);
      else await changeDownload(this.api, downloadId, action === 'retry' ? {retry: true} : {paused: action === 'pause'}, signal);
      if (action === 'remove' || action === 'cancel') this.details.delete(downloadId);
    } finally {
      this.publish({busy: this.state.busy.filter(id => id !== downloadId)});
      void this.refresh();
    }
  }
}

/**
 * The addresses a browser saves a ready title from, one for each of its files (a title in
 * several files saves as several). Each address carries its own permission and asks the server
 * to answer it as a file to keep.
 */
export async function browserSaveAddresses(api: BrowserDownloadsApi, downloadId: string, itemId: string, signal?: AbortSignal): Promise<string[]> {
  const item = await readDownloadedItem(api, downloadId, itemId, signal);
  if (!item.files.length) throw new Error('This title has no file to save.');
  return item.files.map(file => api.mediaUrl(file.url + (file.url.includes('?') ? '&' : '?') + 'save=1'));
}

/** The choices for downloading one title in a browser. */
export const browserDownloadOptions = (api: BrowserDownloadsApi, itemId: string, signal?: AbortSignal): Promise<DownloadOptions> =>
  readDownloadOptions(api, itemId, {}, signal);

const pause = (ms: number, signal?: AbortSignal) => new Promise<void>((resolve, reject) => {
  const stop = () => { clearTimeout(timer); reject(new DOMException('Download closed', 'AbortError')); };
  const timer = setTimeout(() => { signal?.removeEventListener('abort', stop); resolve(); }, ms);
  if (signal?.aborted) stop(); else signal?.addEventListener('abort', stop, {once: true});
});

/**
 * Asks for one title at a quality and, once the server has it ready, gives the addresses to save
 * it from. The original is ready at once; a smaller copy is waited for while the sheet is open,
 * up to five minutes, after which it stays on the Downloads page to save when it is ready. The
 * same title and quality asked again is the same request.
 */
export async function askAndSave(api: BrowserDownloadsApi, itemId: string, quality: string, signal?: AbortSignal,
  waitMs = 300_000, everyMs = 1500): Promise<string[]> {
  const asked = await askForDownload(api, {itemId, quality}, signal);
  const started = Date.now();
  let nudged = false;
  for (;;) {
    const download = await readDownload(api, asked.id, signal);
    const title = download.items?.find(one => one.itemId === itemId);
    if (!title) throw new Error('The server did not include this title.');
    if (title.state === 'ready') return browserSaveAddresses(api, asked.id, itemId, signal);
    // Asking again for one that was paused, or whose copy the server let go, means wanting it now.
    if (!nudged && (title.state === 'paused' || title.state === 'expired')) {
      nudged = true;
      await changeDownload(api, asked.id, title.state === 'paused' ? {paused: false} : {retry: true}, signal);
      continue;
    }
    if (!making(title)) throw new Error(title.reason || 'This title could not be prepared.');
    if (Date.now() - started > waitMs) throw new Error('download-preparation-pending');
    await pause(everyMs, signal);
  }
}

/** The words for a download quality: the ladder's own label, or a music bitrate. */
export function downloadQualityLabel(quality: string): string {
  const rung = canonicalQualityLadder.find(one => one.id === quality);
  return rung ? rung.label : /^\d+$/.test(quality) ? quality + ' kbps' : quality;
}
