import type {DownloadsByPerson} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

/**
 * Storage's Downloads part on the rebuilt server: each person's prepared downloads (titles and bytes), the copies' total
 * and the room left under the server's limit when it has one (`GET /v1/admin/downloads`, owner only).
 */
export type DownloadsUsage = DownloadsByPerson;
export function readDownloadsByPerson(api: ServerRequester, signal?: AbortSignal): Promise<DownloadsUsage> {
  return callServer(api, 'GET /v1/admin/downloads', {signal});
}

/**
 * Clear the copies the server prepared for one person (`DELETE /v1/admin/members/{id}/downloads`, owner only; downloads
 * rule 47): their requests and the files already on their devices are kept, and the server prepares again when needed.
 * Answers 'in-use' when a transfer for them is running (409 download_in_use) and nothing was changed.
 */
export async function clearPreparedCopies(api: ServerRequester, userId: string, signal?: AbortSignal): Promise<'cleared' | 'in-use'> {
  try {await callServer(api, 'DELETE /v1/admin/members/{id}/downloads', {id: userId, signal}); return 'cleared';}
  catch (error) {
    const refused = error as {status?: number; code?: string} | null;
    if (refused?.status === 409 && (refused.code === undefined || refused.code === 'download_in_use')) return 'in-use';
    throw error;
  }
}
