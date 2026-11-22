import React from 'react';
import {Button} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';
import {useAllowedDevices} from '../../shared/settings/useAllowedDevices';

export function AllowedDevicesRows(props: NativeSettingsPanelDeps) {
  const {t} = props, {devices, forget} = useAllowedDevices(props);
  if (!devices.length) return <SettingsRow first testID="settings-allowed-devices" label={t('settings.row.allowedDevices')} description={t('settings.allowedDevices.none')}/>;
  return <>{devices.map((device, n) => <SettingsRow key={device.signInId} first={n === 0} testID={`settings-allowed-device-${n}`}
    label={device.name || t('playOn.porticoDevice')} description={n === 0 ? t('settings.row.allowedDevices') : undefined}
    control={<Button variant="glass" size="compact" accessibilityLabel={t('settings.allowedDevices.forgetLabel', {device: device.name || t('playOn.porticoDevice')})}
      onPress={() => forget(device.signInId)}>{t('settings.allowedDevices.forget')}</Button>}/>)}</>;
}
