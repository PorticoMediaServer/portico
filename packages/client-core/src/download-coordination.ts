export {readDownloadPreparationResponse, readDownloadOptionsResponse, readOfflineDownloadBundleResponse, readDownloadReceiptKeysResponse, readDownloadRevocationsResponse} from './query/download-documents.ts';
import {encodedRoutePath, routePath} from '../../contracts/src/route-paths.ts';
import {durationIntent} from './wire-units.ts';
import type {DeferredProgressEntry} from './downloads.ts';
import type {DownloadsApi} from './downloads-service.ts';

/** Server coordination only. Return the exact transport promise: caller-owned core
 * parsers, settlement, cancellation and local native bookkeeping retain their order. */
export type DownloadPreparationIntent = Readonly<{operationId: string; mediaId?: string; versionId?: string; quality: string}>;
export function prepareItemDownloadResponse(api: DownloadsApi, intent: DownloadPreparationIntent, signal?: AbortSignal) {
  const {operationId, mediaId, versionId, quality} = intent;
  return api.request<unknown>(routePath('create_download_preparations'), 'POST',
    {operationId, mediaId, ...(versionId === undefined ? {} : {versionId}), quality}, signal);
}
export function grantPreparedDownloadResponse(api: DownloadsApi, id: string, operationId: string, signal?: AbortSignal) {
  return api.request<unknown>(encodedRoutePath('create_download_grant', {id: encodeURIComponent(id)}), 'POST', {operationId}, signal);
}
export function issueDownloadReceiptsResponse(api: DownloadsApi, operationId: string, preparationIds: readonly string[], ...signals: [] | [AbortSignal]) {
  const body = {operationId, preparationIds: [...preparationIds]};
  return signals.length ? api.request<unknown>(routePath('issue_download_receipts'), 'POST', body, signals[0])
    : api.request<unknown>(routePath('issue_download_receipts'), 'POST', body);
}
export function revalidateDownloadReceiptsResponse(api: DownloadsApi, operationId: string, receiptIds: readonly string[]) {
  return api.request<unknown>(routePath('revalidate_download_receipts'), 'POST', {operationId, receiptIds: [...receiptIds]});
}
export function revokeRemovedDownloadReceipt(api: DownloadsApi, operationId: string, receiptId: string) {
  return api.request<unknown>(routePath('revoke_download_receipts'), 'POST', {operationId, receiptIds: [receiptId], reason: 'removed'});
}
export function syncOfflineDownloadPlaysResponse(api: DownloadsApi, operationId: string, entries: readonly DeferredProgressEntry[]) {
  return api.request<unknown>(routePath('sync_download_progress'), 'POST',
    {operationId, entries: entries.map(entry => durationIntent(entry, {positionSeconds: 'positionMs'}))});
}
export function syncDesktopDownloadPlaysResponse(api: DownloadsApi, operationId: string,
  plays: readonly Readonly<{playId: string; itemId: string; positionMs: number; ended: boolean; observedMs: number}>[], signal?: AbortSignal) {
  return api.request<unknown>(routePath('sync_download_progress'), 'POST', {operationId,
    entries: plays.map(v => ({playId: v.playId, itemId: v.itemId, positionMs: v.positionMs, ended: v.ended, observedAt: new Date(v.observedMs).toISOString()}))}, signal);
}
