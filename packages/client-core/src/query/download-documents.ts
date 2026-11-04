import {encodedRoutePath, routePath} from '../../../contracts/src/route-paths.ts';
import {parseDownloadBundle, parseDownloadOptions, parseDownloadPreparation, parseReceiptKeys, parseRevocationPage} from '../downloads.ts';
import {parseMediaItem} from '../media-item.ts';
import type {DownloadsApi} from '../downloads-service.ts';

/** Captured download documents: preserve the caller's explicit lifetime and uncached
 * preparation/receipt freshness. These reads never create another list owner. */
export async function readAttachmentDownloadItem(api: DownloadsApi, itemId: string, signal: AbortSignal) {
  return parseMediaItem(await api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, signal));
}

export async function readDownloadPreparation(api: DownloadsApi, id: string, signal?: AbortSignal) {
  return parseDownloadPreparation(await api.request(encodedRoutePath('get_download_preparation', {id: encodeURIComponent(id)}), 'GET', undefined, signal));
}

export async function readDownloadOptions(api: DownloadsApi, itemId: string, signal?: AbortSignal, versionId?: string) {
  const path = encodedRoutePath('get_download_options', {itemId: encodeURIComponent(itemId)})
    + (versionId === undefined ? '' : `?versionId=${encodeURIComponent(versionId)}`);
  return parseDownloadOptions(await api.request(path, 'GET', undefined, signal), itemId);
}

export async function readOfflineDownloadBundle(api: DownloadsApi, id: string) {
  const raw = await api.request<unknown>(encodedRoutePath('get_download_bundle', {id: encodeURIComponent(id)}));
  const bundle = parseDownloadBundle(raw, id);
  const publication = raw && typeof raw === 'object' && 'publication' in raw && typeof raw.publication === 'string' ? raw.publication : undefined;
  return {bundle, publication};
}

export async function readDownloadReceiptKeys(api: DownloadsApi, signal?: AbortSignal) {
  return parseReceiptKeys(await api.request(routePath('list_download_receipt_keys'), 'GET', undefined, signal));
}

export async function readDownloadRevocations(api: DownloadsApi, cursor: string, signal?: AbortSignal) {
  return parseRevocationPage(await api.request(routePath('list_download_revocations') + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''), 'GET', undefined, signal));
}
