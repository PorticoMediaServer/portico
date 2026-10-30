import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {Select} from '../../kit/controls';
import {StateView} from '../../kit/states';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeDevicePreferences} from './device-preference-consumer';

export const nativeSafeModeFocusId = 'settings-quality-safe-mode';
export function nativeSafeModeAvailable(source: NativeDevicePreferences): boolean {
  return typeof source.read('forceStereo') === 'boolean' && typeof source.read('disableHdr') === 'boolean';
}
/** A preset over two real committed fields, never a third stored setting. */
export function NativeSafeModeRow({source, runtime, current}: {source: NativeDevicePreferences; runtime: NativeShellRuntime; current: () => boolean}) {
  const inFlight = useRef(false), alive = useRef(true), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const snapshot = () => JSON.stringify([source.read('forceStereo'), source.read('disableHdr')]);
  const values = useSyncExternalStore(source.subscribe, snapshot);
  const [stereo, hdr] = JSON.parse(values) as unknown[];
  if (typeof stereo !== 'boolean' || typeof hdr !== 'boolean') return null;
  const value = stereo && hdr ? 'safe' : !stereo && !hdr ? 'automatic' : 'custom', t = runtime.text.t;
  const apply = async (choice: string) => {
    if (inFlight.current || !current() || !['automatic', 'safe'].includes(choice)) return;
    inFlight.current = true; setBusy(true); setError(undefined);
    try {
      const asked = choice === 'safe';
      await source.write('forceStereo', asked);
      if (!alive.current || !current()) return;
      await source.write('disableHdr', asked);
      if (!alive.current || !current()) return;
      if (source.read('forceStereo') !== asked || source.read('disableHdr') !== asked) throw new Error('native_device_preference_unreadable');
    } catch (failure) {if (alive.current && current()) setError(failure);}
    finally {inFlight.current = false; if (alive.current && current()) setBusy(false);}
  };
  return <View style={{marginBottom: 30}}>
    <Select focusId={nativeSafeModeFocusId} testID={nativeSafeModeFocusId} disabled={busy} rowLabel={t('next.settingsSpec.account.quality.groups.2.rows.1.label')} description={t('next.settingsSpec.account.quality.groups.2.rows.1.help')} value={value}
      options={(['automatic', 'safe', 'custom'] as const).map((choice, index) => ({value: choice, label: t(`next.settingsSpec.account.quality.groups.2.rows.1.control.options.${index}.label` as Parameters<typeof t>[0]), disabled: choice === 'custom'}))} onValueChange={choice => {void apply(choice);}}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => setError(undefined))}/>}
  </View>;
}
