import {HostedAccountClient} from '@core/hosted-security.ts';
import {browserAccount} from '../platform/account';
export {accountIdentity, sameAccount, currentAccountWork, currentAccountRead} from '../platform/account-work';
export type {AccountIdentity} from '../platform/account-work';

/** The Services root shares the browser's one cookie coordinator with the server app. */
export function servicesAccount() {
  const account = browserAccount();
  const client = new HostedAccountClient(account.api.origin, 'browser',
    (path, method, body, signal, token) => account.api.send(path, method, body, signal, token));
  return {...account, client};
}

export type ServicesAccount = ReturnType<typeof servicesAccount>;
