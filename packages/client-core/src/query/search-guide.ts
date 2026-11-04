import {routePath} from '../../../contracts/src/routes.ts';
import {parseChannelGuide, type ChannelGuide, type GuideRoute} from '../channel-guide.ts';

export type SearchGuideRoute = Pick<GuideRoute, 'kind' | 'sourceId' | 'timezone' | 'search' | 'start' | 'end'>;

/** The existing Search live-result decoration: selected-source windows in
 * sequential batches of 50, with no paging, cache or additional retry. */
export async function readSearchLiveGuides(api: Readonly<{
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}>, serverId: string, route: SearchGuideRoute, channels: readonly string[], lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean; receive: (guide: ChannelGuide) => void}>) {
  for (let start = 0; start < channels.length; start += 50) {
    const query = new URLSearchParams({...route, channels: channels.slice(start, start + 50).join(','), limit: '50'});
    const raw = await api.request(routePath('get_guide') + '?' + query, 'GET', undefined, lifetime.signal);
    if (!lifetime.isCurrent()) return false;
    lifetime.receive(parseChannelGuide(raw, serverId, route));
  }
  return true;
}
