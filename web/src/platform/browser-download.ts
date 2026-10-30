import type {HttpLocalApi} from '@core/index.ts';
import {parseMediaItem} from '@core/media-item.ts';
import {parseDownloadBatch, parseDownloadGrant, parseDownloadOptions, parseDownloadPreparation, type DownloadOptionsView, type DownloadPreparation} from '@core/downloads.ts';
import {encodedRoutePath, routePath} from '../../../packages/contracts/src/routes.ts';

export type BrowserDownloadTarget = Readonly<{libraryId: string; itemId: string}>;
/** Capture one target object and caller lifetime per open sheet. save resolves when
 * the browser receives the file capability, not when its transfer has finished. */
export type BrowserDownloadPort = Readonly<{
  options(target: BrowserDownloadTarget, signal: AbortSignal, isCurrent: () => boolean): Promise<DownloadOptionsView>;
  save(target: BrowserDownloadTarget, quality: string, signal: AbortSignal, isCurrent: () => boolean): Promise<void>;
}>;
type Attempt = {target: BrowserDownloadTarget; body: Readonly<{operationId: string; mediaId: string; quality: string}>; grantId: string;
  current: () => boolean; signal: AbortSignal; preparation?: DownloadPreparation; flight?: Promise<void>; submitted: boolean};

function pause(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const done = () => {clearTimeout(timer); signal.removeEventListener('abort', abort); resolve();};
    const abort = () => {clearTimeout(timer); signal.removeEventListener('abort', abort); reject(new DOMException('Download closed', 'AbortError'));};
    const timer = setTimeout(done, 1500);
    signal.addEventListener('abort', abort, {once: true});
    if (signal.aborted) abort();
  });
}

/** Browser transfers use the server's own attachment response and opaque grant.
 * No media blob, local offline store, authentication token or invented URL. */
export function createBrowserDownloadPort(api: Pick<HttpLocalApi, 'request' | 'mediaUrl'>, authorityCurrent: () => boolean, lifetime: AbortSignal): BrowserDownloadPort {
  const attempts = new WeakMap<BrowserDownloadTarget, Map<string, Attempt>>();
  const assert = (signal: AbortSignal, current: () => boolean) => {
    if (lifetime.aborted || signal.aborted || !authorityCurrent() || !current()) throw new DOMException('Download authority changed', 'AbortError');
  };
  const options = async (target: BrowserDownloadTarget, signal: AbortSignal, current: () => boolean) => {
    target = Object.freeze({...target});
    assert(signal, current);
    const combined = AbortSignal.any([lifetime, signal]);
    const item = parseMediaItem(await api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(target.itemId)}), 'GET', undefined, combined));
    assert(signal, current);
    if (item.id !== target.itemId || item.libraryId !== target.libraryId || !['movie', 'episode', 'song', 'audiobook_file'].includes(item.kind)) throw new Error('download-target-mismatch');
    const view = parseDownloadOptions(await api.request(encodedRoutePath('get_download_options', {itemId: encodeURIComponent(target.itemId)}), 'GET', undefined, combined), target.itemId);
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
      attempt = {target: Object.freeze({...target}), body: Object.freeze({operationId: crypto.randomUUID(), mediaId: target.itemId, quality}), grantId: crypto.randomUUID(),
        signal, current, submitted: false};
      perTarget.set(quality, attempt);
    }
    const captured = attempt;
    const run = async () => {
      const target = captured.target;
      const check = () => assert(captured.signal, captured.current);
      const combined = AbortSignal.any([lifetime, captured.signal]);
      try {
        check();
        if (!captured.submitted) {
          const view = await options(target, captured.signal, captured.current);
          if (!view.policy.allowDownloads || !view.options.some(row => row.quality === quality && row.available)) throw new Error('download-quality-unavailable');
          check(); captured.submitted = true;
        }
        if (!captured.preparation) {
          const batch = parseDownloadBatch(await api.request(routePath('create_download_preparations'), 'POST', captured.body, combined));
          check();
          if (batch.items.length !== 1 || batch.accepted !== 1 || batch.rejected.length) throw new Error('download-preparation-unconfirmed');
          captured.preparation = batch.items[0];
        }
        const validate = (value: DownloadPreparation) => {
          if (value.itemId !== target.itemId || value.libraryId !== target.libraryId || value.quality !== quality) throw new Error('download-preparation-mismatch');
          return value;
        };
        let preparation = validate(captured.preparation);
        const started = Date.now();
        while (preparation.state === 'queued' || preparation.state === 'running') {
          check();
          if (Date.now() - started > 300000) throw new Error('download-preparation-pending');
          await pause(combined); check();
          const next = parseDownloadPreparation(await api.request(encodedRoutePath('get_download_preparation', {id: encodeURIComponent(preparation.id)}), 'GET', undefined, combined));
          check();
          if (next.id !== preparation.id) throw new Error('download-preparation-mismatch');
          captured.preparation = preparation = validate(next);
        }
        if (preparation.state !== 'ready') throw new Error('download-not-ready');
        const grant = parseDownloadGrant(await api.request(encodedRoutePath('create_download_grant', {id: encodeURIComponent(preparation.id)}), 'POST', {operationId: captured.grantId}, combined));
        check();
        if (grant.preparationId !== preparation.id || grant.itemId !== target.itemId || grant.artifact.sha256 !== preparation.artifact.sha256
          || grant.artifact.bytes !== preparation.artifact.bytes || Date.parse(grant.expiresAt) <= Date.now()) throw new Error('download-grant-mismatch');
        const href = api.mediaUrl(grant.url); check();
        const anchor = document.createElement('a');
        anchor.href = href; anchor.download = grant.artifact.fileName; anchor.rel = 'noreferrer'; anchor.referrerPolicy = 'no-referrer';
        document.body.append(anchor);
        try {check(); anchor.click();} finally {anchor.remove();}
        perTarget.delete(quality);
      } finally {captured.flight = undefined;}
    };
    // Install the flight before any producer callback can yield or publish.
    captured.flight = Promise.resolve().then(run);
    return captured.flight;
  }};
}
