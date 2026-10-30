import {HostedError, ServicesQuiet} from '../kit/accounts/ServicesAuth';
import {useCallback, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode} from 'react';
import type {HostedSession} from '@core/index.ts';
import {ProfileAvatar} from '../kit/shell/AccountSurfaces';
import {ServicesHeader, ServicesWorkspace, ServicesWorkspaceActions, type ServicesSection} from '../kit/accounts/ServicesWorkspace';
import {goTo} from '../platform/hosted-navigation';
import {Button} from '../kit/controls';
import {DevicesPanel} from './DevicesPanel';
import {FeedPanel} from './FeedPanel';
import {ServersPanel} from './ServersPanel';
import {SecurityPanel} from './SecurityPanel';
import {ServicesFeed} from './feed';
import {workspaceApi, type WorkspaceApi} from './workspace-api';
import {useAccountResource} from './use-account-resource';
import {accountIdentity, type ServicesAccount} from './session';
import {servicesI18n} from './i18n';

export function AccountPage({account, session, section, identityTransaction, onDeleted, onSecurityEnded}: {account: ServicesAccount; session: Omit<HostedSession, 'refreshToken'>; section: ServicesSection; identityTransaction?: string; onDeleted?: () => void; onSecurityEnded?: (codes?: readonly string[]) => void}) {
  const identity = accountIdentity(session);
  return <BoundAccount key={identity.accountId + '/' + identity.familyId} account={account} session={session} section={section} identityTransaction={identityTransaction} onDeleted={onDeleted} onSecurityEnded={onSecurityEnded} />;
}
function BoundAccount({account, session, section, identityTransaction, onDeleted, onSecurityEnded}: {account: ServicesAccount; session: Omit<HostedSession, 'refreshToken'>; section: ServicesSection; identityTransaction?: string; onDeleted?: () => void; onSecurityEnded?: (codes?: readonly string[]) => void}) {
  const {t} = useMemo(servicesI18n, []);
  const accountId = session.account.id, familyId = session.familyId!;
  const identity = useMemo(() => ({accountId, familyId}), [accountId, familyId]);
  const api = useMemo(() => workspaceApi(account, identity), [account, identity]);
  const create = useCallback(() => new ServicesFeed(api.notifications), [api]), bound = useAccountResource(create);
  return bound ? <ReadyAccount key={bound.id} account={account} session={session} section={section} identityTransaction={identityTransaction} onDeleted={onDeleted} onSecurityEnded={onSecurityEnded} api={api} feed={bound.value}/>
    : <ServicesQuiet>{t('status.loading')}</ServicesQuiet>;
}
function ReadyAccount({account, session, section, identityTransaction, onDeleted, onSecurityEnded, api, feed}: {
  account: ServicesAccount; session: Omit<HostedSession, 'refreshToken'>; section: ServicesSection; identityTransaction?: string; onDeleted?: () => void; onSecurityEnded?: (codes?: readonly string[]) => void; api: WorkspaceApi; feed: ServicesFeed;
}) {
  const {t} = useMemo(servicesI18n, []);
  const accountId = session.account.id, familyId = session.familyId!;
  const identity = useMemo(() => ({accountId, familyId}), [accountId, familyId]);
  const state = useSyncExternalStore(feed.subscribe, feed.getSnapshot);
  const [error, setError] = useState<unknown>();
  const [contact, setContact] = useState<string>();
  useEffect(() => { void feed.refresh(); }, [feed]);
  useEffect(() => {
    const controller = new AbortController();
    void api.security(controller.signal).then(value => { if (!controller.signal.aborted) setContact(value.contactAddress || undefined); }, () => {});
    return () => controller.abort();
  }, [api]);
  const name = session.account.displayName || session.account.username, initial = [...name][0]?.toLocaleUpperCase() ?? '';
  // Sections change in place: the workspace (header, sidebar, backdrop) stays mounted and only
  // the pane swaps (H14). Every section renders inside the one workspace below.
  const select = (tab: ServicesSection) => goTo('/account?tab=' + tab);
  const signOut = () => { void account.service.logoutIfCurrent(identity).catch(setError); };
  const header = <ServicesHeader unread={state.unread > 0} avatar={<ProfileAvatar appearance="session" initial={initial} name={name} onClick={() => select('devices')} />} />;
  const devicesFrame = (children: ReactNode, actions: ReactNode) => <>{children}{actions ? <ServicesWorkspaceActions>{actions}</ServicesWorkspaceActions> : null}</>;
  const pane = section === 'devices' ? <DevicesPanel api={api} frame={devicesFrame} />
    : section === 'notifications' ? <FeedPanel feed={feed} onReviewDevices={() => select('devices')} />
      : section === 'security' ? <SecurityPanel identityTransaction={identityTransaction} account={account} api={api} identity={identity} username={session.account.username} onDeleted={onDeleted} onSecurityEnded={onSecurityEnded} />
        : <ServersPanel api={api} />;
  const actions = section === 'notifications' ? <Button size="small" icon="i-check" disabled={state.busy || !state.unread} onClick={() => void feed.markRead('all')}>{t('web.account.notif.markAll')}</Button> : undefined;
  const description = section === 'notifications' ? undefined : t(section === 'devices' ? 'services.workspace.devicesBody' : section === 'security' ? 'services.workspace.securityBody' : 'web.hostedAccount.serversHelp');
  return <ServicesWorkspace header={header} account={{name, email: contact, initial}} section={section}
    onSection={select} onSignOut={signOut} actions={actions} unread={state.unread} description={description}>{error ? <HostedError error={error} /> : null}{pane}</ServicesWorkspace>;
}
