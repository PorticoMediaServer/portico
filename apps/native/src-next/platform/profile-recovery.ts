import {HttpLocalApi, type LocalSession} from '../../../../packages/client-core/src';
import {nativeNetworkFetch} from './server-connections';

/** The PIN recovery lifecycle is shared with web in client-core. Native supplies its
 * pinned temporary connection and the hosted account for Portico Account proof. */
export {
  ProfileRecoveryController as NativeProfileRecovery,
  type ProfileRecoveryPhase as NativeRecoveryPhase, type ProfileRecoveryState as NativeRecoveryState, type ProfileRecoveryPorts as NativeRecoveryPorts,
} from '../../../../packages/client-core/src/profile-recovery';

/** A fresh server-scoped API for the proof family; the caller's selected connection
 * and its pin are reused, never replaced. */
export function temporaryRecoveryApi(api: HttpLocalApi, session: LocalSession): HttpLocalApi {
  const temporary = new HttpLocalApi(api.baseUrl, session.accessToken, nativeNetworkFetch);
  const route = api.getRouteConnection();
  if (route) temporary.useRoutes(route);
  return temporary;
}

/** Forgot PIN's Portico step on the apps: the signed-in Portico Account's proof source (#3075) and
 * what the screen shows before anything is typed. */
export type NativePorticoRecovery = Readonly<{source: import('../../../../packages/client-core/src/profile-recovery').PorticoPINProofSource;
  account: Readonly<{username: string; hasPassword: boolean; twoStep: boolean}>}>;
export async function nativePorticoRecovery(): Promise<NativePorticoRecovery> {
  const {nativeAccounts} = await import('./accounts');
  const source = await nativeAccounts.porticoPINProof();
  return Object.freeze({source, account: await nativeAccounts.porticoRecoveryAccount(source)});
}
