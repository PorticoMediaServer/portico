import {routeOrigin} from '../../../../packages/client-core/src/route-identity';
import {hostedUrl} from './account-authority';

/** Where a TV sends Forgot PIN, as a QR and a typed address. A local server resets a PIN on its own
 * page. A Portico Accounts server's needs the Portico Account's password, checked by Portico Services,
 * which only web.getportico.tv can ask (owner's rule, 12 Nov): there the person opens this server from
 * their list, chooses the profile, and Forgot PIN? sets the new PIN, which the TV then takes. */
export function forgotPINHandoff(serverAddress: string, server: Readonly<{id: string; signIn: 'local' | 'portico'}>):
  Readonly<{url: string; body: 'next.auth.tv.forgotPINBody' | 'next.auth.tv.forgotPINPorticoBody'}> {
  if (server.signIn === 'portico') {
    const url = new URL('/servers', hostedUrl); url.searchParams.set('open', server.id);
    return Object.freeze({url: url.href, body: 'next.auth.tv.forgotPINPorticoBody'});
  }
  return Object.freeze({url: new URL('/settings/profile', routeOrigin(serverAddress)).href, body: 'next.auth.tv.forgotPINBody'});
}
