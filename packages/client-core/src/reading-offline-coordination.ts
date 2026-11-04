import {routePath, decodePutReadingSessionsSessionIdProgressResponse} from '../../contracts/src/index.ts';
import type {DownloadReceipt} from './downloads.ts';
import type {ReadingLocation} from './reading.ts';
import type {DownloadsApi} from './downloads-service.ts';
import {parseReadingOfflineRefresh, readingOfflineRefreshPath} from './reading-offline-refresh.ts';

/** The device keeps receipt verification and durable observations. Core sends one
 * existing batch and validates it against the exact publication bindings submitted. */
export async function refreshOfflineReadingProgress(api: DownloadsApi,
  batch: readonly Readonly<{receipt: DownloadReceipt; itemId: string; sourceId: string; sourceRevision: string}>[], current: () => boolean) {
  const raw = await api.request(readingOfflineRefreshPath, 'POST', {receipts: batch.map(value => value.receipt)});
  if (!current()) return undefined;
  return parseReadingOfflineRefresh(raw, batch.map(value => ({receiptId: value.receipt.receiptId, itemId: value.itemId, sourceId: value.sourceId, sourceRevision: value.sourceRevision})));
}
export type OfflineReadingObservation = Readonly<{receipt: DownloadReceipt; observationReceipt: DownloadReceipt;
  expectedRevision: number; observedAt: number; startedAt: number; location: ReadingLocation}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
/** Invalid or retired answers stop the device's pass, leaving its outbox untouched.
 * Its existing acknowledgement/reconciliation policy decides which local record settles. */
export async function syncOfflineReadingObservation(api: DownloadsApi, input: OfflineReadingObservation, sourceId: string, current: () => boolean) {
  const {receipt, observationReceipt, expectedRevision, observedAt, startedAt, location} = input;
  const raw = await api.request(routePath('post_reading_offline_progress'), 'POST', {receipt, observationReceipt, expectedRevision, observedAt, startedAt, location});
  if (!current() || !object(raw) || (raw.outcome !== 'applied' && raw.outcome !== 'history_paused' && raw.outcome !== 'stale_observation' && raw.outcome !== 'superseded_online')) return undefined;
  if (Object.keys(raw).some(key => key !== 'progress' && key !== 'saved' && key !== 'outcome') || !Object.prototype.hasOwnProperty.call(raw, 'progress') || typeof raw.saved !== 'boolean') return undefined;
  const result = decodePutReadingSessionsSessionIdProgressResponse({progress: raw.progress, saved: raw.saved});
  if (result.saved !== (raw.outcome === 'applied') || result.progress && (result.progress.sourceId !== sourceId || !/^[0-9a-f]{64}$/.test(result.progress.sourceRevision) || !Number.isSafeInteger(result.progress.revision) || result.progress.revision < 1 || !Number.isSafeInteger(result.progress.observedAt) || result.progress.observedAt < 1)) return undefined;
  return {outcome: raw.outcome, result};
}
