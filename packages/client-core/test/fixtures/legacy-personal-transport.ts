// PersonalSavedService tests written against the old server's History documents keep pinning the service's own
// behaviour: this transport asks the fixture with the old paths and bodies, and hands its answers through as they are.
// Only test code uses this; the app's transport is personal-saved-next.ts.
import {routePath} from '../../../contracts/src/route-paths.ts';
import type {PersonalSavedTransport} from '../../src/personal-saved.ts';

export const legacyPersonalTransport: PersonalSavedTransport = Object.freeze({
  async history(api, _bound, period, cursor, signal) {
    const query = new URLSearchParams({limit: '40', ...(cursor ? {cursor} : {})}); if (period) query.set('period', period);
    return (api as {request<T>(p: string, m?: string, b?: unknown, s?: AbortSignal): Promise<T>}).request<unknown>(routePath('get_personal_history') + '?' + query, 'GET', undefined, signal);
  },
  async clearHistory(api, _bound, _operationId, signal, body) {
    return (api as {request<T>(p: string, m?: string, b?: unknown, s?: AbortSignal): Promise<T>}).request<unknown>(routePath('post_personal_history_actions'), 'POST', body, signal);
  },
} as PersonalSavedTransport);
