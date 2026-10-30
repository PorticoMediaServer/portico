import {historyPeriods, playlistListFilters, resourceContentFilters, resourceContentQuery, savedListFilters, type SavedRoute} from '@core/saved.ts';
import type {SavedDownloadsScope} from './downloads-port.ts';

type Route = SavedRoute | Readonly<{view: 'downloads'}> | Readonly<{view: 'home'}>;

const names = ['home', 'watchlist', 'favorites', 'playlists', 'playlist', 'collections', 'views', 'history', 'resource', 'downloads'] as const;
const text = (value: unknown, max = 256): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);
function invalid(): never {throw new Error('invalid-saved-route');}
/** The owned URL schema forwards only the selected server policy, never raw keys. */
export function parseSavedScreenRoute(raw: Record<string, unknown>): Route {
  // A bare /saved is the phone directory; any owned parameter without a view keeps
  // the existing watchlist semantics (or resource, for resourceId) unchanged.
  const owned = ['resourceId', 'playlistId', 'sort', 'direction', 'filter', 'shape', 'period'] as const;
  const hasOwned = owned.some(key => raw[key] !== undefined);
  const view = names.find(name => name === (raw.view ?? (hasOwned ? (raw.resourceId === undefined ? 'watchlist' : 'resource') : 'home')));
  if (!view) invalid();
  if (view === 'downloads') return {view};
  // A directory route owns no other saved parameter; an explicit view=home with one
  // is refused rather than silently dropping it.
  if (view === 'home') {if (hasOwned) invalid(); return {view};}
  // A playlist or collection page carries its temporary order and filter.
  const content = () => {try {return resourceContentQuery(raw);} catch {return invalid();}};
  if (view === 'playlist') return text(raw.playlistId) ? {view, playlistId: raw.playlistId, ...content()} : invalid();
  if (view === 'resource' && !text(raw.resourceId)) invalid();
  if (view === 'resource') content();
  if (raw.resourceId !== undefined && !text(raw.resourceId)) invalid();
  if (raw.sort !== undefined && !text(raw.sort, 64)) invalid();
  if (raw.direction !== undefined && raw.direction !== 'asc' && raw.direction !== 'desc') invalid();
  if (raw.filter !== undefined && !(view === 'playlists' ? playlistListFilters : view === 'watchlist' || view === 'favorites' ? savedListFilters : view === 'resource' ? resourceContentFilters : []).some(value => value === raw.filter)) invalid();
  if (raw.shape !== undefined && (!['watchlist', 'favorites', 'collections', 'views'].includes(String(view)) || raw.shape !== 'posters' && raw.shape !== 'squares')) invalid();
  if (raw.period !== undefined && (view !== 'history' || !historyPeriods.some(value => value === raw.period))) invalid();
  const filter = [...playlistListFilters, ...savedListFilters, ...resourceContentFilters].find(value => value === raw.filter);
  const period = historyPeriods.find(value => value === raw.period);
  return {view,
    ...(text(raw.resourceId) ? {resourceId: raw.resourceId} : {}), ...(text(raw.sort, 64) ? {sort: raw.sort} : {}),
    ...(raw.direction === 'asc' || raw.direction === 'desc' ? {direction: raw.direction} : {}),
    ...(filter === undefined ? {} : {filter}),
    ...(raw.shape === 'posters' || raw.shape === 'squares' ? {shape: raw.shape} : {}),
    ...(period === undefined ? {} : {period})};
}

/** The core normalizes query property order. A render must compare the actual
 * destination/query, independent of how the router constructed its object. */
export function savedRouteKey(route: Route): string {
  return JSON.stringify(Object.entries(route).filter(([, value]) => value !== undefined).sort(([a], [b]) => a.localeCompare(b)));
}

export function sameSavedRoute(actual: Route | null, expected: Route): boolean {
  return actual !== null && savedRouteKey(actual) === savedRouteKey(expected);
}

/** Runtime-only identity for UI state, never a native/persisted storage key. */
export function savedViewKey(scope: SavedDownloadsScope, route: Route): string {
  return JSON.stringify([scope.serverId, scope.authority, scope.accountId, scope.profileId, scope.sessionFamilyId, savedRouteKey(route)]);
}
