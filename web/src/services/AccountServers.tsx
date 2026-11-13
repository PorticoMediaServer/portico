import {useCallback, useEffect, useState} from 'react';
import {ServicesBadge, ServicesEmpty, ServicesGroup, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {ServicesNotice} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import {servicesI18n} from './i18n';
import {account} from './account-session';
import type {ListedServer as Listed} from '@core/portico-account.ts';
import {problemMessage} from './account-problem';

/** The account's servers, from its signed server list. A member can leave one; the owner can
 * remove one that is gone. Opening a server is the server chooser's job, not this page's. */
export function AccountServers() {
  const {t} = servicesI18n();
  const [servers, setServers] = useState<readonly Listed[]>(), [error, setError] = useState<unknown>();
  const [asking, setAsking] = useState<Listed>(), [busy, setBusy] = useState(false), [dialogError, setDialogError] = useState<unknown>();
  const load = useCallback(async () => {
    try {
      setServers(await account.servers());
    } catch (caught) { setError(caught); }
  }, []);
  useEffect(() => { void load(); }, [load]);
  const confirm = async () => {
    if (!asking) return;
    setBusy(true); setDialogError(undefined);
    try {
      await (asking.owner ? account.removeServer(asking.serverId) : account.leaveServer(asking.serverId));
      setAsking(undefined);
      await load();
    } catch (caught) { setDialogError(caught); } finally { setBusy(false); }
  };
  const role = (server: Listed) => t(server.owner ? 'next.account.servers.owner' : server.role === 'admin' ? 'next.account.servers.admin' : 'next.account.servers.member');
  const server = asking?.name ?? '';
  return <>
    {error ? <ServicesNotice tone="error">{t(problemMessage(error, 'account'))}</ServicesNotice> : null}
    {servers && servers.length === 0 && <ServicesEmpty title={t('next.account.servers.empty')} description={t('next.account.servers.emptyBody')} />}
    {servers && servers.length > 0 && <ServicesGroup>{servers.map(item =>
      <ServicesWorkspaceRow key={item.serverId} icon="i-server" title={item.name} badge={<ServicesBadge tone={item.owner ? 'accent' : 'default'}>{role(item)}</ServicesBadge>}>
        <Button size="small" onClick={() => { setDialogError(undefined); setAsking(item); }}>{t(item.owner ? 'next.account.servers.remove' : 'next.account.servers.leave')}</Button>
      </ServicesWorkspaceRow>)}
    </ServicesGroup>}
    <Dialog footerDismisses open={asking !== undefined} onOpenChange={open => { if (!open && !busy) setAsking(undefined); }} size="sm"
      title={t(asking?.owner ? 'next.account.servers.removeTitle' : 'next.account.servers.leaveTitle', {server})}
      description={t(asking?.owner ? 'next.account.servers.removeBody' : 'next.account.servers.leaveBody', {server})}
      footer={<><Button size="small" disabled={busy} onClick={() => setAsking(undefined)}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={busy} onClick={() => void confirm()}>{t(asking?.owner ? 'next.account.servers.remove' : 'next.account.servers.leave')}</Button></>}>
      {dialogError ? <ServicesNotice tone="error">{t(problemMessage(dialogError, 'account'))}</ServicesNotice> : null}
    </Dialog>
  </>;
}
