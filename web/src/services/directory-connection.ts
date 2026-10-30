import {inspectDirectServer} from '@core/server-connections.ts';
import {ServerSignInProbe} from '@core/sign-in.ts';
import {RouteError, routeOrigin} from '@core/route-identity.ts';
import {browserConnectionEnvironment} from '../platform/server-connections';

/** Typed local address entry still proves the actual server before handoff. */
export async function servicesLocalOrigin(address: string, signal: AbortSignal) {
  const probe = new ServerSignInProbe(), abort = () => probe.cancel();
  signal.addEventListener('abort',abort,{once:true});
  try {
    signal.throwIfAborted(); const system = await probe.inspect(address); signal.throwIfAborted();
    const state = probe.getSnapshot();
    if (!system || !state.origin) throw state.error ?? new RouteError('route_unavailable','The server did not answer.');
    const origin = routeOrigin(state.origin), pin = await inspectDirectServer(origin,browserConnectionEnvironment(system.id),signal); signal.throwIfAborted();
    if (pin.serverId !== system.id) throw new RouteError('identity_mismatch','The server identity changed.');
    return origin;
  } finally {signal.removeEventListener('abort',abort); probe.dispose();}
}
