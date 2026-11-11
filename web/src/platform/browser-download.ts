import type {HttpLocalApi} from '@core/index.ts';
import {createAttachmentDownloadPort} from '@core/attachment-download.ts';
export type {BrowserDownloadPort, BrowserDownloadTarget} from '@core/attachment-download.ts';

/** Hands the browser one address to save as a file; the server names the file. */
export function saveFromAddress(href: string, check: () => void = () => {}) {
  const anchor = document.createElement('a');
  anchor.href = href; anchor.download = ''; anchor.rel = 'noreferrer'; anchor.referrerPolicy = 'no-referrer';
  document.body.append(anchor);
  try {check(); anchor.click();} finally {anchor.remove();}
}

/** The platform receives the core-owned attachment capability and lets the browser transfer bytes. */
export function createBrowserDownloadPort(api: Pick<HttpLocalApi, 'request' | 'mediaUrl'>, authorityCurrent: () => boolean, lifetime: AbortSignal) {
  return createAttachmentDownloadPort(api, authorityCurrent, lifetime, saveFromAddress);
}
