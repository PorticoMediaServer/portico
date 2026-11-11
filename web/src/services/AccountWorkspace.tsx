import {useEffect, useState, useSyncExternalStore} from 'react';
import {ServicesActions, ServicesAuthStage, ServicesHeading, ServicesLead} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {servicesI18n} from './i18n';
import {accountSession, personCall, signOut} from './account-session';
import {AccountSignIn} from './AccountSignIn';

type Account = Readonly<{username: string; displayName: string; email: string}>;

export function AccountWorkspace() {
  const {t} = servicesI18n();
  const signedIn = useSyncExternalStore(accountSession.subscribe, accountSession.signedIn);
  const [account, setAccount] = useState<Account>();
  useEffect(() => { if (signedIn) void personCall<Account>('GET', '/v1/account').then(setAccount, () => undefined); }, [signedIn]);
  if (!signedIn) return <AccountSignIn next="/account" />;
  return <ServicesAuthStage>
    <ServicesHeading>{account ? account.displayName || account.username : ''}</ServicesHeading>
    <ServicesLead>{account?.email}</ServicesLead>
    <ServicesActions><Button onClick={() => void signOut()}>{t('action.signOut')}</Button></ServicesActions>
  </ServicesAuthStage>;
}
