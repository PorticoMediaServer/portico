import {subscribePresentation} from '../../shell/fact-hooks';
import type {ShellFactsSource} from '../../shell/fact-source';
import {defaultI18n} from '@i18n';
import {shellLanguage} from '../../shell/settings';
import type {ItemResponse} from '@core/media-item.ts';
import type {CastMetadata} from '@core/cast/index.ts';
import {WebCastSender} from '../../platform/cast';
import {PlayOnController, mediaAirPlayPort} from '../../platform/playback/play-on';
import {WatchReceivers, watchLinkTv} from './receivers';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {WatchBindings} from './types';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import {readWatchCastMetadata} from './cast-metadata';

/** Only current source facts can prove a caption survives a video-only transport.
 * A missing plan is unknown; older no-plan offers may prove off, and the current
 * session itself can prove that the chosen caption was already burned. */
export function remotePictureKeepsCaptions(snapshot: WebPlaybackRuntimeSnapshot): boolean {
  const fence=snapshot.fence,offers=snapshot.offers,current=offers?.current,session=snapshot.session;
  if(snapshot.disposed||!fence||fence.kind==='local'||!current||!session)return false;
  if(current.sessionId!==fence.sessionId||current.generation!==fence.generation||current.sourceId!==fence.sourceId
    ||session.id!==fence.sessionId||session.presentation.generation!==fence.generation)return false;
  const plan=offers.subtitlePlan;
  if(plan){
    if(plan.sessionId!==fence.sessionId||plan.generation!==fence.generation||plan.sourceId!==fence.sourceId)return false;
    return plan.mode==='off'||plan.renderer==='burn_in';
  }
  const selected=session.presentation.subtitle;
  if(selected?.delivery==='burn'&&session.presentation.subtitles.length===0)return true;
  return !selected&&session.presentation.subtitles.length===0
    &&(current.subtitleSelection==='off'||current.subtitleSelection==='disabled');
}

/** Page-drawn captions require the existing burn transition on actual wireless activation. */
function airPlayCanReconcileCaptions(snapshot: WebPlaybackRuntimeSnapshot): boolean {
  const fence = snapshot.fence, plan = snapshot.offers?.subtitlePlan, current = snapshot.offers?.current, session = snapshot.session;
  return !!fence && fence.kind === 'queue' && !!plan && !!current && !!session && !snapshot.disposed
    && current.sessionId === fence.sessionId && current.generation === fence.generation && current.sourceId === fence.sourceId
    && session.id === fence.sessionId && session.presentation.generation === fence.generation
    && plan.sessionId === fence.sessionId && plan.generation === fence.generation && plan.sourceId === fence.sourceId
    && plan.mode === 'track' && (plan.renderer === 'client' || plan.renderer === 'external_text') && plan.selected?.enabled === true;
}

export type WatchPlaybackContext = Pick<ShellAssemblyContext, 'api' | 'session' | 'isCurrent' | 'epoch'> & Partial<Pick<ShellAssemblyContext, 'preferences' | 'facts'>> & Readonly<{factsSource?: ShellFactsSource}>;
/** Created only by the mounted persistent host. The same real video owns AirPlay. */
export function createWatchPlayOn(context: WatchPlaybackContext, binding: WatchBindings, video: HTMLVideoElement, applicationId?: string) {
  const life = new AbortController(), authority = binding.runtime.getSnapshot().authority;
  const originalSession={...context.session(),viewer:{...context.session().viewer}},pin=context.api.getRouteConnection()?.pin,originalOrigin=new URL(context.api.baseUrl).origin;
  const items = new Map<string, ItemResponse>();
  const isCurrent = () => {
    const viewer = context.session().viewer;
    return !life.signal.aborted && new URL(context.api.baseUrl).origin===originalOrigin && context.session().sessionFamilyId===originalSession.sessionFamilyId && context.session().deviceId===originalSession.deviceId && context.api.getRouteConnection()?.pin.publicKey===pin?.publicKey && context.isCurrent() && !binding.runtime.getSnapshot().disposed && context.epoch === authority.epoch
      && viewer.role === originalSession.viewer.role && viewer.serverId === authority.serverId && viewer.authority === authority.authority && viewer.accountId === authority.accountId && viewer.profileId === authority.profileId;
  };
  const assertCurrent = () => {if (!isCurrent()) throw new DOMException('Playback authority retired', 'AbortError');};
  const api = {async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    assertCurrent(); const active = signal ? AbortSignal.any([signal, life.signal]) : life.signal;
    const value = await context.api.request<T>(path, method, body, active); assertCurrent();active.throwIfAborted();return value;
  }};
  const describe = async (itemId: string, purpose: 'identity' | 'cover'): Promise<CastMetadata> => {
    const described = await readWatchCastMetadata(api, itemId, assertCurrent, purpose);
    assertCurrent();
    items.set(itemId, described.item);
    while (items.size > 4) items.delete(items.keys().next().value!);
    return described.metadata;
  };
  const describeItem = (itemId: string) => describe(itemId, 'cover');
  const language = () => {const preferences = context.preferences?.getSnapshot('web') ?? context.facts?.preferences;return preferences ? shellLanguage(preferences) : defaultI18n;};
  const receivers = new WatchReceivers({api: {request: (path, method, body, signal) => api.request(path, method, body, signal)}, runtime: binding.runtime, t: (id, values) => language().t(id, values), isCurrent, reportError: binding.reportError});
  const linkTv = watchLinkTv(api);
  const sender = new WebCastSender({api,isCurrent,
    ...(pin&&pin.serverId===originalSession.viewer.serverId&&originalSession.deviceId?{controllerContext:{origin:new URL(context.api.baseUrl).origin,publicKey:pin.publicKey,
      serverId:originalSession.viewer.serverId,authority:originalSession.viewer.authority,accountId:originalSession.viewer.accountId,profileId:originalSession.viewer.profileId,
      senderDeviceId:originalSession.deviceId,senderFamilyId:originalSession.sessionFamilyId}}:{}), serverOrigin: context.api.baseUrl, castApplicationId: applicationId,
    describe: async itemId => (await describe(itemId, 'identity')).title});
  const controller = new PlayOnController({sender, receivers, runtime: binding.runtime, airPlay: mediaAirPlayPort(video, undefined, {
    isCurrent: () => {const shown = binding.runtime.getSnapshot();return isCurrent() && !!shown.fence && (remotePictureKeepsCaptions(shown) || airPlayCanReconcileCaptions(shown)) && binding.runtime.browserAirPlayReady(shown.fence);},
    subscribe: binding.runtime.subscribe,
  }), isCurrent, describeItem, reportError: binding.reportError});
  const offPresentation = subscribePresentation(context, () => {if (isCurrent()) receivers.refreshPresentation();});
  return {controller, receivers, linkTv, item: (id: string) => isCurrent() ? items.get(id) : undefined,
    dispose: () => {life.abort();offPresentation();controller.dispose();receivers.dispose();items.clear();}};
}
export type WatchPlayOnBinding = ReturnType<typeof createWatchPlayOn>;
