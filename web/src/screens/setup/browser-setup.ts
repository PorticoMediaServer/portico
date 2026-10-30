import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {routeOrigin, assertFirstContactIdentity} from '@core/route-identity.ts';
import {knownServerRoutes} from '@core/app-sessions.ts';
import {connectPairedServer, inspectDirectServer} from '@core/server-connections.ts';
import {parseServerSignIn} from '@core/sign-in.ts';
import type {SetupDraftStore} from '@core/setup-onboarding.ts';
import {browserAppSessions, browserConnectionEnvironment} from '../../platform/server-connections';
import type {SetupPorts} from './setup-flow.ts';

export type BrowserSetupPortsInput = Readonly<{
  /** The protected journal is Security-owned; this screen only reads/writes its draft port. */
  draft: (serverId: string) => SetupDraftStore;
  acceptLocal: SetupPorts['acceptLocal'];
  continueHosted: SetupPorts['continueHosted'];
  resumeHosted?: SetupPorts['resumeHosted'];
  discardStaleDraft?: SetupPorts['discardStaleDraft'];
  /** The composition binds this to the admitted owner's authenticated API. */
  addLibrary: SetupPorts['addLibrary'];
  libraryFolders?: SetupPorts['libraryFolders'];
  complete: () => void;
  origin?: () => string;
}>;

/** The existing verified pairing for a server that has no account yet. The
 * protected setup journal and the Hosted continuation arrive through the ports
 * above; this module never guesses their protocol. */
export function createBrowserSetupPorts(input: BrowserSetupPortsInput): SetupPorts {
  const originOf = () => routeOrigin(input.origin?.() ?? location.origin);
  return Object.freeze({
    connect: async signal => {
      const origin = originOf();
      const pin = await inspectDirectServer(origin, browserConnectionEnvironment(), signal);
      const connected = await connectPairedServer(origin, pin, browserConnectionEnvironment(pin.serverId), signal);
      try {
        const server = parseServerSignIn(await connected.api.request(routePath('get_system'), 'GET', undefined, signal));
        if (server.id !== pin.serverId) throw Object.assign(new Error('The server identity changed.'), {code: 'identity_mismatch'});
        assertFirstContactIdentity(origin, pin, {knownRoutes: knownServerRoutes(await browserAppSessions().read())});
        return Object.freeze({server, api: connected.api, pin, draft: input.draft(server.id), dispose: () => connected.connection.dispose()});
      } catch (error) {
        connected.connection.dispose();
        throw error;
      }
    },
    acceptLocal: input.acceptLocal,
    continueHosted: input.continueHosted,
    ...(input.resumeHosted ? {resumeHosted: input.resumeHosted} : {}),
    ...(input.discardStaleDraft ? {discardStaleDraft: input.discardStaleDraft} : {}),
    addLibrary: input.addLibrary,
    ...(input.libraryFolders ? {libraryFolders: input.libraryFolders} : {}),
    complete: input.complete,
    hostname: () => location.hostname,
  });
}
