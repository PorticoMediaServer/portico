import type {HttpLocalApi} from '@core/index.ts';
import {createAttachmentDownloadPort} from '@core/attachment-download.ts';
export type {BrowserDownloadPort, BrowserDownloadTarget} from '@core/attachment-download.ts';

let keptByApp: (() => void) | undefined;
/** The desktop app keeps downloads itself (downloads rule 3): once the server has the title ready,
 * the app's own downloads are told instead of the browser saving the file. */
export function installAppKeptDownloads(ready: () => void): () => void {
  keptByApp = ready;
  return () => { if (keptByApp === ready) keptByApp = undefined; };
}

/** The platform receives the core-owned attachment capability and lets the browser transfer bytes. */
export function createBrowserDownloadPort(api: Pick<HttpLocalApi, 'request' | 'mediaUrl'>, authorityCurrent: () => boolean, lifetime: AbortSignal) {
  return createAttachmentDownloadPort(api, authorityCurrent, lifetime, (href, filename, check) => {
    if (keptByApp) { check(); keptByApp(); return; }
    const anchor = document.createElement('a');
    anchor.href = href; anchor.download = filename; anchor.rel = 'noreferrer'; anchor.referrerPolicy = 'no-referrer';
    document.body.append(anchor);
    try {check(); anchor.click();} finally {anchor.remove();}
  });
}
