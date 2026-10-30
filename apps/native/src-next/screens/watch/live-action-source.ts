import {playbackFence, sameFence, type ChannelPlaybackFence} from '../../../../../packages/player-core/src';
import {wireId} from '../../../../../packages/player-core/src/validation';
import type {ChannelApi} from '../../../../../packages/client-core/src/channel-guide';
import {legacyChannel, type LegacyGuideOptions} from '../../../../../packages/client-core/src/guide/legacy-source';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import type {NativeRouteSnapshot} from '../../shell/routes';
import type {NativeTVLiveGuide} from './live-guide';

export type NativeLiveActionSource = Readonly<{
  fence: ChannelPlaybackFence;
  target: Readonly<{kind: 'live-source' | 'library-channel'; sourceId: string; channelId: string; generation: string}>;
  query: Pick<LegacyGuideOptions, 'timezone' | 'search' | 'group' | 'favorites' | 'includeHidden'>;
  authority: ShellAuthority;
  route: NativeRouteSnapshot;
  current: () => boolean;
  api: ChannelApi;
}>;
const retired = () => Object.assign(new Error('Live action source retired'), {name: 'AbortError'});
const principal = (context: NativeShellBootstrapContext) => {
  const session = context.session();
  return session ? JSON.stringify([session.sessionFamilyId, session.tokenGeneration, session.authorizationHorizon, session.deviceId ?? null,
    session.installationId ?? null, session.viewer.serverId, session.viewer.accountId, session.viewer.profileId, session.viewer.authority, session.viewer.role]) : undefined;
};

/** A fresh one-channel read is not a guide lineup. Home and direct channel routes
 * can borrow only their accepted local output; all rights come from the read. */
export function captureLiveActionSource(context: NativeShellBootstrapContext, guide: NativeTVLiveGuide, presented: () => boolean): NativeLiveActionSource | undefined {
  const value = context.playback.getSnapshot(), offered = value.fence, route = context.navigation.getSnapshot();
  const authority = Object.freeze({...context.authority()}), originalPrincipal = principal(context), session = context.session();
  if (!context.current() || !presented() || !session || !originalPrincipal || offered?.kind !== 'channel' || !wireId(offered.sourceId)) return undefined;
  let fence: ChannelPlaybackFence;
  try {fence = playbackFence(offered);} catch {return undefined;}
  const sourceId = offered.sourceId;
  if (fence.playback.scope.serverId !== authority.serverId || fence.playback.scope.accountId !== authority.accountId
    || fence.playback.scope.profileId !== authority.profileId || fence.playback.scope.authority !== session.viewer.authority) return undefined;
  const bound = () => {
    const live = context.playback.getSnapshot(), output = live.playback.session, channel = live.playback.channel, linear = live.playback.linear?.channel;
    return context.current() && presented() && context.navigation.getSnapshot() === route && sameAuthority(authority, context.authority())
      && originalPrincipal === principal(context) && !live.retired && !live.startFailure && !live.capacityWait
      && (live.playback.phase === 'ready' || live.playback.phase === 'starting') && sameAuthority(live.authority, authority)
      && live.dock.local?.kind === 'live' && live.dock.local.expanded && sameFence(live.dock.local.fence, fence) && sameFence(live.fence, fence)
      && live.playback.playbackRun === fence.playback.run && output?.id === fence.sessionId && output.generation === fence.generation
      && channel?.kind === fence.channelKind && channel.sourceId === sourceId && channel.channelId === fence.channelId && channel.generation === fence.channelGeneration
      && linear?.kind === fence.channelKind && linear.sourceId === sourceId && linear.channelId === fence.channelId && linear.generation === fence.channelGeneration;
  };
  if (!bound()) return undefined;
  const fromGuide = guide.captureSource(), raw = fromGuide && legacyChannel(fromGuide.origin.row);
  let query: NativeLiveActionSource['query'], sourceCurrent: () => boolean, sourceAPI: ChannelApi;
  if (fromGuide && raw && sameFence(fromGuide.fence, fence) && raw.sourceId === sourceId && raw.id === fence.channelId
    && raw.provenance === fence.channelKind && raw.generation === fence.channelGeneration) {
    query = fromGuide.origin.options;sourceCurrent = () => bound() && fromGuide.current();sourceAPI = fromGuide.api;
  } else {
    const descriptor = route.route;
    if (!(descriptor.kind === 'shell' && descriptor.destination.kind === 'home'
      || descriptor.kind === 'channel' && descriptor.sourceId === sourceId && descriptor.channelId === fence.channelId)) return undefined;
    // This independent request chooses the actual device zone once. It never
    // substitutes a zone for an existing published guide or creates a lineup.
    let timezone: string;
    try {timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;if (!timezone) return undefined;new Intl.DateTimeFormat(undefined, {timeZone: timezone});}
    catch {return undefined;}
    query = Object.freeze({timezone});sourceCurrent = bound;sourceAPI = context.api;
  }
  const api: ChannelApi = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!sourceCurrent() || signal?.aborted) throw retired();
    try {const result = await sourceAPI.request<T>(path, method, body, signal);if (!sourceCurrent() || signal?.aborted) throw retired();return result;}
    catch (error) {if (!sourceCurrent() || signal?.aborted) throw retired();throw error;}
  }};
  return Object.freeze({fence, authority, route, query, current: sourceCurrent, api,
    target: Object.freeze({kind: fence.channelKind, sourceId, channelId: fence.channelId, generation: fence.channelGeneration})});
}
