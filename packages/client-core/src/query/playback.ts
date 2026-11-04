import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {ItemResponse} from '../media-item.ts';
import type {V1Http} from '../playback-v1/http.ts';
import {call, enc} from '../playback-v1/http.ts';
import {decodeOpen} from '../../../contracts/src/runtime.ts';
import type {GetPlaybackSessionResponse} from '../../../contracts/src/types.ts';

type PlaybackApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
export function readPlaybackCapabilities(api: PlaybackApi, signal?: AbortSignal): Promise<unknown> {
  return api.request(routePath('get_capabilities'), 'GET', undefined, signal);
}
export function readPlaybackItem(api: PlaybackApi, id: string, signal?: AbortSignal): Promise<ItemResponse> {
  return api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined, signal);
}
/** The admitted subtitle plan supplies the exact font/line document capability. */
export function readPlaybackSubtitleFonts(api: PlaybackApi, path: string, signal?: AbortSignal): Promise<unknown> {
  return api.request(path, 'GET', undefined, signal);
}
export function readPlaybackSubtitleLines(api: PlaybackApi, path: string, signal?: AbortSignal): Promise<unknown> {
  return api.request(path, 'GET', undefined, signal);
}
/** Preserve the original guide query's ordering, including its explicit empty cursor. */
export function readPlaybackGuide(api: PlaybackApi, query: URLSearchParams, signal?: AbortSignal): Promise<unknown> {
  return api.request(`${routePath('get_guide')}?${query}`, 'GET', undefined, signal);
}
export async function readPreparingChannel(http: V1Http, id: string, signal?: AbortSignal): Promise<GetPlaybackSessionResponse> {
  const response = await call(http, {method: 'GET', path: encodedRoutePath('get_playback_session', {id: enc(id)}), signal});
  return decodeOpen('GetPlaybackSessionResponse', response.body) as GetPlaybackSessionResponse;
}
/** Page teardown must keep using the captured API's keepalive path, without waiting. */
export function farewellPlaybackSession(farewell: (path: string, method: string, body?: unknown) => void, id: string, generation: number, positionMs?: number): void {
  farewell(encodedRoutePath('stop_playback_session', {id: encodeURIComponent(id)}), 'DELETE', {generation,
    ...(positionMs !== undefined ? {positionMs: Math.max(0, Math.round(positionMs))} : {})});
}

/** Bounded native/Vega transport consumes the group service's path without owning auth policy. */
export function openPlaybackGroupStream(open: (path: string, headers: Record<string, string>, signal: AbortSignal) => Promise<Response>, token: string, path: string, lastEventId: string, signal: AbortSignal): Promise<Response> {
  return open(path, {Accept: 'text/event-stream', Authorization: `Bearer ${token}`,
    ...(lastEventId ? {'Last-Event-ID': lastEventId} : {})}, signal);
}
