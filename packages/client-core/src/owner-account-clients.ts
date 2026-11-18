import type {KeyCreate, KeyCreated} from '../../contracts/src/next/api.ts';
import type {HttpLocalApi} from './http.ts';
import {call} from './playback-v1/http.ts';
import {callServer, serverPath, type ServerRequester} from './query/server-api.ts';
import {unreadableServerResponse} from './server-messages.ts';

/** Personal token operations use the signed-in account's original Settings scope. */
export function ownerAccessTokenApi(scope: ServerRequester & {within<T>(work: (signal: AbortSignal) => Promise<T>, signal?: AbortSignal): Promise<T>},
  http: Pick<HttpLocalApi, 'requestRaw'>) {
  return {
    list: async (signal?: AbortSignal) => {
      const page = await callServer(scope, 'GET /v1/me/tokens', {signal});
      if (typeof page?.tokensAllowed !== 'boolean' || !Array.isArray(page.items)) throw new Error(unreadableServerResponse);
      return page;
    },
    create: async (operationId: string, body: KeyCreate, signal?: AbortSignal) => {
      const answer = await call({send: ({path, method, signal, ...options}) =>
        scope.within(next => http.requestRaw(path, method, {...options, signal: next}), signal)}, {
        method: 'POST', path: serverPath('POST /v1/me/tokens'), body, headers: {'Idempotency-Key': operationId}, signal,
      });
      const {secret, note} = (answer.body as KeyCreated | undefined) ?? {};
      if (typeof secret === 'string' && secret.startsWith('pk_')) return {secret};
      if (secret === undefined && typeof note === 'string') return {};
      throw new Error(unreadableServerResponse);
    },
    revoke: (id: string, signal?: AbortSignal) => callServer(scope, 'DELETE /v1/me/tokens/{id}', {id, signal}),
  };
}
