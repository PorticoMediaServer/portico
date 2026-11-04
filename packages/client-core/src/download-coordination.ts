export {readDownloadPreparation, readDownloadOptions, readOfflineDownloadBundle, readDownloadReceiptKeys, readDownloadRevocations} from './query/download-documents.ts';
import {encodedRoutePath, routePath} from '../../contracts/src/route-paths.ts';
import {parseDownloadBatch, parseDownloadGrant,
  parseDeferredProgressReceipt, parseReceiptOutcomes,
  type DeferredProgressEntry} from './downloads.ts';
import {durationIntent} from './wire-units.ts';
import type {DownloadsApi} from './downloads-service.ts';

/** Server coordination only. The caller retains its captured transport, intent IDs and
 * lifetime; the platform owns files, receipt signatures, transfers and durable outboxes. */
export type DownloadPreparationIntent = Readonly<{operationId: string; mediaId?: string; versionId?: string; quality: string}>;
/** The caller supplies the stable id of this intent; no implicit retry or list refresh. */
export async function prepareItemDownload(api: DownloadsApi, intent: DownloadPreparationIntent, signal?: AbortSignal, received?: () => void) {
  const {operationId, mediaId, versionId, quality} = intent;
  const raw = await api.request(routePath('create_download_preparations'), 'POST',
    {operationId, mediaId, ...(versionId === undefined ? {} : {versionId}), quality}, signal);
  // Native's intent settles on a transport answer, even if its DTO is unreadable.
  received?.();
  return parseDownloadBatch(raw);
}
export async function grantPreparedDownload(api: DownloadsApi, id: string, operationId: string, signal?: AbortSignal) {
  return parseDownloadGrant(await api.request(encodedRoutePath('create_download_grant', {id: encodeURIComponent(id)}), 'POST', {operationId}, signal));
}
export async function issueDownloadReceipts(api: DownloadsApi, operationId: string, preparationIds: readonly string[], signal?: AbortSignal) {
  return parseReceiptOutcomes(await api.request(routePath('issue_download_receipts'), 'POST', {operationId, preparationIds: [...preparationIds]}, signal));
}
export async function revalidateDownloadReceipts(api: DownloadsApi, operationId: string, receiptIds: readonly string[], signal?: AbortSignal) {
  return parseReceiptOutcomes(await api.request(routePath('revalidate_download_receipts'), 'POST', {operationId, receiptIds: [...receiptIds]}, signal));
}
export async function revokeRemovedDownloadReceipt(api: DownloadsApi, operationId: string, receiptId: string) {
  await api.request(routePath('revoke_download_receipts'), 'POST', {operationId, receiptIds: [receiptId], reason: 'removed'});
}
/** Native outboxes store seconds. Desktop stores milliseconds and keeps its exact
 * timestamps; both submit the existing wire DTO, once, under the supplied intent ID. */
export async function syncOfflineDownloadPlays(api: DownloadsApi, operationId: string, entries: readonly DeferredProgressEntry[]) {
  return parseDeferredProgressReceipt(await api.request(routePath('sync_download_progress'), 'POST',
    {operationId, entries: entries.map(entry => durationIntent(entry, {positionSeconds: 'positionMs'}))}));
}
export async function syncDesktopDownloadPlays(api: DownloadsApi, operationId: string,
  plays: readonly Readonly<{playId: string; itemId: string; positionMs: number; ended: boolean; observedMs: number}>[], signal?: AbortSignal) {
  return parseDeferredProgressReceipt(await api.request(routePath('sync_download_progress'), 'POST', {operationId,
    entries: plays.map(v => ({playId: v.playId, itemId: v.itemId, positionMs: v.positionMs, ended: v.ended, observedAt: new Date(v.observedMs).toISOString()}))}, signal));
}
