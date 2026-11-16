import {serverPath} from './query/server-api.ts';
import type {SavedApi} from './saved.ts';
/** Clear this profile's history once; the caller keeps its confirmation and lifetime guard. */
export function clearPersonalHistory(api: SavedApi, _operationId: string, _expectedRevision: number, signal: AbortSignal): Promise<void> {
  return api.request<void>(serverPath('DELETE /v1/history'), 'DELETE', undefined, signal);
}
