import React, {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {Select} from '../../kit/controls';
import {StateView} from '../../kit/states';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeDeviceSettings} from '../../platform/device-settings';

export const nativeDynamicRangeFocusId = 'settings-device-match-dynamic-range';

/** The device record controls tvOS's actual app-owned display-criteria request. */
export function NativeDynamicRangeRow({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const source = useMemo(() => nativeDeviceSettings(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => source.read('matchDynamicRange'));
  const live = useMemo(() => ({owner: context, current: false}), [context]);
  const pending = useRef(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, [live]);
  const t = runtime.text.t;
  if (value !== 'auto' && value !== 'off') return null;
  const apply = async (next: string) => {
    if (!live.current || !live.owner.current() || pending.current || (next !== 'auto' && next !== 'off')) return;
    pending.current = true; setBusy(true); setError(undefined);
    try {await source.write('matchDynamicRange', next);
      if (live.current && live.owner.current() && source.read('matchDynamicRange') !== (next)) throw new Error('native_device_preference_unreadable');}
    catch (failure) {if (live.current && live.owner.current()) setError(failure);}
    finally {pending.current = false; if (live.current && live.owner.current()) setBusy(false);}
  };
  return <View>
    <Select testID={nativeDynamicRangeFocusId} focusId={nativeDynamicRangeFocusId} rowLabel={t('next.settingsSpec.account.device.groups.1.rows.2.label')}
      disabled={busy} value={value}
      options={[
        {value: 'auto', label: t('next.settingsSpec.account.device.groups.1.rows.2.control.options.0.label')},
        {value: 'off', label: t('next.settingsSpec.account.device.groups.1.rows.2.control.options.1.label')},
      ]} onValueChange={next => {void apply(next);}}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => setError(undefined))}/>}
  </View>;
}
