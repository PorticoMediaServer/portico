import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {AccessDevice} from '@core/server-administration.ts';
import {devicePlatform} from '@core/server-admin/people-words.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Dialog} from '../../kit/overlays/Dialog';
import {SettingsGroup} from '../../kit/settings';
import {profileDeviceIcon} from './account-profile/device-icon';
import type {PeopleDevicesApi} from './people-devices-source';
import styles from './people-devices.module.css';

const t = defaultI18n.t;
// The person page's Devices row names the action and its confirmation; the server-wide list says the same.
const devicesRow = settingsRowIndex.get('people.devices')!.row;
type State = Readonly<{items: readonly AccessDevice[]; nextCursor: string; loading: boolean; loaded: boolean; error?: unknown}>;

/** owner-people 24a's Devices tab on a phone: every device signed in to the server, with whose it is, when it was
 * last seen, and Sign out for that one device (on its own account). A person's page keeps its own Devices list. */
export function PeopleDevicesAll({api, isCurrent, canSignOut, onTotal, onError}: Readonly<{api: PeopleDevicesApi; isCurrent: () => boolean; canSignOut: boolean;
  onTotal?: (total: number | undefined) => void; onError: (error: unknown) => void}>) {
  const [state, setState] = useState<State>({items: [], nextCursor: '', loading: false, loaded: false});
  const [target, setTarget] = useState<AccessDevice>(), [busy, setBusy] = useState(false);
  const request = useRef<AbortController | undefined>(undefined), trigger = useRef<HTMLButtonElement | null>(null);
  const load = (cursor = '') => {
    request.current?.abort(); const controller = request.current = new AbortController();
    setState(current => ({...current, loading: true, error: undefined}));
    void api.list('', cursor, controller.signal).then(page => {
      if (controller.signal.aborted || !isCurrent()) return;
      setState(current => ({items: cursor ? [...current.items, ...page.items] : page.items, nextCursor: page.nextCursor, loading: false, loaded: true}));
    }, error => {if (!controller.signal.aborted && isCurrent()) setState(current => ({...current, loading: false, error}));});
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {load(); return () => request.current?.abort();}, [api]);
  useEffect(() => {onTotal?.(state.loaded && !state.nextCursor ? state.items.length : undefined);}, [onTotal, state.loaded, state.nextCursor, state.items.length]);
  const confirm = async () => {
    const device = target; if (!device || busy || !isCurrent()) return;
    setBusy(true);
    try {await api.signOut(device.accountId, device.id, new AbortController().signal); if (isCurrent()) {setTarget(undefined); load();}}
    catch (error) {if (isCurrent()) onError(error);}
    finally {setBusy(false);}
  };
  return <SettingsGroup plain>
    {state.loading && !state.loaded ? <p role="status">{t('next.settings.loading')}</p> : null}
    {state.error ? <div role="alert"><p>{t('next.states.pageFailedBody')}</p><Button size="small" disabled={state.loading} onClick={() => load()}>{t('action.tryAgain')}</Button></div> : null}
    <ul className={styles.devices} aria-label={t('settings.section.devices')}>{state.items.map(device => <li key={device.id}>
      <span className={styles.icon}><Icon name={profileDeviceIcon(device.platform)} size={22} /></span>
      <span className={styles.name}><b>{device.name || device.app}</b><small>{[device.username, devicePlatform(t, device.platform), device.profileName].filter(Boolean).join(' · ')}</small>
        <small>{new Intl.DateTimeFormat(undefined, {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(device.lastSeenAt))}</small></span>
      <Button size="small" disabled={!canSignOut || busy} onClick={event => {trigger.current = event.currentTarget; setTarget(device);}}>{t(devicesRow.control.itemAction!.label)}</Button>
    </li>)}</ul>
    {state.nextCursor && <Button size="small" loading={state.loading} onClick={() => load(state.nextCursor)}>{t('next.settings.loadMore')}</Button>}
    {target && <Dialog open size="sm" title={t('confirm.signOutDevice.title', {device: target.name || target.app})} description={t(devicesRow.control.itemAction!.confirm!)} returnFocusRef={trigger}
      onOpenChange={open => {if (!open && !busy) setTarget(undefined);}} dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy}
      footer={<><Button size="small" disabled={busy} onClick={() => setTarget(undefined)}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={busy} onClick={() => void confirm()}>{t(devicesRow.control.itemAction!.label)}</Button></>}>{null}</Dialog>}
  </SettingsGroup>;
}
