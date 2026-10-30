import React, {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {connectionDetails, connectionStatusKey, insecureChoicesFor, type InsecureConnections} from '../../../../../packages/client-core/src/connection-policy';
import type {RouteConnection, RouteSnapshot} from '../../../../../packages/client-core/src/route-connection';
import {Button, Select} from '../../kit/controls';
import {SettingsRow} from '../../kit/shell';
import {StateView} from '../../kit/states';
import {nativeConnectionPolicy} from '../../platform/connection-preference';
import {nativeDeviceSettings} from '../../platform/device-settings';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSettingsPanelDeps} from './settings-panels';

/**
 * Settings › This device › Advanced › Connection (account-device.json; P2-69). Status is the
 * route in use: secure by its scheme, at home by the server's own answer for it.
 */
const noSnapshot = (): RouteSnapshot | undefined => undefined;
const noSubscribe = () => () => {};
function useRoute(connection: RouteConnection | undefined) {
  return useSyncExternalStore(connection?.subscribe ?? noSubscribe, connection?.getSnapshot ?? noSnapshot);
}

const statusText = {
  secureHome: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.secureHome',
  secureAway: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.secureAway',
  insecureHome: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.insecureHome',
  insecureAway: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.insecureAway',
} as const;

export function NativeConnectionStatusRow({context, t}: NativeSettingsPanelDeps) {
  const connection = context.api.getRouteConnection();
  const route = useRoute(connection);
  const details = connectionDetails(route?.phase === 'ready' ? route.origin : undefined, connection?.getMemory().policy, route?.remote);
  if (!details) return null;
  return <SettingsRow first testID="settings-connection-status" label={t('next.settingsSpec.account.device.advanced.groups.4.rows.0.label')} value={t(statusText[connectionStatusKey(details)])}/>;
}

/** The address in use (the route, not the saved logical address), shown whole. */
export function NativeConnectionAddressRow({context, t}: NativeSettingsPanelDeps) {
  const connection = context.api.getRouteConnection();
  const route = useRoute(connection);
  const origin = route?.phase === 'ready' && route.origin ? route.origin : context.api.baseUrl;
  return origin ? <SettingsRow first testID="settings-server-address" label={t('next.settingsSpec.account.device.advanced.groups.4.rows.1.label')} value={origin}/> : null;
}

export function NativeTestConnectionRow({context, t, lifetime, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const [result, setResult] = useState<string>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const live = useRef(false);
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  const test = async () => {
    if (busy) return;
    setBusy(true); setResult(undefined); setError(undefined);
    const started = Date.now();
    try {
      await lifetime.request(signal => context.api.connection(signal));
      if (live.current) setResult(t('next.settingsSpec.account.device.advanced.groups.4.rows.2.control.result', {server: context.serverName, ms: Date.now() - started}));
    } catch (failure) {if (live.current) setError(failure);}
    finally {if (live.current) setBusy(false);}
  };
  return <View>
    <SettingsRow first testID="settings-test-connection" label={t('next.settingsSpec.account.device.advanced.groups.4.rows.2.label')} description={result}
      control={<Button testID="settings-test-connection-button" variant="glass" size="small" loading={busy} onPress={() => {void test();}}>{t('next.settingsSpec.account.device.advanced.groups.4.rows.2.control.label')}</Button>}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => setError(undefined), 'settings')}/>}
  </View>;
}

const choiceLabel: Record<InsecureConnections, 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.0.label' | 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.1.label' | 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.2.label'> = {
  never: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.0.label',
  local: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.1.label',
  always: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.2.label',
};
export const nativeInsecureConnectionsFocusId = 'settings-device-insecure-connections';

/** Never / On my network / Always. Apple offers no Always: its remote plain HTTP is never allowed. */
export function NativeInsecureConnectionsRow({context, t, runtime}: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const source = useMemo(() => nativeDeviceSettings(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => source.read('allowInsecure'));
  const pending = useRef(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const live = useRef(false);
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  const choices = insecureChoicesFor(nativeConnectionPolicy().platform);
  if (typeof value !== 'string') return null;
  const apply = async (next: string) => {
    if (!live.current || !context.current() || pending.current || !choices.some(choice => choice === next)) return;
    pending.current = true; setBusy(true); setError(undefined);
    try {await source.write('allowInsecure', next);}
    catch (failure) {if (live.current && context.current()) setError(failure);}
    finally {pending.current = false; if (live.current) setBusy(false);}
  };
  return <View>
    <Select testID={nativeInsecureConnectionsFocusId} focusId={nativeInsecureConnectionsFocusId} rowLabel={t('next.settingsSpec.account.device.advanced.groups.4.rows.3.label')}
      disabled={busy} value={choices.some(choice => choice === value) ? value : 'local'}
      options={choices.map(choice => ({value: choice, label: t(choiceLabel[choice])}))} onValueChange={next => {void apply(next);}}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => setError(undefined), 'settings')}/>}
  </View>;
}
