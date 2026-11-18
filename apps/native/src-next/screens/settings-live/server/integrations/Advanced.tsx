import React, {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {IntegrationSwitchesSource} from '../../../../../../../packages/client-core/src/server-admin/integration-switches';
import {Switch} from '../../../../kit/controls';
import {useTheme} from '../../../../kit/theme';
import {StateView} from '../../../../kit/states';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime} from '../../lifetime';

const idle = Object.freeze({phase: 'loading' as const});
const noSubscribe = () => () => {};

/** Integrations: the Jellyfin-compatible routes switch, on the rebuilt server's Integrations settings
 * (`GET`/`PUT /v1/admin/settings/integrations`, `compatibleRoutes`). Like the other switches, a change saves at once. */
export function NativeIntegrationAdvanced({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const context = ports.context, theme = useTheme(), t = runtime.text.t;
  const [source, setSource] = useState<IntegrationSwitchesSource>();
  useLayoutEffect(() => {
    const lifetime = new NativeScreenLifetime(context);
    const next = new IntegrationSwitchesSource({request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal) =>
      lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller)}, lifetime.current);
    setSource(next); void next.load();
    return () => {next.dispose(); lifetime.dispose();};
  }, [context]);
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? (() => idle), source?.getSnapshot ?? (() => idle));
  const on = state.value?.compatibleRoutes;
  return <View style={{marginTop: 16}}>
    {on !== undefined && <Switch checked={on} disabled={!!state.saving} onCheckedChange={value => {void source?.set('compatibleRoutes', value);}}>{t('integrations.compatibility.title')}</Switch>}
    <Text style={{color: theme.colors.textSecondary, marginTop: 4}}>{t('integrations.compatibility.help')}</Text>
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {void source?.load();})}/>}
  </View>;
}
