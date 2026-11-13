import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {V1Http} from '../playback-v1/http.ts';
import {call, enc} from '../playback-v1/http.ts';
import {SessionsClient} from '../playback-v1/sessions.ts';
import {playbackPath} from '../playback-v1/routes.ts';
import {throwIfAborted} from '../abort-lifetime.ts';
import type {OriginalChannelStart} from '../playback-v1/channel-control.ts';

type PlaybackApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
/** Native keeps ordinary playback until its own measured handover change lands. */
export function readPlaybackCapabilities(_api: PlaybackApi, signal?: AbortSignal): Promise<unknown> {
  throwIfAborted(signal);
  return Promise.resolve({features: {}});
}
/** Web alone enables the rebuilt server's prepared audio joins. */
export function readWebPlaybackCapabilities(api: PlaybackApi, signal?: AbortSignal): Promise<unknown> {
  throwIfAborted(signal);
  return api.request<{capabilities?: string[]}>(routePath('get_system'), 'GET', undefined, signal).then(system =>
    ({features: system.capabilities?.includes('queue-transitions') ? {queueTransitions: 'enabled'} : {}}));
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
/** The complete original channel cleanup continuation stays in one async operation: the session is
 * stopped only while it is still this start's own channel playback. */
export async function stopOriginalPreparingChannel(http: V1Http, body: OriginalChannelStart['body'], actual: Readonly<{id: string; generation: number}>, signal: AbortSignal, claimStop: () => boolean): Promise<void> {
  const response = await call(http, {method: 'GET', path: encodedRoutePath('get_playback_session', {id: enc(actual.id)}), signal});
  const observed = response.body as {id?: unknown; state?: unknown; channel?: {id?: unknown}; presentation?: {generation?: unknown}} | null;
  const generation = typeof observed?.presentation?.generation === 'number' ? observed.presentation.generation : 0;
  // A live session numbers no generation; a numbered one must be the one this start saw.
  if (!observed || observed.id !== actual.id || observed.state === 'ended' || observed.channel?.id !== body.channel || generation > 0 && generation !== actual.generation) return;
  if (!claimStop()) return;
  await new SessionsClient(http).stop(actual.id, undefined, signal);
}
/** Page teardown must keep using the captured API's keepalive path, without waiting. The stop
 * carries the final position. */
export function farewellPlaybackSession(farewell: (path: string, method: string, body?: unknown) => void, id: string, _generation: number, positionMs?: number): void {
  farewell(playbackPath('DELETE /v1/playback/sessions/{id}', {id}), 'DELETE',
    positionMs !== undefined ? {positionMs: Math.max(0, Math.round(positionMs))} : undefined);
}
