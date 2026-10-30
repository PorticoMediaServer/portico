import {fetchItemRecommendations, fetchShowRecommendations, type ItemRecommendations, type ShowRecommendations} from '@core/recommendations.ts';
import type {DetailProjection} from '@core/detail.ts';
import type {ShowWorkspaceData} from '@core/show-workspace.ts';
import {screenLifetime, type ScreenContext} from './context';

type Recommendations = ItemRecommendations | ShowRecommendations;
/** Bounded real recommendation rows. No browser-side ranking, membership or community guesses. */
export function createRecommendations(context: ScreenContext) {
  const life = screenLifetime(context), listeners = new Set<() => void>();
  let state: Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; data?: Recommendations}> = {phase: 'idle'}, request = 0;
  let controller: AbortController | undefined;
  const publish = (next: typeof state) => { state = Object.freeze(next); for (const listener of listeners) listener(); };
  return {
    getSnapshot: () => state,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    async select(data: DetailProjection | ShowWorkspaceData, isCurrent: () => boolean) {
      life.assert(); const generation = ++request; controller?.abort(); controller = new AbortController();
      publish({phase: 'loading'});
      try {
        const value = 'show' in data ? await fetchShowRecommendations(life.api, data.show.id, 12, life.signal(controller.signal)) : await fetchItemRecommendations(life.api, data.item.id, 12, life.signal(controller.signal));
        if (generation !== request || !life.current() || !isCurrent()) return;
        if (value.revision.catalog !== data.revision.catalog || value.revision.viewer !== data.revision.viewer) throw new Error('Recommendation revision changed');
        publish({phase: 'ready', data: value});
      } catch { if (generation === request && life.current() && isCurrent()) publish({phase: 'error'}); }
    },
    clear() { request++; controller?.abort(); publish({phase: 'idle'}); },
    dispose() { request++; life.dispose(); controller?.abort(); publish({phase: 'idle'}); listeners.clear(); },
  };
}
