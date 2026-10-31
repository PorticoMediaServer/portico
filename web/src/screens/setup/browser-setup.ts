import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {routeOrigin, assertFirstContactIdentity} from '@core/route-identity.ts';
import {knownServerRoutes} from '@core/app-sessions.ts';
import {connectPairedServer, inspectDirectServer} from '@core/server-connections.ts';
import {parseServerSignIn} from '@core/sign-in.ts';
import type {SetupPairing} from '../../platform/setup-adapter';
import {browserAppSessions, browserConnectionEnvironment} from '../../platform/server-connections';

/** The verified pairing of the server that served this page: its pinned identity, checked on first contact. */
export function browserSetupPairing(origin: () => string = () => location.origin) {
  return async (signal: AbortSignal): Promise<SetupPairing> => {
    const address = routeOrigin(origin());
    const pin = await inspectDirectServer(address, browserConnectionEnvironment(), signal);
    const connected = await connectPairedServer(address, pin, browserConnectionEnvironment(pin.serverId), signal);
    try {
      const server = parseServerSignIn(await connected.api.request(routePath('get_system'), 'GET', undefined, signal));
      if (server.id !== pin.serverId) throw Object.assign(new Error('The server identity changed.'), {code: 'identity_mismatch'});
      assertFirstContactIdentity(address, pin, {knownRoutes: knownServerRoutes(await browserAppSessions().read())});
      return Object.freeze({server, api: connected.api, pin, dispose: () => connected.connection.dispose()});
    } catch (error) {
      connected.connection.dispose();
      throw error;
    }
  };
}
