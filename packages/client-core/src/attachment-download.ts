import type {HttpLocalApi} from './index.ts';
import {downloadReasonMessage, parseDownloadOptions, type DownloadOptionsView} from './downloads.ts';
import {parseMediaItem} from './media-item.ts';
import {readAttachmentDownloadItemResponse, readDownloadOptionsResponse} from './query/download-documents.ts';
import {askForDownload, readDownload, readDownloadedItem} from './device-downloads.ts';

export type BrowserDownloadTarget = Readonly<{libraryId: string; itemId: string}>;
/** Capture one target object and caller lifetime per open sheet. save resolves when
 * the browser receives the file capability, not when its transfer has finished. */
export type BrowserDownloadPort = Readonly<{
  options(target: BrowserDownloadTarget, signal: AbortSignal, isCurrent: () => boolean): Promise<DownloadOptionsView>;
  save(target: BrowserDownloadTarget, quality: string, signal: AbortSignal, isCurrent: () => boolean): Promise<void>;
}>;
type Attempt = {target: BrowserDownloadTarget; body: Readonly<{operationId: string; itemId: string; quality: string}>;
  current: () => boolean; signal: AbortSignal; downloadId?: string; flight?: Promise<void>; submitted: boolean};

function pause(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const done = () => {clearTimeout(timer); signal.removeEventListener('abort', abort); resolve();};
    const abort = () => {clearTimeout(timer); signal.removeEventListener('abort', abort); reject(new DOMException('Download closed', 'AbortError'));};
    const timer = setTimeout(done, 1500);
    signal.addEventListener('abort', abort, {once: true});
    if (signal.aborted) abort();
  });
}

/** Ask the server to send its existing file capability as a browser attachment. */
export function browserAttachmentAddress(href: string): string {
  const url = new URL(href);
  url.searchParams.set('save', '1');
  return url.href;
}

/** Browser transfers use the server's own attachment response and opaque grant. */
export function createAttachmentDownloadPort(api: Pick<HttpLocalApi, 'request' | 'mediaUrl'>, authorityCurrent: () => boolean, lifetime: AbortSignal, handoff: (href: string, filename: string, check: () => void) => void): BrowserDownloadPort {
  const attempts = new WeakMap<BrowserDownloadTarget, Map<string, Attempt>>();
  const assert = (signal: AbortSignal, current: () => boolean) => {
    if (lifetime.aborted || signal.aborted || !authorityCurrent() || !current()) throw new DOMException('Download authority changed', 'AbortError');
  };
  const options = async (target: BrowserDownloadTarget, signal: AbortSignal, current: () => boolean) => {
    target = Object.freeze({...target});
    assert(signal, current);
    const combined = AbortSignal.any([lifetime, signal]);
    const item = parseMediaItem(await readAttachmentDownloadItemResponse(api, target.itemId, combined));
    assert(signal, current);
    if (item.id !== target.itemId || item.libraryId !== target.libraryId || !['movie', 'episode', 'song', 'audiobook_file'].includes(item.kind)) throw new Error('download-target-mismatch');
    const view = parseDownloadOptions(await readDownloadOptionsResponse(api, target.itemId, combined), target.itemId);
    assert(signal, current);
    return view;
  };
  return {options, async save(target, quality, signal, current) {
    assert(signal, current);
    let perTarget = attempts.get(target);
    if (!perTarget) {perTarget = new Map(); attempts.set(target, perTarget);}
    let attempt = perTarget.get(quality);
    if (attempt) assert(attempt.signal, attempt.current);
    if (attempt?.flight) return attempt.flight;
    if (!attempt) {
      attempt = {target: Object.freeze({...target}), body: Object.freeze({operationId: crypto.randomUUID(), itemId: target.itemId, quality}),
        signal, current, submitted: false};
      perTarget.set(quality, attempt);
    }
    const captured = attempt;
    const run = async () => {
      const target = captured.target;
      const check = () => assert(captured.signal, captured.current);
      function terminal(error: Error): never {if (perTarget.get(quality) === captured) perTarget.delete(quality); throw error;}
      const combined = AbortSignal.any([lifetime, captured.signal]);
      try {
        check();
        if (!captured.submitted) {
          const view = await options(target, captured.signal, captured.current);
          if (!view.policy.allowDownloads || !view.options.some(row => row.quality === quality && row.available)) terminal(new Error('download-quality-unavailable'));
          check(); captured.submitted = true;
        }
        if (!captured.downloadId) {
          const download = await askForDownload(api, captured.body, combined);
          check();
          if (!download.id || download.target.id !== target.itemId || download.quality !== quality) terminal(new Error('download-preparation-mismatch'));
          captured.downloadId = download.id;
        }
        const downloadId = captured.downloadId;
        const started = Date.now();
        for (;;) {
          const download = await readDownload(api, downloadId, combined);
          check();
          const title = download.items?.find(item => item.itemId === target.itemId);
          if (download.id !== downloadId || download.target.id !== target.itemId || download.quality !== quality || !title)
            terminal(new Error('download-preparation-mismatch'));
          if (title.state === 'ready' || title.state === 'received') break;
          if (title.state !== 'waiting' && title.state !== 'preparing') terminal(new Error('download-not-ready'));
          if (Date.now() - started > 300000) terminal(new Error('download-preparation-pending'));
          await pause(combined); check();
        }
        const downloaded = await readDownloadedItem(api, downloadId, target.itemId, combined);
        check();
        if (downloaded.item.id !== target.itemId || downloaded.item.libraryId !== target.libraryId || downloaded.quality !== quality)
          terminal(new Error('download-grant-mismatch'));
        if (downloaded.files.length > 1) terminal(new Error(downloadReasonMessage('several_files')));
        const file = downloaded.files[0];
        if (!file) terminal(new Error('download-not-ready'));
        handoff(browserAttachmentAddress(file.url), `${downloaded.item.title}.${file.container ?? 'bin'}`, check);
        if (perTarget.get(quality) === captured) perTarget.delete(quality);
      } finally {captured.flight = undefined;}
    };
    // Install the flight before any producer callback can yield or publish.
    captured.flight = Promise.resolve().then(run);
    return captured.flight;
  }};
}
