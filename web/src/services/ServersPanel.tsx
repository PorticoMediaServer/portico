import {useEffect, useMemo, useRef, useState} from 'react';
import {goTo} from '../platform/hosted-navigation';
import {encodedRoutePath, routeBody} from '../../../packages/contracts/src/hosted/routes.ts';
import {ServicesBadge, ServicesDangerAction, ServicesEmpty, ServicesGroup, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import {HostedError, ServicesQuiet} from '../kit/accounts/ServicesAuth';
import {servicesI18n} from './i18n';
import type {WorkspaceApi, WorkspaceServers} from './workspace-api';
import {servicesServerDirectoryCopy} from './server-directory';
import styles from './server-directory.module.css';
import servicesStyles from './services.module.css';

export function ServersPanel({api}: {api: WorkspaceApi}) {
  const i18n = useMemo(servicesI18n, []), {t} = i18n;
  const [servers, setServers] = useState<WorkspaceServers>(), [error, setError] = useState<unknown>();
  const [leaving, setLeaving] = useState<WorkspaceServers['items'][number]>(), [busy, setBusy] = useState(false);
  const [retrying, setRetrying] = useState(''), [seconds, setSeconds] = useState(0);
  const operation = useRef<AbortController | null>(null);
  const load = async (signal: AbortSignal) => {
    const value = await api.servers(signal);
    if (!signal.aborted) { setServers(value); setError(undefined); }
  };
  useEffect(() => {
    const controller = new AbortController(); operation.current = controller;
    void api.servers(controller.signal).then(value => { if (!controller.signal.aborted) setServers(value); }, value => { if (!controller.signal.aborted) setError(value); });
    return () => controller.abort();
  }, [api]);
  // While a server is offline, Portico checks again by itself; the drawn line
  // counts the real seconds down and every row keeps its own Try again.
  const items = servers?.items ?? [], offline = items.some(server => server.status !== 'active');
  // Connect 28 draws its calm explanation only when the account has servers and
  // none answers; one that is merely asleep is not an error.
  const allOffline = items.length > 0 && items.every(server => server.status !== 'active');
  const ownsOffline = allOffline && items.some(server => server.ownedByMe);
  useEffect(() => {
    if (!offline) { setSeconds(0); return; }
    setSeconds(30);
    const timer = setInterval(() => {
      setSeconds(current => {
        if (current > 1) return current - 1;
        const signal = operation.current?.signal;
        if (signal && !signal.aborted && !busy) void api.servers(signal).then(value => { if (!signal.aborted) setServers(value); }, () => undefined);
        return 30;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [offline, api, busy]);
  const retry = async (server: WorkspaceServers['items'][number]) => {
    const signal = operation.current?.signal;
    if (retrying || !signal || signal.aborted) return;
    setRetrying(server.id); setError(undefined);
    try { await load(signal); }
    catch (value) { if (!signal.aborted) setError(value); }
    finally { if (!signal.aborted) setRetrying(''); }
  };
  const leave = async () => {
    if (!leaving || leaving.ownedByMe || busy || !operation.current) return;
    const signal = operation.current.signal; setBusy(true); setError(undefined);
    try {
      await api.request(encodedRoutePath('server_leave', {id: encodeURIComponent(leaving.id)}), 'POST', routeBody('server_leave', {}), signal);
      const value = await api.servers(signal);
      if (!signal.aborted) { setServers(value); setLeaving(undefined); }
    } catch (value) { if (!signal.aborted) setError(value); }
    finally { if (!signal.aborted) setBusy(false); }
  };
  return <>{error ? <HostedError error={error} /> : null}
    {offline && seconds > 0 ? <p className={styles.recheck}>{t('web.hostedAccount.recheckingIn', {time: seconds})}</p> : null}
    {ownsOffline ? <p className={styles.offlineHint}>{t('web.hostedAccount.offlineOwnerHint')}</p> : null}
    {servers && <ServicesGroup>{servers.items.length ? servers.items.map(server => {
      const copy = servicesServerDirectoryCopy(server, i18n);
      return <ServicesWorkspaceRow key={server.id} icon="i-server" title={server.name} dim={!copy.online}
      help={copy.presence && <span className={copy.online ? styles.online : undefined}>{copy.presence}</span>}
      badge={copy.badge ? <ServicesBadge tone={copy.badge.tone}>{copy.badge.label}</ServicesBadge> : undefined}>
      {!copy.online ? <Button size="small" variant="glass" disabled={busy || Boolean(retrying)} loading={retrying === server.id} onClick={() => void retry(server)}>{t('next.states.tryAgain')}</Button> : null}
      {server.ownedByMe ? <ServicesQuiet>{t('web.hostedAccount.leaveOwner')}</ServicesQuiet> : <Button size="small" disabled={busy} onClick={() => setLeaving(server)}>{t('web.hostedAccount.leaveServer')}</Button>}
    </ServicesWorkspaceRow>;}) : <ServicesEmpty title={t('services.workspace.emptyServers')} description={t('services.workspace.emptyServersBody')}
      actions={<><Button size="small" variant="primary" onClick={() => goTo('/welcome#join')}>{t('services.start.join')}</Button><Button size="small" onClick={() => goTo('/welcome#setup')}>{t('services.start.setup')}</Button></>} />}</ServicesGroup>}
    {allOffline ? <ServicesGroup title={t('web.hostedAccount.accountActions')}>
      <ServicesWorkspaceRow icon="i-person" title={t('web.hostedAccount.stillWorksAccount')} help={t('web.hostedAccount.stillWorksAccountBody')}>
        <Button size="small" variant="glass" icon="i-right" iconOnly aria-label={t('web.hostedAccount.stillWorksAccount')} onClick={() => goTo('/account')} />
      </ServicesWorkspaceRow>
      <ServicesWorkspaceRow icon="i-link" title={t('web.hostedAccount.stillWorksJoin')} help={t('web.hostedAccount.stillWorksJoinBody')}>
        <Button size="small" variant="glass" icon="i-right" iconOnly aria-label={t('web.hostedAccount.stillWorksJoin')} onClick={() => goTo('/welcome#join')} />
      </ServicesWorkspaceRow>
      <ServicesWorkspaceRow icon="i-server" title={t('web.hostedAccount.stillWorksSetup')} help={t('web.hostedAccount.stillWorksSetupBody')}>
        <Button size="small" variant="glass" icon="i-right" iconOnly aria-label={t('web.hostedAccount.stillWorksSetup')} onClick={() => goTo('/welcome#setup')} />
      </ServicesWorkspaceRow>
    </ServicesGroup> : null}
    <Dialog open={Boolean(leaving)} onOpenChange={open => { if (!open && !busy) setLeaving(undefined); }} className={servicesStyles.overlaySpacing} title={t('web.hostedAccount.leaveTitle', {server: leaving?.name ?? ''})}
      description={t('web.hostedAccount.leaveBody', {server: leaving?.name ?? ''})} showCloseButton={false} stackFooter footer={<><Button size="small" disabled={busy} onClick={() => setLeaving(undefined)}>{t('action.cancel')}</Button><ServicesDangerAction loading={busy} onClick={() => void leave()}>{t('web.hostedAccount.leaveServer')}</ServicesDangerAction></>}><></></Dialog>
  </>;
}
