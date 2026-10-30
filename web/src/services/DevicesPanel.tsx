import {HostedError, ServicesActions, ServicesQuiet, ServicesNotice} from '../kit/accounts/ServicesAuth';
import {useCallback, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode} from 'react';
import {accountSessionDevice, type AccountSessionReviewService} from '@core/account-sessions.ts';
import {ServicesBadge, ServicesDangerAction, ServicesDeviceFacts, ServicesGroup, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import type {IconName} from '../kit/Icon';
import {servicesI18n} from './i18n';
import type {WorkspaceApi} from './workspace-api';
import {servicesRelativeTime, servicesSignInDate} from './time';
import styles from './services.module.css';
import {useAccountResource} from './use-account-resource';

type Props = {api: WorkspaceApi; frame: (children: ReactNode, actions: ReactNode) => ReactNode};
export function DevicesPanel({api, frame}: Props) {
  const create = useCallback(() => api.sessions(), [api]), bound = useAccountResource(create);
  return bound ? <BoundDevicesPanel key={bound.id} frame={frame} service={bound.value}/>
    : frame(<ServicesQuiet>{servicesI18n().t('status.loading')}</ServicesQuiet>, null);
}
function BoundDevicesPanel({frame, service}: Pick<Props, 'frame'> & {service: AccountSessionReviewService}) {
  const i18n = useMemo(servicesI18n, []), {t} = i18n;
  const state = useSyncExternalStore(service.subscribe, service.getSnapshot);
  const [error, setError] = useState<unknown>();
  const [ending, setEnding] = useState<string>(), [all, setAll] = useState(false);
  useEffect(() => { void service.refresh(); }, [service]);
  const active = state.items.filter(item => item.status === 'active');
  const name = (id: string) => { const row = state.items.find(item => item.id === id); return row ? accountSessionDevice(row).name : ''; };
  const busy = state.pending !== null || state.endingOthers;
  // While an ambiguous revoke retains its exact family or operation, ordinary revokes
  // stay closed and only the authored retry can settle it. Read-only paging stays available.
  const locked = busy || Boolean(state.retryId) || state.retryOthers;
  const retryRevoke = async () => {
    const snapshot = service.getSnapshot();
    if (!state.retryId && !state.retryOthers) return;
    // Only this opening's retained intent may be retried; a newer ambiguity owns its own.
    if (snapshot.retryId !== state.retryId || snapshot.retryOthers !== state.retryOthers) return;
    try { await service.retryRevoke(); } catch (value) { setError(value); }
  };
  const revoke = (id: string) => { try { void service.revoke(id).catch(setError); } catch (value) { setError(value); } };
  const endOthers = () => {
    setAll(false); setError(undefined);
    try { void service.revokeOthers().catch(setError); } catch (value) { setError(value); }
  };
  const actions = <Button size="small" disabled={locked || !active.some(item => !item.current) && !state.nextCursor} onClick={() => setAll(true)}>{t('web.hostedAccount.signOutOthers')}</Button>;
  return frame(<>
    {error ? <HostedError error={error} /> : null}{state.error ? <HostedError error={state.error} /> : null}{state.mutationError ? <HostedError error={state.mutationError} /> : null}
    {(state.retryId || state.retryOthers) && <ServicesActions><Button size="small" disabled={busy} onClick={() => void retryRevoke()}>{t('next.states.tryAgain')}</Button></ServicesActions>}
    {state.notice && <ServicesNotice tone="info">{state.notice}</ServicesNotice>}
    {active.length > 0 && <ServicesGroup>{active.map(item => {
      const device = accountSessionDevice(item);
      const icon: IconName | undefined = device.form === 'phone' ? 'i-phone' : device.form === 'tv' ? 'i-tv' : device.form === 'computer' ? 'i-laptop' : undefined;
      const app = device.app ?? '';
      const signedIn = t('web.hostedAccount.signedInOn', {date: servicesSignInDate(i18n, item.createdAt)});
      const lastUsed = item.lastUsedAt ? t('web.hostedAccount.lastUsed', {when: servicesRelativeTime(i18n, item.lastUsedAt)}) : '';
      const phoneApp = item.platform === 'tvos' ? t('services.platform.tvos') : item.platform === 'androidtv' ? t('services.platform.androidtv') : item.platform === 'web' ? '' : app;
      const help = <ServicesDeviceFacts desktop={[app, signedIn, lastUsed]} phone={[item.current ? signedIn : phoneApp, lastUsed]} />;
      return <ServicesWorkspaceRow key={item.id} icon={icon} title={device.name} help={help} badge={item.current ? <ServicesBadge tone="accent">{t('web.hostedAccount.thisBrowser')}</ServicesBadge> : undefined}>
        {!item.current && <Button size="small" loading={state.pending === item.id} disabled={locked} onClick={() => setEnding(item.id)}>{t('action.signOut')}</Button>}
      </ServicesWorkspaceRow>;
    })}</ServicesGroup>}
    {(state.hasPrevious || state.nextCursor) && <div className={styles.pager}><Button size="small" disabled={busy || !state.hasPrevious} onClick={() => void service.previous()}>{t('web.hostedAccount.newer')}</Button><Button size="small" disabled={busy || !state.nextCursor} onClick={() => void service.next()}>{t('web.hostedAccount.older')}</Button></div>}
    <Dialog open={Boolean(ending)} onOpenChange={open => { if (!open) setEnding(undefined); }} title={t('web.hostedAccount.signOutDeviceTitle', {device: ending ? name(ending) : ''})} description={t('web.hostedAccount.signOutBody')} showCloseButton={false} stackFooter className={styles.overlaySpacing}
      footer={<><Button size="small" onClick={() => setEnding(undefined)}>{t('action.cancel')}</Button><ServicesDangerAction onClick={() => { const id = ending!; setEnding(undefined); revoke(id); }}>{t('action.signOut')}</ServicesDangerAction></>}><></></Dialog>
    <Dialog open={all} onOpenChange={open => { if (!open) setAll(false); }} title={t('web.hostedAccount.signOutOthersTitle')} description={t('web.hostedAccount.signOutOthersBody')} showCloseButton={false} stackFooter className={styles.overlaySpacing}
      footer={<><Button size="small" onClick={() => setAll(false)}>{t('action.cancel')}</Button><ServicesDangerAction onClick={endOthers}>{t('web.hostedAccount.signOutOthers')}</ServicesDangerAction></>}><></></Dialog>
  </>, actions);
}
