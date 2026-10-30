import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {Device} from '@core/identity-devices.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import {Icon} from '../../../kit/Icon';
import type {ProfileDevicesSource} from './devices';
import styles from './profile.module.css';
import deviceStyles from './profile-devices.module.css';
import {profileDeviceIcon} from './device-icon';
const t = defaultI18n.t;
export function ProfileDevices({source, isCurrent, onFailure, signOut}: {source: ProfileDevicesSource; isCurrent: () => boolean; onFailure: () => void; signOut: () => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [target, setTarget] = useState<Device>();
  const active = useRef<Device | undefined>(undefined), pending = useRef(false), live = useRef(true), trigger = useRef<HTMLElement | null>(null);
  useEffect(() => {live.current = true;void source.load();return () => {live.current = false;active.current = undefined;};}, [source]);
  useEffect(() => {if (state.error && isCurrent()) onFailure();}, [state.error, isCurrent, onFailure]);
  const finish = () => {active.current = undefined;setTarget(undefined);};
  const close = () => {if (!pending.current) finish();};
  const run = async (captured: Device) => {
    if (!live.current || !isCurrent() || active.current !== captured || pending.current) return;
    pending.current = true;
    try {
      await source.signOut(captured);
      if (live.current && isCurrent() && active.current === captured) {finish();if (captured.current) signOut();}
    } catch {if (live.current && isCurrent()) {finish();if (!source.getSnapshot().error) onFailure();}}
    finally {pending.current = false;}
  };
  if (!isCurrent() || !state.devices) return null;
  return <>
    <ul className={styles.profiles} aria-label={t('next.settingsSpec.account.profile.groups.6.rows.0.label')}>
      {state.devices.filter(device => device.sessions > 0).map(device => <li key={device.id}>
        <span className={styles.profileIcon}><Icon name={profileDeviceIcon(device.platform)} size={20} /></span>
        <span className={styles.profileName}><span className={deviceStyles.name}><b>{device.name}</b>{device.current && <span className={deviceStyles.current}>{t('device.thisDevice')}</span>}</span><small>{device.app}</small></span>
        <Button size="xs" variant="glass" disabled={state.busy} onClick={event => {if (!live.current || !isCurrent() || pending.current) return;trigger.current = event.currentTarget;active.current = device;setTarget(device);}}>{t('accounts.signOut.server')}</Button>
      </li>)}
    </ul>
    {target && <Dialog open children={null} size="sm" returnFocusRef={trigger} title={t('confirm.signOutDevice.title', {device: target.name})}
      description={t('next.settingsSpec.account.profile.groups.6.rows.0.control.itemAction.confirm')} dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy}
      onOpenChange={value => {if (!value && !pending.current) close();}} footer={<><Button size="small" disabled={state.busy} onClick={close}>{t('action.cancel')}</Button>
        <Button size="small" variant="primary" loading={state.busy} onClick={() => {void run(target);}}>{t('accounts.signOut.server')}</Button></>} />}
  </>;
}
