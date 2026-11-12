import {callServer, type ServerRequester} from '../query/server-api.ts';

/**
 * Files Portico could not prepare, across the libraries this viewer may see (console rules 1 and 72): the owner sees
 * every library's, an administrator those of the libraries they are allowed. `GET /v1/admin/skipped`, 40 a page.
 */
export type SkippedFile = Readonly<{libraryId: string; libraryName: string; path: string; reason: string; message: string}>;
export type SkippedFilesPage = Readonly<{items: readonly SkippedFile[]; nextCursor?: string}>;

const text = (v: unknown): v is string => typeof v === 'string';
export async function readSkippedFiles(api: ServerRequester, cursor?: string, signal?: AbortSignal): Promise<SkippedFilesPage> {
  const raw: unknown = await callServer(api, 'GET /v1/admin/skipped', {query: {cursor}, signal});
  const page = raw as {items?: unknown; nextCursor?: unknown} | null;
  if (!page || !Array.isArray(page.items) || page.nextCursor !== undefined && !text(page.nextCursor)) throw Object.assign(new Error('invalid_skipped_files'), {code: 'invalid_skipped_files'});
  const items = page.items.filter((f): f is SkippedFile => !!f && typeof f === 'object' && text((f as SkippedFile).path) && text((f as SkippedFile).message))
    .map(f => Object.freeze({libraryId: text(f.libraryId) ? f.libraryId : '', libraryName: text(f.libraryName) ? f.libraryName : '', path: f.path, reason: text(f.reason) ? f.reason : '', message: f.message}));
  return Object.freeze({items: Object.freeze(items), ...(page.nextCursor ? {nextCursor: page.nextCursor as string} : {})});
}
