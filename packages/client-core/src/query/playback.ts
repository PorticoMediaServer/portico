import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {V1Http} from '../playback-v1/http.ts';
import {call, enc} from '../playback-v1/http.ts';
import {decodeOpen} from '../../../contracts/src/runtime.ts';
import {SessionsClient} from '../playback-v1/sessions.ts';
import {playbackPath} from '../playback-v1/routes.ts';
import {throwIfAborted} from '../abort-lifetime.ts';
import type {OriginalChannelStart} from '../playback-v1/channel-control.ts';
import type {GetPlaybackSessionResponse} from '../../../contracts/src/types.ts';

type PlaybackApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
/** The playback features the server offers beyond sessions and queues. The rebuilt server offers
 * none yet: no hand-over to the system audio players (they still speak the old server's queue and
 * progress messages) and no prepared gapless joins, so audio plays in the app's own player. */
export function readPlaybackCapabilities(_api: PlaybackApi, signal?: AbortSignal): Promise<unknown> {
  throwIfAborted(signal);
  return Promise.resolve({features: {}});
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
/** Page teardown must keep using the captured API's keepalive path, without waiting. The stop
 * carries the final position. */
export function farewellPlaybackSession(farewell: (path: string, method: string, body?: unknown) => void, id: string, _generation: number, positionMs?: number): void {
  farewell(playbackPath('DELETE /v1/playback/sessions/{id}', {id}), 'DELETE',
    positionMs !== undefined ? {positionMs: Math.max(0, Math.round(positionMs))} : undefined);
}
