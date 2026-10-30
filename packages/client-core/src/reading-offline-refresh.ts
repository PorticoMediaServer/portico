import {decodePostReadingOfflineProgressReadResponse, routePath, type PostReadingOfflineProgressReadResponse} from '../../contracts/src/index.ts';

/** Inbound refresh of downloaded books' saved positions
 * (`POST /v1/reading/offline/progress/read`). The read opens no session and
 * writes nothing on the server. Each submitted receipt gets one result:
 * `current` names the copy's exact publication with the viewer's saved position
 * on it (`progress: null` is confirmed absence), and `refused` carries a stable
 * code: `receipt_rejected` (authority lost) or `source_unavailable` (the server's
 * source for the copy is missing or replaced). */
export const readingOfflineRefreshPath = routePath('post_reading_offline_progress_read');
export const readingOfflineRefreshLimit = 50;

type Result = PostReadingOfflineProgressReadResponse['results'][number];
export type ReadingOfflineRefreshCurrent = Extract<Result, {itemId: string}>;
export type ReadingOfflineRefreshResult = Readonly<{receiptId: string; outcome: 'current'; itemId: string; sourceId: string; sourceRevision: string; progress: ReadingOfflineRefreshCurrent['progress']}>
  | Readonly<{receiptId: string; outcome: 'refused'; code: 'receipt_rejected' | 'source_unavailable'}>;

function invalid(): never {throw Object.assign(new Error('invalid_offline_refresh'), {code: 'invalid_document'});}

/** Validates a refresh answer against the exact receipts sent, in order. */
export function parseReadingOfflineRefresh(raw: unknown, sent: readonly Readonly<{receiptId: string; itemId: string; sourceId: string; sourceRevision: string}>[]): readonly ReadingOfflineRefreshResult[] {
  let decoded: PostReadingOfflineProgressReadResponse;
  try {decoded = decodePostReadingOfflineProgressReadResponse(raw);} catch {invalid();}
  if (decoded.results.length !== sent.length) invalid();
  return Object.freeze(decoded.results.map((result, i): ReadingOfflineRefreshResult => {
    const expected = sent[i]!;
    if (result.receiptId !== expected.receiptId) invalid();
    if (result.outcome === 'refused' && 'code' in result) {
      if (result.code !== 'receipt_rejected' && result.code !== 'source_unavailable') invalid();
      return Object.freeze({receiptId: result.receiptId, outcome: 'refused', code: result.code});
    }
    if (result.outcome !== 'current' || !('itemId' in result) || result.itemId !== expected.itemId || result.sourceId !== expected.sourceId || result.sourceRevision !== expected.sourceRevision) invalid();
    const progress = result.progress;
    if (progress !== null && (progress.sourceId !== expected.sourceId || progress.stale !== (progress.sourceRevision !== expected.sourceRevision))) invalid();
    return Object.freeze({receiptId: result.receiptId, outcome: 'current', itemId: result.itemId, sourceId: result.sourceId, sourceRevision: result.sourceRevision, progress});
  }));
}
