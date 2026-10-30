import type {LibraryContentApi} from '@core/library-content.ts';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes.ts';
import type {MessageId} from '@i18n';

export type SkippedFile = Readonly<{sequence: number; path: string; reason: MessageId}>;
export type SkippedPage = Readonly<{items: readonly SkippedFile[]; count: number; hasMore: boolean; next: number}>;
const reasons: Readonly<Record<string, MessageId>> = {
  file_truncated: 'scan.issue.file_truncated', unfinished_download: 'scan.issue.unfinished_download',
};
const categories: Readonly<Record<string, MessageId>> = {
  damaged: 'scan.issue.damaged', unsupported: 'scan.issue.unsupported', 'not-media': 'scan.issue.not_media',
};
const integer = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) >= 0;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
/** Only the server's unplayable-file page is shown; unmatched titles never enter this list. */
export async function skippedFiles(api: LibraryContentApi, libraryId: string, jobId: string, after: number, signal: AbortSignal): Promise<SkippedPage> {
  const path = encodedRoutePath('get_scan_issues', {id: encodeURIComponent(libraryId), job: encodeURIComponent(jobId)});
  const raw: unknown = await api.request(path + '?' + new URLSearchParams({after: String(after), limit: '40'}), 'GET', undefined, signal);
  if (!object(raw) || raw.jobId !== jobId || !Array.isArray(raw.items) || raw.items.length > 40 || !integer(raw.count) ||
      typeof raw.hasMore !== 'boolean' || !integer(raw.nextSequence) || raw.nextSequence < after || (raw.hasMore && raw.nextSequence <= after)) throw new Error('invalid_skipped_files');
  const items: SkippedFile[] = [];
  let previous = after;
  for (const row of raw.items) {
    if (!object(row) || !integer(row.sequence) || row.sequence <= previous || row.sequence > raw.nextSequence || typeof row.path !== 'string' ||
        row.path.length > 8192 || /[\x00-\x1f\x7f]/.test(row.path) || typeof row.category !== 'string' || !Object.hasOwn(categories, row.category)) throw new Error('invalid_skipped_file');
    previous = row.sequence;
    items.push({sequence: row.sequence, path: row.path, reason: typeof row.reason === 'string' && Object.hasOwn(reasons, row.reason) ? reasons[row.reason] : categories[row.category]});
  }
  return {items, count: raw.count, hasMore: raw.hasMore, next: raw.nextSequence};
}
