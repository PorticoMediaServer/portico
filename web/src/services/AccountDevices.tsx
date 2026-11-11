import {useCallback, useEffect, useState} from 'react';
import {ServicesBadge, ServicesGroup, ServicesGroupNote, ServicesWorkspaceRow} from '../kit/accounts/ServicesWorkspace';
import {ServicesNotice} from '../kit/accounts/ServicesAuth';
import {Button} from '../kit/controls';
import {servicesI18n} from './i18n';
import {personCall, signOut} from './account-session';
import {problemMessage} from './account-problem';

type Device = Readonly<{id: string; name: string; kind: string; createdAt: string; lastUsedAt: string; current: boolean}>;

/** Where the account is signed in, with a way to sign any of them out. */
export function AccountDevices() {
  const {t, date} = servicesI18n();
  const [devices, setDevices] = useState<readonly Device[]>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState<string>(), [done, setDone] = useState(false);
  const load = useCallback(async () => {
    try { setDevices((await personCall<{items: Device[]}>('GET', '/v1/account/sessions')).items); } catch (caught) { setError(caught); } // query-read: kept (Portico Services account pages: the person's Portico Account, not a media server document)
  }, []);
  useEffect(() => { void load(); }, [load]);
  const act = async (id: string, work: () => Promise<unknown>) => {
    setBusy(id); setError(undefined); setDone(false);
    try { await work(); await load(); } catch (caught) { setError(caught); } finally { setBusy(undefined); }
  };
  const when = (value: string) => date(value, 'medium');
  const others = devices?.filter(device => !device.current) ?? [];
  return <>
    {error ? <ServicesNotice tone="error">{t(problemMessage(error, 'account'))}</ServicesNotice> : null}
    {done ? <ServicesNotice tone="success">{t('next.account.devices.othersDone')}</ServicesNotice> : null}
    {devices && <ServicesGroup actions={others.length > 0 ? <Button size="small" loading={busy === 'others'} disabled={busy !== undefined} onClick={() => void act('others', async () => { await personCall('DELETE', '/v1/account/sessions'); setDone(true); })}>{t('next.account.devices.signOutOthers')}</Button> : undefined}>
      {[...devices].sort((a, b) => Number(b.current) - Number(a.current) || b.lastUsedAt.localeCompare(a.lastUsedAt)).map(device =>
        <ServicesWorkspaceRow key={device.id} icon={device.kind === 'tv' ? 'i-tv' : device.kind === 'phone' ? 'i-phone' : 'i-laptop'}
          title={device.name || device.kind} badge={device.current ? <ServicesBadge tone="accent">{t('next.account.devices.thisDevice')}</ServicesBadge> : undefined}
          help={t('next.account.devices.lastUsed', {when: when(device.lastUsedAt)})}>
          <Button size="small" loading={busy === device.id} disabled={busy !== undefined}
            onClick={() => void (device.current ? signOut() : act(device.id, () => personCall('DELETE', `/v1/account/sessions/${encodeURIComponent(device.id)}`)))}>{t('next.account.devices.signOut')}</Button>
        </ServicesWorkspaceRow>)}
    </ServicesGroup>}
    {devices && others.length === 0 && <ServicesGroupNote><p>{t('next.account.devices.none')}</p></ServicesGroupNote>}
  </>;
}
