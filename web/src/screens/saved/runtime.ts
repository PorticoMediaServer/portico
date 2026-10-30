import {readingSavedReferencePath, parseReadingSavedReferenceReceipt, validReadingSavedReferenceCommand, type ReadingSavedReferenceCommand} from '@core/reading-saved-group.ts';
import {SavedService, type SavedApi, type SavedRestore, type SavedRoute} from '@core/saved.ts';
import {PersonalSavedService, isPersonalSavedRoute} from '@core/personal-saved.ts';
import {DownloadsService} from '@core/downloads-service.ts';
import {randomId} from '@core/random-id.ts';
import type {SavedDownloadsScope} from './downloads-port.ts';
import {SavedOperations} from './operations.ts';
import {SavedReadyDownloads, type SaveReadyPreparation} from './ready-downloads.ts';
import {SavedSummary} from './summary.ts';

export type SavedScreenRoute = SavedRoute | Readonly<{view: 'downloads'}> | Readonly<{view: 'home'}>;
/** The web's home route is the directory; its canonical My List is the only read it
 * may make. `home` is a UI route and never reaches core by type. */
/** One frozen query object, so every effect and quiet refresh sees a stable route. */
const watchlistReader = Object.freeze({view: 'watchlist', sort: 'added', direction: 'desc'} as const);
/** One canonical reader query for the directory, its preview and My List.
 * Current server Saved publishes added in both directions. Keep explicit URL
 * sort/direction; never rewrite only the request behind an exact route witness. */
export const savedReaderRoute = (route: SavedScreenRoute): Exclude<SavedScreenRoute, {view: 'home'}> => {
  if (route.view === 'home') return watchlistReader;
  if ((route.view === 'watchlist' || route.view === 'playlists') && route.sort === undefined) return Object.freeze({...route, sort: route.view === 'playlists' ? 'updated' : 'added', direction: route.direction ?? 'desc'});
  return route;
};

/** One captured parent lifetime, using only the published server services. Native
 * files/rules/artwork are installed separately by Settings; this creates no store. */
export function createSavedRuntime(input: Readonly<{api: SavedApi; scope: SavedDownloadsScope; isCurrent: () => boolean; deviceId?: string}>) {
  if (!['local', 'hosted'].includes(input.scope.authority) || [input.scope.serverId, input.scope.accountId, input.scope.profileId, input.scope.sessionFamilyId].some(value => typeof value !== 'string' || !value || /[\u0000-\u001f\u007f]/.test(value))) throw new Error('Saved requires a complete captured viewer scope.');
  const scope = Object.freeze({...input.scope}), operations = new SavedOperations(input.isCurrent), controllers = new Set<AbortController>();
  const abort = () => new DOMException('Saved authority is no longer current.', 'AbortError');
  const api: SavedApi = {
    async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
      if (!operations.current || signal?.aborted) throw abort();
      const controller = new AbortController(), stop = () => controller.abort();
      signal?.addEventListener('abort', stop, {once: true}); controllers.add(controller);
      try {
        if (!operations.current || signal?.aborted) throw abort();
        const result = await input.api.request<T>(path, method, body, controller.signal);
        if (!operations.current || controller.signal.aborted) throw abort();
        return result;
      } finally { signal?.removeEventListener('abort', stop); controllers.delete(controller); }
    },
  };
  const serverScope = Object.freeze({serverId: scope.serverId, viewerId: JSON.stringify([scope.authority, scope.accountId, scope.profileId, scope.sessionFamilyId])});
  const makeSaved = () => new SavedService({api, scope: serverScope, requestId: async () => randomId()});
  const saved = makeSaved(), posters = makeSaved(), squares = makeSaved(), personal = new PersonalSavedService(api, serverScope, async () => randomId()), downloads = new DownloadsService(api);
  const summary = new SavedSummary(api, serverScope, operations);
  let readyDownloads: SavedReadyDownloads | undefined;
  if (input.deviceId) downloads.followDevice(input.deviceId);
  return {
    scope, operations, saved, posters, squares, personal, downloads, summary,
    canRemoveReadingGroup(referenceId: string): boolean {return operations.current && readingSavedReferencePath(referenceId) !== undefined;},
    async removeReadingGroup(referenceId: string, command: ReadingSavedReferenceCommand, signal: AbortSignal) {
      const path = readingSavedReferencePath(referenceId), captured = Object.freeze({...command});
      if (!path || !validReadingSavedReferenceCommand(captured)) throw new Error('invalid_reading_saved_group');
      const value = await api.request<unknown>(path, 'DELETE', captured, signal);
      signal.throwIfAborted();
      if (!operations.current) throw abort();
      return parseReadingSavedReferenceReceipt(value, scope.serverId, referenceId, captured);
    },
    /** Install once for this captured parent lifetime. Route unmounts do not
     * cancel an accepted native queue; parent retirement closes the observer. */
    installReadyDownloads(save: SaveReadyPreparation, reportError: (error: unknown) => void): void {
      if (!operations.current || readyDownloads) return;
      readyDownloads = new SavedReadyDownloads(downloads, scope, operations, save, reportError);
      downloads.start();
    },
    async select(route: SavedScreenRoute, restore?: SavedRestore): Promise<void> {
      const reader = savedReaderRoute(route);
      if (!operations.current || reader.view === 'downloads') return;
      if (isPersonalSavedRoute(reader)) { await Promise.all([personal.select(reader, restore), reader.view === 'collections' || reader.view === 'views' ? summary.refresh() : Promise.resolve()]); return; }
      if (reader.view === 'watchlist' || reader.view === 'favorites') {
        await Promise.all([posters.select({...reader, shape: 'posters'}, restore), squares.select({...reader, shape: 'squares'}, restore)]);
      } else await saved.select(reader, restore);
    },
    dispose(): void {
      readyDownloads?.dispose();
      summary.dispose();
      operations.dispose(); for (const controller of controllers) controller.abort(); controllers.clear();
      saved.dispose(); posters.dispose(); squares.dispose(); personal.dispose(); downloads.dispose();
    },
  };
}
export type SavedRuntime = ReturnType<typeof createSavedRuntime>;
