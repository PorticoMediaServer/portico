/**
 * The phase-2 `V1Http` over the selected server's `HttpLocalApi` (routes, live token with refresh
 * and one 401 retry, delivery and installation headers). Playback v1 modules take this seam, so
 * wiring a platform is `new SessionsClient(localV1Http(api))`.
 */
import type {HttpLocalApi} from '../index.ts';
import type {V1Http} from './http.ts';

export function localV1Http(api: Pick<HttpLocalApi, 'requestRaw'> & Partial<Pick<HttpLocalApi, 'acknowledgeUserMutation'>>): V1Http {
  return {
    acknowledgeUserMutation: intentId => api.acknowledgeUserMutation?.(intentId) ?? Promise.resolve(),
    send: request => api.requestRaw(request.path, request.method, {headers: request.headers, body: request.body, signal: request.signal,
      ...(request.deadlineMs !== undefined ? {deadlineMs: request.deadlineMs} : {}), ...(request.singleAttempt ? {singleAttempt: true} : {})}),
  };
}
