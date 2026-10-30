import type {SavedRoute, SavedService} from '@core/saved.ts';
import type {SavedRuntime} from './runtime.ts';
import {sameSavedRoute} from './route.ts';

/** One exact retry lifetime: the original runtime, normalized route and reader
 * identities. The ticket is render identity only; the layout-owned admission is
 * what authorizes a command. */
export type SavedRetryTicket = Readonly<{
  runtime: SavedRuntime;
  route: SavedRoute;
  routeKey: string;
  posters: SavedService;
  squares: SavedService;
  saved: SavedService;
  reportError: (error: unknown) => void;
}>;
export type SavedRetryAdmission = {ticket: SavedRetryTicket; live: boolean; pending: symbol | undefined};

export function createSavedRetryAdmission(ticket: SavedRetryTicket): SavedRetryAdmission {
  return {ticket, live: true, pending: undefined};
}

/** Dead and detached before any abort/select callback can reenter. */
export function retireSavedRetryAdmission(value: SavedRetryAdmission): void {
  value.live = false;
  value.pending = undefined;
}

/** The original route/readers must still own the current runtime, not merely the
 * parent's global operations flag. Every external getter is followed by a liveness
 * recheck: a getter may synchronously retire the admission and still return old
 * matching data. */
export function savedRetryCurrent(value: SavedRetryAdmission): boolean {
  if (!value.live) return false;
  const runtime = value.ticket.runtime;
  if (!runtime.operations.current) return false;
  if (!value.live) return false;
  const route = value.ticket.route;
  if (route.view === 'watchlist' || route.view === 'favorites') {
    if (!value.live) return false;
    const poster = value.ticket.posters.getSnapshot();
    if (!value.live) return false;
    const square = value.ticket.squares.getSnapshot();
    if (!value.live) return false;
    return sameSavedRoute(poster.route, {...route, shape: 'posters'})
      && sameSavedRoute(square.route, {...route, shape: 'squares'});
  }
  if (!value.live) return false;
  const saved = value.ticket.saved.getSnapshot();
  if (!value.live) return false;
  return sameSavedRoute(saved.route, route);
}
