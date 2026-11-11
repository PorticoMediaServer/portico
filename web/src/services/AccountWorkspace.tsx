import {useCallback, useEffect, useState, useSyncExternalStore, type ReactNode} from 'react';
import {ServicesHeader, ServicesWorkspace, type ServicesSection} from '../kit/accounts/ServicesWorkspace';
import {ServicesAuthStage, ServicesActions, ServicesHeading, ServicesNotice} from '../kit/accounts/ServicesAuth';
import {ProfileAvatarFace} from '../kit/shell/AccountSurfaces';
import {Button} from '../kit/controls';
import {goTo} from '../platform/hosted-navigation';
import {servicesI18n} from './i18n';
import {accountSession, personCall, signOut} from './account-session';
import {problemMessage} from './account-problem';
import {AccountSignIn} from './AccountSignIn';
import {AccountDevices} from './AccountDevices';
import {AccountServers} from './AccountServers';
import {AccountSecurity} from './AccountSecurity';

export type Account = Readonly<{
  id: string; email: string; newEmailWaiting?: string; username: string; displayName: string;
  hasPassword: boolean; providers: readonly string[]; twoStep: boolean; ownsServer: boolean;
}>;
const shown: readonly ServicesSection[] = ['devices', 'security', 'servers'];
const sectionOf = (search: string): ServicesSection => {
  const tab = new URLSearchParams(search).get('tab');
  return tab === 'security' || tab === 'servers' ? tab : 'devices';
};

/** `/account`: the signed-in person's Portico Account. Devices, Security and Servers. */
export function AccountWorkspace() {
  const signedIn = useSyncExternalStore(accountSession.subscribe, accountSession.signedIn);
  if (!signedIn) return <AccountSignIn next={location.pathname + location.search} />;
  return <SignedInWorkspace />;
}

function SignedInWorkspace() {
  const {t} = servicesI18n();
  const [account, setAccount] = useState<Account>(), [error, setError] = useState<unknown>(), [section, setSection] = useState(() => sectionOf(location.search));
  const reload = useCallback(async () => {
    try { setAccount(await personCall<Account>('GET', '/v1/account')); setError(undefined); } catch (caught) { setError(caught); }
  }, []);
  useEffect(() => { void reload(); }, [reload]);
  useEffect(() => {
    const changed = () => setSection(sectionOf(location.search));
    window.addEventListener('popstate', changed);
    return () => window.removeEventListener('popstate', changed);
  }, []);
  const open = (next: ServicesSection) => { setSection(next); goTo(`/account?tab=${next}`, {replace: true}); };
  if (error && !account) return <ServicesAuthStage variant="state">
    <ServicesHeading>{t('next.account.workspace.failed')}</ServicesHeading>
    <ServicesNotice tone="error">{t(problemMessage(error, 'account'))}</ServicesNotice>
    <ServicesActions><Button onClick={() => void reload()}>{t('next.account.problem.tryAgain')}</Button></ServicesActions>
  </ServicesAuthStage>;
  if (!account) return <ServicesAuthStage><></></ServicesAuthStage>;
  const name = account.displayName || account.username;
  const initial = Array.from(name)[0]?.toLocaleUpperCase() ?? '';
  const lead: Record<ServicesSection, ReactNode> = {devices: t('next.account.workspace.devicesLead'), security: t('next.account.workspace.securityLead'), servers: t('next.account.workspace.serversLead'), notifications: null};
  return <ServicesWorkspace header={<ServicesHeader notifications={false} avatar={<ProfileAvatarFace initial={initial} size={40} />} />}
    account={{name, email: account.email, initial}} section={section} onSection={open} shown={shown} onSignOut={() => void signOut()} description={lead[section]}>
    {section === 'devices' ? <AccountDevices /> : section === 'servers' ? <AccountServers /> : <AccountSecurity account={account} reload={reload} />}
  </ServicesWorkspace>;
}
