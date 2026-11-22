import React from 'react';
import {Switch} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {DeviceRowBlock} from '../../../kit/settings/DeviceRows';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
// Named through pages/shared (not `./`) so the screen harness loads the hook for real when a test loads this row.
import {useServerOfflineNotice} from '../../../pages/shared/settings/useServerOfflineNotice';

export const nativeServerOfflineNoticeId = 'settings-notice-server-offline';
const label = 'next.settingsSpec.account.profile.groups.7.rows.1.label';

/** "Server stops answering": the Portico Account's own notice switch, with its failure under it. */
export function NativeServerOfflineNoticeRow({context, runtime, lifetime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  const t = runtime.text.t, view = useServerOfflineNotice({context, runtime, lifetime});
  if (!view.shown) return null;
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime}>
    <SettingsRow testID={nativeServerOfflineNoticeId} label={t(label)}
      control={<Switch testID={nativeServerOfflineNoticeId} checked={view.value} disabled={view.busy} accessibilityLabel={t(label)} onCheckedChange={view.choose}/>}/>
  </DeviceRowBlock>;
}
