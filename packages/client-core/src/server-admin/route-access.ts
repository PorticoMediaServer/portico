import {routes, type RouteId} from '../../../contracts/src/routes.ts';

/** A server tier that may open Server settings. */
export type ServerTier = 'owner' | 'admin';

const access = new Map<string, Readonly<{access: string; requires?: string}>>(routes.map(route => [route.id, route]));

/**
 * Whether this tier may call the route, read from its published access in the generated route
 * contracts (routes.ts), the server's own source for its access rules. The owner may call any of
 * them. An administrator may call an `admin_scope` route, and a viewer route that asks for no
 * administration scope; an `owner` route, or a device route that asks for one, is the owner's,
 * because the contract does not publish that an administrator may call it.
 */
export function serverTierMayCall(id: RouteId, tier: ServerTier): boolean {
  const route = access.get(id);
  if (!route) return false;
  if (tier === 'owner') return true;
  if (route.access === 'admin_scope') return true;
  if (route.access === 'owner') return false;
  return route.requires !== 'admin';
}

/** Whether this tier may call every route a part of a page calls, so it never offers a call that fails. */
export function serverTierMayCallAll(ids: readonly RouteId[], tier: ServerTier): boolean {
  return ids.every(id => serverTierMayCall(id, tier));
}
