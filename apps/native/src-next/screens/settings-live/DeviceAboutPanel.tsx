import React, {useEffect, useState} from 'react';
import {Linking, Platform, Text, View} from 'react-native';
import {routePath, type RouteResponse} from '../../../../../packages/contracts/src/routes';
import {SettingsRow} from '../../kit/shell';
import {StateView} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import {shellType} from '../../kit/shell/visual';
import type {NativeSettingsPanelDeps} from './settings-panels';
import type {NativeShellRuntime} from '../../shell/runtime';
import {DeviceReportRow} from './DeviceReportRow';
import {BUILD_ID} from '../../platform/build-id';

/** Installation facts already supplied by bootstrap; no guessed version or device name. */
export function DeviceAboutPanel({context, lifetime, t, runtime, openRoute}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const theme = useTheme();
  const [serverVersion, setServerVersion] = useState<string>(), [serverApi, setServerApi] = useState<string>(), [versionError, setVersionError] = useState<unknown>(), [retry, setRetry] = useState(0);
  useEffect(() => {
    const caller = new AbortController();
    setServerVersion(undefined); setServerApi(undefined); setVersionError(undefined);
    void lifetime.request(signal => context.api.request<RouteResponse<'get_system'>>(routePath('get_system'), 'GET', undefined, signal), caller.signal).then(system => {
      if (caller.signal.aborted || !lifetime.current()) return;
      if (system.id !== lifetime.authority.serverId || typeof system.version !== 'string' || !system.version.trim()) throw new Error('native_settings_server_version_invalid');
      setServerVersion(system.version);
      // O-159: the server's API version names both sides of any report (absent on a pre-release server).
      const api = (system as {apiVersion?: unknown}).apiVersion;
      setServerApi(typeof api === 'string' && /^\d{1,6}\.\d{1,6}$/.test(api) ? api : undefined);
    }).catch(error => {if (!caller.signal.aborted && lifetime.current()) setVersionError(error);});
    return () => caller.abort();
  }, [context, lifetime, retry]);
  const helpAddress = 'getportico.tv/help';
  const help = () => {
    lifetime.assert();
    void Linking.openURL(`https://${helpAddress}`).catch(error => {if (lifetime.current()) runtime.reportError(error);});
  };
  return <View testID="settings-panel-about">
    <View testID="settings-about-credits" style={{paddingHorizontal: theme.isTV ? 24 : 14, paddingVertical: theme.isTV ? 18 : 12, gap: 12}}>
      <Text style={[shellType(theme.isTV ? 20 : 13, 400, theme.isTV ? 29 : 19), {color: theme.colors.textTertiary}]}>{t('about.legal')}</Text>
      <Text style={[shellType(theme.isTV ? 20 : 13, 400, theme.isTV ? 29 : 19), {color: theme.colors.textTertiary}]}>{t('about.datasetCredit')}</Text>
    </View>
    <SettingsRow first testID="settings-app-version" label={t('about.appVersion')} value={context.appVersion}/>
    <SettingsRow testID="settings-app-build" label={t('web.about.build')} value={BUILD_ID}/>
    <SettingsRow testID="settings-server-name" label={t('about.server')} value={context.serverName}/>
    {serverVersion !== undefined && <SettingsRow testID="settings-server-version" label={t('about.serverVersion')} value={serverVersion}/>}
    {serverApi !== undefined && <SettingsRow testID="settings-server-api" label={t('web.about.serverApi')} value={serverApi}/>}
    {versionError !== undefined && <StateView {...runtime.error(versionError, () => setRetry(value => value + 1))}/>}
    <SettingsRow testID="settings-open-licenses" label={t('licenses.title')} value={t('next.settingsSpec.account.device.groups.5.rows.3.control.label')} onPress={() => {lifetime.assert(); openRoute({kind: 'licenses'});}}/>
    {!Platform.isTV && <SettingsRow testID="settings-device-help" label={t('next.settingsSpec.account.device.groups.5.rows.4.label')} value={helpAddress} onPress={help}/>}
    <DeviceReportRow context={context} lifetime={lifetime} t={t} runtime={runtime} openRoute={openRoute}/>
  </View>;
}
