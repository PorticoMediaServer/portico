import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {AccessDevice} from '@core/server-administration.ts';
import {devicePlatform} from '@core/server-admin/people-words.ts';
import {Button} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Dialog} from '../../kit/overlays/Dialog';
import {useRetained} from '../../kit/overlays';
import {SettingsGroup} from '../../kit/settings';
import {SettingsRow} from '../../kit/shell/SettingsSurfaces';
import {profileDeviceIcon} from './account-profile/device-icon';
import {PersonDevicesSource, type PeopleDevicesApi} from './people-devices-source';
import styles from './people-devices.module.css';

const t = defaultI18n.t, devicesRow = settingsRowIndex.get('people.devices')!.row, allRow = settingsRowIndex.get('people.signOutPerson')!.row;
type PeopleDevicesProps = Readonly<{
  accountId: string; api: PeopleDevicesApi; isCurrent: () => boolean; canSignOut: boolean; onError: (error: unknown) => void;
}>;
export function PeopleDevices(props: PeopleDevicesProps) {
  const {accountId, api, isCurrent, canSignOut} = props;
  const [entry, setEntry] = useState<{id: number; accountId: string; api: PeopleDevicesApi; isCurrent: () => boolean; canSignOut: boolean; source: PersonDevicesSource}>();
  const sequence = useRef(0);
  // Own the source in a committed layout so StrictMode remounts receive a fresh
  // lease instead of reviving a disposed person's request lifetime.
  useLayoutEffect(() => {
    const source = new PersonDevicesSource(accountId, api, isCurrent, () => canSignOut);
    setEntry({id: ++sequence.current, accountId, api, isCurrent, canSignOut, source});
    return () => source.dispose();
  }, [accountId, api, isCurrent, canSignOut]);
  return entry && entry.accountId === accountId && entry.api === api && entry.isCurrent === isCurrent && entry.canSignOut === canSignOut
    ? <PeopleDevicesView key={entry.id} {...props} source={entry.source} /> : null;
}
function PeopleDevicesView({source, isCurrent, canSignOut, onError}: PeopleDevicesProps & {source: PersonDevicesSource}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [target, setTarget] = useState<{source: PersonDevicesSource; device?: AccessDevice}>();
  const active = useRef<typeof target>(undefined), alive = useRef(false), mountedSource = useRef<PersonDevicesSource | undefined>(undefined), trigger = useRef<HTMLElement | null>(null), submitting = useRef(false);
  useEffect(() => {let mounted = true; alive.current = true; mountedSource.current = source; void source.load().catch(error => {if (mounted && isCurrent()) onError(error);}); return () => {mounted = false; alive.current = false; mountedSource.current = undefined; active.current = undefined;};}, [source, isCurrent, onError]);
  const ask = (element: HTMLElement, device?: AccessDevice) => {if (!alive.current || !isCurrent() || !canSignOut || state.busy || state.loading) return; const next = {source, device}; trigger.current = element; active.current = next; setTarget(next);};
  const close = () => {if (!submitting.current) {active.current = undefined; setTarget(undefined);}};
  const confirm = async () => {
    const captured = active.current;
    if (!captured || captured !== target || captured.source !== source || !alive.current || !isCurrent() || submitting.current) return;
    submitting.current = true;
    try {await source.signOut(captured.device); if (alive.current && isCurrent() && active.current === captured) {active.current = undefined; setTarget(undefined);}}
    catch (error) {if (alive.current && isCurrent() && active.current === captured) onError(error);}
    finally {submitting.current = false;}
  };
  const reload = (more = false) => {void source.load(more).catch(error => {if (alive.current && mountedSource.current === source && isCurrent()) onError(error);});};
  const opening = target?.source === source && active.current === target ? target : undefined;
  const shownOpening = useRetained(opening);
  return <SettingsGroup plain>
    {state.loading && !state.loaded ? <p role="status">{t('next.settings.loading')}</p> : null}
    {state.error ? <div role="alert"><p>{t('next.states.pageFailedBody')}</p><Button size="small" disabled={state.loading || state.busy} onClick={() => reload()}>{t('action.tryAgain')}</Button></div> : null}
    <ul className={styles.devices} aria-label={t(devicesRow.label)}>{state.items.map(device => <li key={device.id}>
      <span className={styles.icon}><Icon name={profileDeviceIcon(device.platform)} size={22} /></span>
      <span className={styles.name}><b>{device.name || device.app}</b><small>{[devicePlatform(t, device.platform), device.profileName].filter(Boolean).join(' ')}</small>
        <small>{new Intl.DateTimeFormat(undefined, {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(device.lastSeenAt))}</small></span>
      <Button size="small" disabled={!canSignOut || state.loading || state.busy} onClick={event => ask(event.currentTarget, device)}>{t(devicesRow.control.itemAction!.label)}</Button>
    </li>)}</ul>
    {state.nextCursor && <Button size="small" loading={state.loading} disabled={state.busy} onClick={() => reload(true)}>{t('next.settings.loadMore')}</Button>}
    <SettingsRow label={t(allRow.label)}><Button size="small" disabled={!canSignOut || !state.loaded || state.loading || state.busy} onClick={event => ask(event.currentTarget)}>{t(allRow.control.label!)}</Button></SettingsRow>
    {shownOpening && <Dialog open={!!opening} size="sm" title={shownOpening.device ? t('confirm.signOutDevice.title', {device: shownOpening.device.name}) : t(allRow.label)}
      description={t(shownOpening.device ? devicesRow.control.itemAction!.confirm! : allRow.control.confirm!)} returnFocusRef={trigger}
      onOpenChange={open => {if (!open) close();}} dismissOnOutsidePress={!state.busy} dismissOnBack={!state.busy} showCloseButton={!state.busy}
      footer={<><Button size="small" disabled={state.busy} onClick={close}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={state.busy} onClick={() => void confirm()}>{t(shownOpening.device ? devicesRow.control.itemAction!.label : allRow.control.label!)}</Button></>}>{null}</Dialog>}
  </SettingsGroup>;
}
