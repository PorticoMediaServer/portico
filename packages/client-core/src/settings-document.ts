import type {HttpLocalApi} from './index.ts';
import type {RequestRaw} from './home-selection/list-destinations.ts';
import {serverPath} from './query/server-api.ts';
import {CodedError, unreadableServerResponse} from './server-messages.ts';
import {isStaleAnswer} from './freshness-actions.ts';

export type SettingsDocumentAPI = Pick<HttpLocalApi, 'request'> & Partial<{requestRaw: RequestRaw}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
export const settingsSaveConflict = (error: unknown): boolean => isStaleAnswer(error) || object(error) && error.code === 'changed_elsewhere';

/** Every settings-document save carries the revision the person edited. Preserve refusals for the row's error line. */
export async function putSettingsDocument(api: SettingsDocumentAPI, name: string, etag: string, change: Readonly<Record<string, unknown>>, signal?: AbortSignal): Promise<unknown> {
  if (!api.requestRaw) throw new CodedError('unsupported', unreadableServerResponse);
  if (!/^"[0-9]+"$/.test(etag)) throw new Error(unreadableServerResponse);
  const answer = await api.requestRaw(serverPath('PUT /v1/admin/settings/{name}', {id: name}), 'PUT',
    {headers: {'If-Match': etag}, body: change, signal});
  if (answer.status >= 400) {
    const error = object(answer.body) && object(answer.body.error) ? answer.body.error : {};
    throw Object.assign(new Error(typeof error.message === 'string' ? error.message : unreadableServerResponse), {status: answer.status, code: error.code});
  }
  return answer.body;
}
