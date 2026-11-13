import React, {useLayoutEffect, useRef, useState} from 'react';
import {View} from 'react-native';
import {readNetworkInterface, saveNetworkInterface, type NetworkInterfaceSettings} from '../../../../../../packages/client-core/src/server-admin/network-interface';
import {Select} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeScreenLifetime} from '../lifetime';
import {nativeStorageOwner} from './storage/space-source';

const K = 'next.settingsSpec.server.remote.advanced.groups.3.rows.1';
type Opening = Readonly<{current: () => boolean; load: () => Promise<NetworkInterfaceSettings>; save: (state: NetworkInterfaceSettings, selected: string) => Promise<NetworkInterfaceSettings>}>;

/** The real interface document stays independent of the older remote-page forms. */
export function NativeNetworkInterfaceRow({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const t = runtime.text.t;
  const [value, setValue] = useState<NetworkInterfaceSettings>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const opening = useRef<Opening | undefined>(undefined);
  useLayoutEffect(() => {
    if (!nativeStorageOwner(context)) return;
    const lifetime = new NativeScreenLifetime(context);
    const current = () => lifetime.current() && nativeStorageOwner(context);
    const held: Opening = {current,
      load: () => lifetime.request(signal => readNetworkInterface(context.api, signal)),
      save: (state, selected) => lifetime.request(signal => saveNetworkInterface(context.api, state, selected, signal)),
    };
    opening.current = held;setValue(undefined);setError(undefined);setBusy(false);
    void held.load().then(next => {if (held.current()) setValue(next);}, failure => {if (held.current()) setError(failure);});
    return () => {lifetime.dispose();if (opening.current === held) opening.current = undefined;};
  }, [context]);
  const run = async (selected?: string) => {
    const held = opening.current;
    if (!held?.current() || busy || selected !== undefined && !value) return;
    setBusy(true);setError(undefined);
    try {
      const next = selected === undefined ? await held.load() : await held.save(value!, selected);
      if (opening.current === held && held.current()) setValue(next);
    } catch (failure) {
      // A stale whole document must be reread deliberately before another write.
      if (opening.current === held && held.current()) {setValue(undefined);setError(failure);}
    } finally {if (opening.current === held && held.current()) setBusy(false);}
  };
  if (!nativeStorageOwner(context)) return null;
  const choices = [{value: '', label: t('web.connectivity.interfaceAuto')}, ...(value?.interfaces ?? []).map(iface => ({value: iface.name, label: `${iface.name} (${iface.addresses.join(', ')})`}))];
  if (value?.selected && !value.interfaces.some(iface => iface.name === value.selected)) choices.push({value: value.selected, label: value.selected});
  return <View>
    <SettingsRow testID="remote-network-interface" label={t(`${K}.label`)} description={value?.note ?? t(`${K}.help`)}
      control={value ? <Select testID="remote-network-interface-select" accessibilityLabel={t(`${K}.label`)} value={value.selected} options={choices} disabled={busy} onValueChange={selected => {void run(selected);}}/> : undefined}
      value={!value && error === undefined ? t('status.loading') : undefined}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => {void run();}, 'server-console')}/>}
  </View>;
}
