import {routePath} from '../../../contracts/src/routes.ts';
import type {GuideRoute} from '../channel-guide.ts';

export type SearchGuideRoute = Pick<GuideRoute, 'kind' | 'sourceId' | 'timezone' | 'search' | 'start' | 'end'>;

/** One unchanged Search decoration batch. Return the original transport promise
 * so the caller keeps its sequential batches, guards, parsing and clock sampling. */
export function readSearchLiveGuideResponse(api: Readonly<{
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}>, route: SearchGuideRoute, channels: readonly string[], signal: AbortSignal) {
  const query = new URLSearchParams({...route, channels: channels.join(','), limit: '50'});
  return api.request(routePath('get_guide') + '?' + query, 'GET', undefined, signal);
}
