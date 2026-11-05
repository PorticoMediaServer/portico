import {encodedRoutePath, routePath, routePattern} from '../../../contracts/src/routes.ts';
import type {ItemResponse} from '../media-item.ts';
import type {V1Http} from '../playback-v1/http.ts';
import {call, enc} from '../playback-v1/http.ts';
import {decodeOpen} from '../../../contracts/src/runtime.ts';
import {SessionsClient} from '../playback-v1/sessions.ts';
import type {OriginalChannelStart} from '../playback-v1/channel-control.ts';
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
/** The complete original channel cleanup continuation stays in one async operation. */
export async function stopOriginalPreparingChannel(http: V1Http, body: OriginalChannelStart['body'], actual: Readonly<{id: string; generation: number}>, signal: AbortSignal, claimStop: () => boolean): Promise<void> {
  // The general playable Session intentionally omits channelId. Read the official view.
  const response = await call(http, {method: 'GET', path: encodedRoutePath('get_playback_session', {id: enc(actual.id)}), signal});
  // O-159 open tier: optional newer enum values do not suppress the original Stop.
  const observed = decodeOpen('GetPlaybackSessionResponse', response.body) as GetPlaybackSessionResponse;
  if (observed.id !== actual.id || observed.presentation.generation !== actual.generation || observed.role !== 'local' ||
    observed.state === 'ended' || !['live', 'channel'].includes(observed.kind) || observed.channelId !== body.channelId ||
    observed.queue || observed.itemId || observed.versionId || observed.discTitle) return;
  // Guide generation remains in the original POST receipt; SessionView does not echo it.
  if (!claimStop()) return;
  await new SessionsClient(http).stop(actual.id, undefined, signal);
}
/** Page teardown must keep using the captured API's keepalive path, without waiting. */
export function farewellPlaybackSession(farewell: (path: string, method: string, body?: unknown) => void, id: string, generation: number, positionMs?: number): void {
  farewell(routePattern('stop_playback_session').replace('{id}', encodeURIComponent(id)), 'DELETE', {generation,
    ...(positionMs !== undefined ? {positionMs: Math.max(0, Math.round(positionMs))} : {})});
}
