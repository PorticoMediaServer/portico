import {routes, type RouteId} from '../../../contracts/src/routes.ts';

/** A server tier that may open Server settings. */
export type ServerTier = 'owner' | 'admin';

const access = new Map<string, Readonly<{access: string; requires?: string}>>(routes.map(route => [route.id, route]));

/** The access kinds an administrator may call, named one by one: `admin_scope`, and the viewer
 *  kinds when the route asks for no administration. Any other kind, including one a later contract
 *  adds, is not allowed until it is named here. */
const ADMIN_SCOPE = 'admin_scope';
const VIEWER_ACCESS: ReadonlySet<string> = new Set(['public', 'device', 'viewer_item_restrictions']);

/** Whether this tier may call a route with this published access. */
export function serverTierMayCallAccess(route: Readonly<{access: string; requires?: string}>, tier: ServerTier): boolean {
  if (tier === 'owner') return true;
  if (route.access === ADMIN_SCOPE) return true;
  return VIEWER_ACCESS.has(route.access) && route.requires !== 'admin';
}

/**
 * Whether this tier may call the route, read from its published access in the generated route
 * contracts (routes.ts), the server's own source for its access rules. The owner may call any of
 * them; an administrator only what `serverTierMayCallAccess` names. An `owner` route, a device
 * route that asks for administration (the contract publishes no administrator tier for it), and
 * an unknown kind are the owner's. An unknown route is no one's.
 */
export function serverTierMayCall(id: RouteId, tier: ServerTier): boolean {
  const route = access.get(id);
  return !!route && serverTierMayCallAccess(route, tier);
}

/** Whether this tier may call every route a part of a page calls, so it never offers a call that fails. */
export function serverTierMayCallAll(ids: readonly RouteId[], tier: ServerTier): boolean {
  return ids.every(id => serverTierMayCall(id, tier));
}
