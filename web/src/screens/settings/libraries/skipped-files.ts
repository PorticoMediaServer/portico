import {readLibrarySkippedFiles} from '@core/admin/skipped-files.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {MessageId} from '@i18n';
/** `reason` is the catalogue's words when this app knows the server's reason; otherwise the server's own `message` is shown. */
export type SkippedFile = Readonly<{path: string; reason?: MessageId; message: string}>;
export type SkippedPage = Readonly<{items: readonly SkippedFile[]; hasMore: boolean; next: string}>;
const reasons: Readonly<Record<string, MessageId>> = {
  damaged: 'scan.issue.damaged', not_supported: 'scan.issue.unsupported', not_media: 'scan.issue.not_media',
};
/** One page of the library's files Portico could not prepare (`GET /v1/admin/libraries/{id}/skipped`, 40 a page). */
export async function skippedFiles(api: LibraryContentApi, libraryId: string, cursor: string, signal: AbortSignal): Promise<SkippedPage> {
  const page = await readLibrarySkippedFiles(api, libraryId, cursor || undefined, signal);
  return {items: page.items.map(file => ({path: file.path, ...(Object.hasOwn(reasons, file.reason) ? {reason: reasons[file.reason]} : {}), message: file.message})),
    hasMore: !!page.nextCursor, next: page.nextCursor ?? ''};
}
