import {routePath} from '../../contracts/src/route-paths.ts';
import type {DownloadReceipt} from './downloads.ts';
import type {ReadingLocation} from './reading.ts';
import type {DownloadsApi} from './downloads-service.ts';

/** Return the transport's promise unchanged. Device-owned receipt checks, guards,
 * parsing and outbox reconciliation stay in their original continuation. */
export function refreshOfflineReadingProgressResponse(api: DownloadsApi, receipts: readonly DownloadReceipt[]) {
  return api.request<unknown>(routePath('post_reading_offline_progress_read'), 'POST', {receipts});
}
export function syncOfflineReadingObservationResponse(api: DownloadsApi, receipt: DownloadReceipt, observationReceipt: DownloadReceipt,
  expectedRevision: number, observedAt: number, startedAt: number, location: ReadingLocation) {
  return api.request<unknown>(routePath('post_reading_offline_progress'), 'POST', {receipt, observationReceipt, expectedRevision, observedAt, startedAt, location});
}
