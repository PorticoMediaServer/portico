import type {HttpLocalApi} from '@core/index.ts';
import {createAttachmentDownloadPort} from '@core/attachment-download.ts';
export type {BrowserDownloadPort, BrowserDownloadTarget} from '@core/attachment-download.ts';

/** The platform receives the core-owned attachment capability and lets the browser transfer bytes. */
export function createBrowserDownloadPort(api: Pick<HttpLocalApi, 'request' | 'mediaUrl'>, authorityCurrent: () => boolean, lifetime: AbortSignal) {
  return createAttachmentDownloadPort(api, authorityCurrent, lifetime, (href, filename, check) => {
    const anchor = document.createElement('a');
    anchor.href = href; anchor.download = filename; anchor.rel = 'noreferrer'; anchor.referrerPolicy = 'no-referrer';
    document.body.append(anchor);
    try {check(); anchor.click();} finally {anchor.remove();}
  });
}
