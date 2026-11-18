import {serverPath} from './query/server-api.ts';
import type {Job} from '../../contracts/src/next/api.ts';
import {kindMetadataAgentsPath, metadataAgentPath, writeLibraryMetadataAgent} from './server-admin/metadata-agent.ts';
import type {LibraryContentApi} from './library-content.ts';
import type {OwnerRequestApi} from './owner-requests.ts';
export const readOwnerMetadataAgent = <T = unknown>(api: OwnerRequestApi, id: string, signal?: AbortSignal): Promise<T> => api.request<T>(metadataAgentPath(id), 'GET', undefined, signal);
export const writeOwnerMetadataAgent = <T = unknown>(api: LibraryContentApi, id: string, body: unknown, signal?: AbortSignal): Promise<T> => writeLibraryMetadataAgent<T>(api, id, body, signal);
/** Starts the library's missing-lyrics job. A lost answer is retried with this same header key. */
export async function fetchOwnerLibraryLyrics(api: LibraryContentApi, id: string, key: string, signal?: AbortSignal): Promise<Job> {
  if (!api.requestRaw) throw new Error('The lyrics job requires the header-capable transport.');
  const answer = await api.requestRaw(serverPath('POST /v1/jobs'), 'POST', {headers: {'Idempotency-Key': key}, body: {kind: 'fetch_lyrics', target: {library: id}}, signal});
  if (answer.status >= 400) {const error = answer.body as {code?: string; message?: string} | undefined;throw Object.assign(new Error(error?.message ?? 'The lyrics job could not start.'), {status: answer.status, code: error?.code});}
  const job = answer.body as Job | undefined;
  if (!job || typeof job.id !== 'string' || !job.id || job.id.length > 256 || /[\x00-\x1f\x7f]/.test(job.id) || job.kind !== 'fetch_lyrics' || !['waiting', 'running', 'done', 'failed', 'cancelled'].includes(job.state)) throw new Error('Invalid lyrics job response.');
  return job;
}
/** Catalogue words for the current job states; a receipt acknowledges the state returned, never completion by implication. */
export const libraryLyricsJobState = (state: string): 'queued'|'running'|'succeeded'|'failed'|'cancelled' => state === 'waiting' ? 'queued' : state === 'done' ? 'succeeded' : state as 'running'|'failed'|'cancelled';

/** Metadata choices for the original Add Library effect. Keep its omitted method,
 * promise identity and caller-owned parser/retirement ordering. */
export const readOwnerKindMetadataAgents = <T = unknown>(api: OwnerRequestApi, kind: string, signal?: AbortSignal): Promise<T> => signal
  ? api.request<T>(kindMetadataAgentsPath(kind), 'GET', undefined, signal) : api.request<T>(kindMetadataAgentsPath(kind));
