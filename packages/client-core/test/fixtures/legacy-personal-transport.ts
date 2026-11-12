// PersonalSavedService tests written against the old server's History, collection and saved view documents keep pinning the service's own
// behaviour: this transport asks the fixture with the old paths and bodies, and hands its answers through as they are.
// Only test code uses this; the app's transport is personal-saved-next.ts.
import {encodedRoutePath, routePath} from '../../../contracts/src/route-paths.ts';
import type {PersonalSavedTransport} from '../../src/personal-saved.ts';
import {resourceContentParams} from '../../src/saved.ts';

type Requester = {request<T>(p: string, m?: string, b?: unknown, s?: AbortSignal): Promise<T>};

export const legacyPersonalTransport: PersonalSavedTransport = Object.freeze({
  async history(api, _bound, period, cursor, signal) {
    const query = new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {})}); if (period) query.set('period', period);
    return (api as {request<T>(p: string, m?: string, b?: unknown, s?: AbortSignal): Promise<T>}).request<unknown>(routePath('get_personal_history') + '?' + query, 'GET', undefined, signal);
  },
  async clearHistory(api, _bound, _operationId, signal, body) {
    return (api as {request<T>(p: string, m?: string, b?: unknown, s?: AbortSignal): Promise<T>}).request<unknown>(routePath('post_personal_history_actions'), 'POST', body, signal);
  },
  async directory(api, _bound, kind, cursor, signal) {
    const query = new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {}), kind});
    return (api as Requester).request<unknown>(routePath('get_saved_resources') + '?' + query, 'GET', undefined, signal);
  },
  async resource(api, _bound, resourceId, asked, cursor, signal) {
    const query = resourceContentParams(new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {})}), asked);
    return (api as Requester).request<unknown>(encodedRoutePath('get_saved_resources_id_content', {id: encodeURIComponent(resourceId)}) + '?' + query, 'GET', undefined, signal);
  },
  async row(api, _bound, resourceId, signal) {
    return (api as Requester).request<unknown>(encodedRoutePath('get_saved_resources_id', {id: encodeURIComponent(resourceId)}), 'GET', undefined, signal);
  },
  /** The old route, method and body, as the service built them. */
  async send(api, _bound, _intent, _context, _operationId, signal, old) {
    return (api as Requester).request<unknown>(old!.path, old!.method, old!.body, signal);
  },
} as PersonalSavedTransport);
