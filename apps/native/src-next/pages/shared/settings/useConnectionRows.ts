import {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {connectionDetails, connectionStatusKey, insecureChoicesFor, type InsecureConnections} from '../../../../../../packages/client-core/src/connection-policy';
import type {RouteConnection, RouteSnapshot} from '../../../../../../packages/client-core/src/route-connection';
import {callServer} from '../../../../../../packages/client-core/src/query/server-api';
import {nativeConnectionPolicy} from '../../../platform/connection-preference';
import {nativeDeviceSettings} from '../../../platform/device-settings';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';

/**
 * Settings › This device › Advanced › Connection (account-device.json; P2-69). Status is the
 * route in use: secure by its scheme, at home by the server's own answer for it.
 */
const noSnapshot = (): RouteSnapshot | undefined => undefined;
const noSubscribe = () => () => {};
function useRoute(connection: RouteConnection | undefined) {
  return useSyncExternalStore(connection?.subscribe ?? noSubscribe, connection?.getSnapshot ?? noSnapshot);
}
type Context = NativeSettingsPanelDeps['context'];

const statusText = {
  secureHome: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.secureHome',
  secureAway: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.secureAway',
  insecureHome: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.insecureHome',
  insecureAway: 'next.settingsSpec.account.device.advanced.groups.4.rows.0.control.insecureAway',
} as const;

/** The status line's catalogue key for the route in use, or nothing while there is no route to describe. */
export function useConnectionStatus(context: Context) {
  const connection = context.api.getRouteConnection();
  const route = useRoute(connection);
  const details = connectionDetails(route?.phase === 'ready' ? route.origin : undefined, connection?.getMemory().policy, route?.remote);
  return details ? statusText[connectionStatusKey(details)] : undefined;
}

/** The address in use (the route, not the saved logical address), shown whole. */
export function useConnectionAddress(context: Context) {
  const connection = context.api.getRouteConnection();
  const route = useRoute(connection);
  return route?.phase === 'ready' && route.origin ? route.origin : context.api.baseUrl;
}

/** Test connection: the server's public answer (`GET /v1/system`) over the route in use; an answer is the test passing. */
export function useTestConnection({context, t, lifetime}: Pick<NativeSettingsPanelDeps, 'context' | 't' | 'lifetime'>) {
  const [result, setResult] = useState<string>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const live = useRef(false);
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  const test = async () => {
    if (busy) return;
    setBusy(true); setResult(undefined); setError(undefined);
    const started = Date.now();
    try {
      await lifetime.request(signal => callServer(context.api, 'GET /v1/system', {signal}));
      if (live.current) setResult(t('next.settingsSpec.account.device.advanced.groups.4.rows.2.control.result', {server: context.serverName, ms: Date.now() - started}));
    } catch (failure) {if (live.current) setError(failure);}
    finally {if (live.current) setBusy(false);}
  };
  return {result, error, busy, run: () => {void test();}, clearError: () => setError(undefined)};
}

const choiceLabel: Record<InsecureConnections, 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.0.label' | 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.1.label' | 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.2.label'> = {
  never: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.0.label',
  local: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.1.label',
  always: 'next.settingsSpec.account.device.advanced.groups.4.rows.3.control.options.2.label',
};

/** Never / On my network / Always. Apple offers no Always: its remote plain HTTP is never allowed. */
export function useInsecureConnections(context: Context) {
  const source = useMemo(() => nativeDeviceSettings(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => source.read('allowInsecure'));
  const pending = useRef(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const live = useRef(false);
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, []);
  const choices = insecureChoicesFor(nativeConnectionPolicy().platform);
  const apply = async (next: string) => {
    if (!live.current || !context.current() || pending.current || !choices.some(choice => choice === next)) return;
    pending.current = true; setBusy(true); setError(undefined);
    try {await source.write('allowInsecure', next);}
    catch (failure) {if (live.current && context.current()) setError(failure);}
    finally {pending.current = false; if (live.current) setBusy(false);}
  };
  return {shown: typeof value === 'string', value: typeof value === 'string' && choices.some(choice => choice === value) ? value : 'local',
    options: choices.map(choice => ({value: choice, labelKey: choiceLabel[choice]})), busy, error,
    choose: (next: string) => {void apply(next);}, clearError: () => setError(undefined)};
}
