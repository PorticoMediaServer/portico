import type {DownloadOptions} from '../../contracts/src/next/api.ts';
import {askAndSave, browserDownloadOptions, downloadsOff, type BrowserDownloadsApi} from './browser-downloads.ts';

export type BrowserDownloadTarget = Readonly<{libraryId: string; itemId: string}>;
/** The choices for a title, or `allowed: false` with none when downloads are turned off for this person. */
export type BrowserDownloadChoices = DownloadOptions & Readonly<{allowed: boolean}>;
/** Capture one target object and caller lifetime per open sheet. save resolves when
 * the browser receives the file's address, not when its transfer has finished. */
export type BrowserDownloadPort = Readonly<{
  options(target: BrowserDownloadTarget, signal: AbortSignal, isCurrent: () => boolean): Promise<BrowserDownloadChoices>;
  save(target: BrowserDownloadTarget, quality: string, signal: AbortSignal, isCurrent: () => boolean): Promise<void>;
}>;

/** Save file from a title's page: the browser is handed the server's own file addresses,
 * which carry their own permission. No media blob, local store or sign-in token. A second tap
 * while one is in flight is the same save. */
export function createAttachmentDownloadPort(api: BrowserDownloadsApi, authorityCurrent: () => boolean, lifetime: AbortSignal, handoff: (href: string, check: () => void) => void): BrowserDownloadPort {
  const flights = new WeakMap<BrowserDownloadTarget, Map<string, Promise<void>>>();
  const assert = (signal: AbortSignal, current: () => boolean) => {
    if (lifetime.aborted || signal.aborted || !authorityCurrent() || !current()) throw new DOMException('Download authority changed', 'AbortError');
  };
  return {
    async options(target, signal, current) {
      assert(signal, current);
      const options = await browserDownloadOptions(api, target.itemId, AbortSignal.any([lifetime, signal])).then(value => ({...value, allowed: true}), error => {
        if (!downloadsOff(error)) throw error;
        return {itemId: target.itemId, kind: '', titles: 0, items: [], default: '', allowed: false};
      });
      assert(signal, current);
      return options;
    },
    save(target, quality, signal, current) {
      assert(signal, current);
      let perTarget = flights.get(target);
      if (!perTarget) { perTarget = new Map(); flights.set(target, perTarget); }
      const running = perTarget.get(quality);
      if (running) return running;
      const check = () => assert(signal, current);
      const flight = askAndSave(api, target.itemId, quality, AbortSignal.any([lifetime, signal])).then(addresses => {
        check();
        for (const href of addresses) handoff(href, check);
      }).finally(() => perTarget.delete(quality));
      perTarget.set(quality, flight);
      return flight;
    },
  };
}
