import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import {routeBody} from '../../contracts/src/routes.ts';
import {kindMetadataAgentsPath, metadataAgentPath} from './server-admin/metadata-agent.ts';
import type {OwnerRequestApi} from './owner-requests.ts';
export const readOwnerMetadataAgent = <T = unknown>(api: OwnerRequestApi, id: string, signal?: AbortSignal): Promise<T> => api.request<T>(metadataAgentPath(id), 'GET', undefined, signal);
export const writeOwnerMetadataAgent = <T = unknown>(api: OwnerRequestApi, id: string, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(metadataAgentPath(id), 'PUT', body, signal);
export const readOwnerSkippedFiles = <T = unknown>(api: OwnerRequestApi, id: string, job: string, after: number, signal: AbortSignal): Promise<T> => {
  const path = encodedRoutePath('get_scan_issues', {id: encodeURIComponent(id), job: encodeURIComponent(job)});
  return api.request<T>(path + '?' + new URLSearchParams({after: String(after), limit: '40'}), 'GET', undefined, signal);
};
export const fetchOwnerLibraryLyrics = <T = unknown>(api: OwnerRequestApi, id: string, key: string, signal?: AbortSignal): Promise<T> => api.request<T>(encodedRoutePath('post_libraries_id_lyrics_fetch', {id: encodeURIComponent(id)}), 'POST', {idempotencyKey: key}, signal);
export const fetchOwnerLibraryLyricsContract = <T = unknown>(api: OwnerRequestApi, id: string, key: string): Promise<T> => api.request<T>(encodedRoutePath('post_libraries_id_lyrics_fetch', {id: encodeURIComponent(id)}), 'POST', routeBody('post_libraries_id_lyrics_fetch', {idempotencyKey: key}));

/** Metadata choices for the original Add Library effect. Keep its omitted method,
 * promise identity and caller-owned parser/retirement ordering. */
export const readOwnerKindMetadataAgents = <T = unknown>(api: OwnerRequestApi, kind: string): Promise<T> => api.request<T>(kindMetadataAgentsPath(kind));
