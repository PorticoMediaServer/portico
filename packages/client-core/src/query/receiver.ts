import {routeBody, routePath, routePattern, encodedRoutePath} from '../../../contracts/src/routes.ts';
import type {ItemResponse} from '../media-item.ts';
import {parseMediaItem} from '../media-item.ts';

type ReceiverApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

/** Display facts remain uncached and belong to the caller's captured playback authority. */
export function readReceiverItem<T = ItemResponse>(api: ReceiverApi, id: string, method?: 'GET', signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePattern('get_items_id').replace('{id}', encodeURIComponent(id)), method, undefined, signal);
}
/** Native Together already refused invalid route IDs before transport. Return the
 * raw promise directly: its caller keeps parsing before its original currentness check. */
export function readTogetherPlaybackItem(api: ReceiverApi, id: string, signal?: AbortSignal): Promise<unknown> {
  return api.request<unknown>(encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined, signal);
}
export async function readTogetherItem(api: ReceiverApi, id: string, signal?: AbortSignal): Promise<ItemResponse> {
  const raw = await api.request<ItemResponse>(encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined, signal);
  if (parseMediaItem(raw).id !== id) throw new Error('unexpected_group_item');
  return raw;
}
export function readCastConfiguration(api: ReceiverApi): Promise<{applicationId?: unknown}> {
  return api.request(routePath('social_get_v1_cast_configuration'));
}

/** The existing phone-side Quick Connect review, with its original bounded projection. */
export function receiverLinkOperations(api: ReceiverApi) {
  const text = (value: unknown, max: number): string | undefined => typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined;
  return {
    async preview(code: string, life?: Readonly<{signal: AbortSignal}>) {
      const raw = await api.request<{requestId?: unknown; userCode?: unknown; deviceName?: unknown; platform?: unknown}>(
        routePath('post_quick_connect_review'), 'POST', routeBody('post_quick_connect_review', {userCode: code.trim().toUpperCase()}), life?.signal);
      const requestId = text(raw?.requestId, 128), userCode = text(raw?.userCode, 32), deviceName = text(raw?.deviceName, 64);
      if (!requestId || !userCode || !deviceName) throw new Error('quick_connect_unreadable');
      const platform = text(raw?.platform, 32);
      return Object.freeze({requestId, userCode, deviceName, ...(platform ? {platform} : {})});
    },
    async decide(requestId: string, code: string, decision: 'approve' | 'deny', life?: Readonly<{signal: AbortSignal}>): Promise<void> {
      await api.request(routePath('post_quick_connect_decision'), 'POST',
        routeBody('post_quick_connect_decision', {requestId, userCode: code.trim().toUpperCase(), decision}), life?.signal);
    },
  };
}

/** GroupSessionService supplies the scoped path and cursor; preserve the original stream auth. */
export function openTogetherStream(api: {baseUrl: string; routeFetch: typeof fetch}, token: string, path: string, lastEventId: string | undefined, signal: AbortSignal): Promise<Response> {
  const url = new URL(path, api.baseUrl);
  if (!path.startsWith(routePath('social_get_v1_groups') + '/') || url.origin !== new URL(api.baseUrl).origin) throw new Error('invalid_group_stream');
  return api.routeFetch(url.href, {signal, headers: {Accept: 'text/event-stream', Authorization: `Bearer ${token}`,
    ...(lastEventId ? {'Last-Event-ID': lastEventId} : {})}});
}
