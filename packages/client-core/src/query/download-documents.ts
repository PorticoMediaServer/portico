import {encodedRoutePath, routePath} from '../../../contracts/src/route-paths.ts';
import type {DownloadsApi} from '../downloads-service.ts';

/** Named, uncached document operations return the captured transport promise itself.
 * Callers keep their original settlement/guard/parser sequence; these operations add no
 * async boundary, validation, retry or error reporting before transport settlement. */
export function readAttachmentDownloadItemResponse(api: DownloadsApi, itemId: string, signal: AbortSignal) {
  return api.request<unknown>(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, signal);
}
export function readDownloadPreparationResponse(api: DownloadsApi, id: string, signal?: AbortSignal) {
  return api.request<unknown>(encodedRoutePath('get_download_preparation', {id: encodeURIComponent(id)}), 'GET', undefined, signal);
}
export function readDownloadOptionsResponse(api: DownloadsApi, itemId: string, signal?: AbortSignal, versionId?: string) {
  const path = encodedRoutePath('get_download_options', {itemId: encodeURIComponent(itemId)})
    + (versionId === undefined ? '' : `?versionId=${encodeURIComponent(versionId)}`);
  return api.request<unknown>(path, 'GET', undefined, signal);
}
export function readOfflineDownloadBundleResponse(api: DownloadsApi, id: string) {
  return api.request<unknown>(encodedRoutePath('get_download_bundle', {id: encodeURIComponent(id)}));
}
export function readDownloadReceiptKeysResponse(api: DownloadsApi, ...signals: [] | [AbortSignal]) {
  return signals.length ? api.request<unknown>(routePath('list_download_receipt_keys'), 'GET', undefined, signals[0])
    : api.request<unknown>(routePath('list_download_receipt_keys'));
}
export function readDownloadRevocationsResponse(api: DownloadsApi, cursor: string, signal?: AbortSignal) {
  return api.request<unknown>(routePath('list_download_revocations') + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''), 'GET', undefined, signal);
}
