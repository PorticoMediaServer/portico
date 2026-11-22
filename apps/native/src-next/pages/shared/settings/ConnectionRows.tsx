import React from 'react';
import {Button, Select} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {DeviceRowBlock} from '../../../kit/settings/DeviceRows';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panels';
// Named through pages/shared (not `./`) so the screen harness loads the hook for real when a test loads these rows.
import {useConnectionAddress, useConnectionStatus, useInsecureConnections, useTestConnection} from '../../../pages/shared/settings/useConnectionRows';

const R = 'next.settingsSpec.account.device.advanced.groups.4.rows';
export const nativeInsecureConnectionsFocusId = 'settings-device-insecure-connections';

/** Settings › This device › Advanced › Connection: how this device reaches the server now. */
export function NativeConnectionStatusRow({context, t}: NativeSettingsPanelDeps) {
  const status = useConnectionStatus(context);
  if (!status) return null;
  return <SettingsRow first testID="settings-connection-status" label={t(`${R}.0.label`)} value={t(status)}/>;
}

/** The address in use, shown whole. */
export function NativeConnectionAddressRow({context, t}: NativeSettingsPanelDeps) {
  const origin = useConnectionAddress(context);
  return origin ? <SettingsRow first testID="settings-server-address" label={t(`${R}.1.label`)} value={origin}/> : null;
}

/** Test connection, with the server's answer as the row's line and a failure under it. */
export function NativeTestConnectionRow({context, t, lifetime, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const view = useTestConnection({context, t, lifetime});
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime} errorContext="settings">
    <SettingsRow first testID="settings-test-connection" label={t(`${R}.2.label`)} description={view.result}
      control={<Button testID="settings-test-connection-button" variant="glass" size="small" loading={view.busy} onPress={view.run}>{t(`${R}.2.control.label`)}</Button>}/>
  </DeviceRowBlock>;
}

/** Plain-HTTP connections: Never, On my network, or Always where the platform allows it. */
export function NativeInsecureConnectionsRow({context, t, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const view = useInsecureConnections(context);
  if (!view.shown) return null;
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime} errorContext="settings">
    <Select testID={nativeInsecureConnectionsFocusId} focusId={nativeInsecureConnectionsFocusId} rowLabel={t(`${R}.3.label`)}
      disabled={view.busy} value={view.value} options={view.options.map(option => ({value: option.value, label: t(option.labelKey)}))} onValueChange={view.choose}/>
  </DeviceRowBlock>;
}
