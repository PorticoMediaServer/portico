import {AppState, Linking} from 'react-native';
import {ApiError} from '../../../../packages/client-core/src/http';
import type {CredentialSessionService} from '../../../../packages/client-core/src/credentials';
import type {AccountScope} from '../../../../packages/client-core/src/hosted-security';
import {readPorticoAccount} from '../../../../packages/client-core/src/portico-services-native';
import {accountExpectation, nativeAccountRead} from './account-work';
import {hostedUrl} from './account-authority';
import {nativeNetworkFetch} from './server-connections';

/** The hosted reset page needs no app callback. On return, only Services can say this sign-in ended. */
export async function openNativeAccountPasswordReset(credentials: CredentialSessionService, scope: AccountScope): Promise<void> {
  const original = credentials.getSnapshot().session, captured = Object.freeze({...scope});
  const current = () => {const live = credentials.getSnapshot().session;
    return live?.account.id === captured.accountId && live.familyId === captured.familyId;};
  if (!original || !current()) throw new ApiError(409, 'account_changed', 'account_changed');
  let left = false, off = () => {}, remove = () => {};
  const request = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined;
  const returned = new Promise<void>(resolve => {
    const subscription = AppState.addEventListener('change', state => {
      if (state !== 'active') {left = true; return;}
      if (!left) return;
      remove();
      timer = setTimeout(() => request.abort(), 15000);
      // The ordinary read wrapper reconciles an ended session through its credential owner.
      // Cancellation, an unreachable Services or a successor account must never cause a sign-out here.
      void nativeAccountRead(credentials, issued => readPorticoAccount(issued.accessToken, hostedUrl, nativeNetworkFetch, request.signal),
        {expected: accountExpectation(original), signal: request.signal, isCurrent: current}).then(() => resolve(), () => resolve());
    });
    remove = () => subscription.remove();
    off = credentials.subscribe(() => {if (!current()) {remove(); resolve();}});
  });
  try {
    await Linking.openURL(hostedUrl + '/reset?next=%2Faccount%3Ftab%3Dsecurity');
    await returned;
  } finally {remove(); off(); request.abort(); if (timer) clearTimeout(timer);}
}
