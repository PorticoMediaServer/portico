import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {receiverAllowedDevices, type NativeAllowedDevices} from '../../platform/play-on/receiver-allowed-storage';
import {SettingsRow} from '../../kit/shell';
import {Button} from '../../kit/controls';
import type {NativeSettingsPanelDeps} from './settings-panels';

const none: readonly Readonly<{signInId: string; name: string}>[] = Object.freeze([]);
const noSubscribe = () => () => {};

/** This device, on a TV: the devices its person allowed to play here and control it (area 8,
 * rule 23), each with Forget. A forgotten device is asked again the next time it wants to. */
export function AllowedDevicesPanel({context, lifetime, t}: NativeSettingsPanelDeps) {
  const [list, setList] = useState<NativeAllowedDevices>();
  useEffect(() => {
    const session = context.session();
    if (!session) return;
    let live = true;
    void receiverAllowedDevices([session.viewer.serverId, session.viewer.accountId, session.sessionFamilyId].join(':'))
      .then(value => {if (live && lifetime.current()) setList(value);}).catch(() => {});
    return () => {live = false;};
  }, [context, lifetime]);
  const devices = useSyncExternalStore(list?.subscribe ?? noSubscribe, list?.list ?? (() => none));
  if (!devices.length) return <SettingsRow first testID="settings-allowed-devices" label={t('settings.row.allowedDevices')} description={t('settings.allowedDevices.none')}/>;
  return <>{devices.map((device, n) => <SettingsRow key={device.signInId} first={n === 0} testID={`settings-allowed-device-${n}`}
    label={device.name || t('playOn.porticoDevice')} description={n === 0 ? t('settings.row.allowedDevices') : undefined}
    control={<Button variant="glass" size="compact" accessibilityLabel={t('settings.allowedDevices.forgetLabel', {device: device.name || t('playOn.porticoDevice')})}
      onPress={() => {if (lifetime.current()) list?.forget(device.signInId);}}>{t('settings.allowedDevices.forget')}</Button>}/>)}</>;
}
