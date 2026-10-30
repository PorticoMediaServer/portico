import {useMemo, useState, useSyncExternalStore} from 'react';
import {goTo} from '../platform/hosted-navigation';
/** After joining: a Portico Account server opens from the account's chooser, here at this same
 * address (H14). A local-account server keeps its own web app at its own address. */
const joined = (destination: Readonly<{origin: string; signIn: 'local' | 'portico'}>) => {
  if (destination.signIn === 'portico') goTo('/servers'); else location.assign(destination.origin);
};
import type {JoinLink} from '@core/join-link.ts';
import {AccountInvitationScreen} from '../screens/accounts/AccountInvitation';
import {ShortInvitationEntry} from '../screens/accounts/ShortInvitationEntry';
import {HostedError} from '../kit/accounts/ServicesAuth';
import {Dialog} from '../kit/overlays';
import {ServicesAuthForm} from './AuthForm';
import {servicesInvitationAccount} from './invitation-account';
import {servicesI18n} from './i18n';
import {accountIdentity, type ServicesAccount} from './session';
import dialogStyles from './invitation-dialog.module.css';

/** The link exists only in this document. Provider navigation requires reopening it. */
export function JoinPage({account, link, onBack}: {account: ServicesAccount; link: JoinLink; onBack: () => void}) {
  const session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const current = useMemo(() => session ? servicesInvitationAccount(account, session) : undefined, [account, session]);
  const [error, setError] = useState<unknown>();
  const different = () => {
    if (session) void account.service.logoutIfCurrent(accountIdentity(session)).catch(setError);
  };
  return <AccountInvitationScreen link={link} account={current} onBack={onBack} onDifferentAccount={different}
    signedOutContent={<><HostedError error={error} /><ServicesAuthForm account={account} embedded invitation onSignedIn={() => {}} /></>}
    // The canonical invitation controller has retired its temporary Server grant.
    // This opens the proved origin for fresh admission; no credential crosses origins.
    onJoined={joined} />;
}

/** The typed address/code step. It carries only a verified destination out,
 * exactly as JoinPage does; no credential or long link is serialized. */
export function ShortJoinPage({account, onBack, initialCode}: {account: ServicesAccount; onBack: () => void; initialCode?: string}) {
  const session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const current = useMemo(() => session ? servicesInvitationAccount(account, session) : undefined, [account, session]);
  const [error, setError] = useState<unknown>();
  const different = () => {
    if (session) void account.service.logoutIfCurrent(accountIdentity(session)).catch(setError);
  };
  return <ShortInvitationEntry initialCode={initialCode} account={current} onBack={onBack} onDifferentAccount={different}
    signedOutContent={<><HostedError error={error} /><ServicesAuthForm account={account} embedded invitation onSignedIn={() => {}} /></>}
    onJoined={joined} />;
}

/** Connect 27: the same canonical invitation screen, drawn as the dialog the start
 * page opens on a pasted link. Inspection, account switching and the journal stay
 * in AccountInvitationScreen; only the surrounding presentation is a Dialog. */
export function ServicesInvitationDialog({account, link, pasted, onBack, onUnavailable}: {account: ServicesAccount; link: JoinLink; pasted: string; onBack: () => void; onUnavailable?: () => void}) {
  const {t} = useMemo(servicesI18n, []);
  const session = useSyncExternalStore(account.service.subscribe, account.service.getSnapshot).session;
  const current = useMemo(() => session ? servicesInvitationAccount(account, session) : undefined, [account, session]);
  const [error, setError] = useState<unknown>();
  const different = () => { if (session) void account.service.logoutIfCurrent(accountIdentity(session)).catch(setError); };
  // The drawn dialog carries its own Join and Cancel row; the surface adds no footer.
  return <Dialog open onOpenChange={open => { if (!open) onBack(); }} title={t('services.start.join')} description={t('services.start.joinLede')}>
    <p className={dialogStyles.link} title={pasted}>{pasted}</p>
    <AccountInvitationScreen presentation="dialog" onUnavailable={onUnavailable} link={link} account={current} onBack={onBack} onDifferentAccount={different}
      signedOutContent={<><HostedError error={error} /><ServicesAuthForm account={account} embedded invitation onSignedIn={() => {}} /></>}
      onJoined={joined} />
  </Dialog>;
}
