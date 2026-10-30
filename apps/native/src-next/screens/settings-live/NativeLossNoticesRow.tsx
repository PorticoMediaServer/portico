import React, {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {Switch} from '../../kit/controls';
import {SettingsRow} from '../../kit/shell';
import {StateView} from '../../kit/states';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {bindNativeDevicePreferences} from '../settings/device-preference-bindings';

export const nativeLossNoticesFocusId = 'settings-device-loss-notices';

/** One installation preference controls the notice; Playback Info retains every fact. */
export function NativeLossNoticesRow({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const source = useMemo(() => bindNativeDevicePreferences(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => source.read('device.lossNotices')?.current);
  const live = useMemo(() => ({owner: context, current: false}), [context]);
  const pending = useRef(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, [live]);
  const t = runtime.text.t;
  if (typeof value !== 'boolean') return null;
  const apply = async (next: boolean) => {
    if (!live.current || !live.owner.current() || pending.current) return;
    pending.current = true; setBusy(true); setError(undefined);
    try {await source.write('device.lossNotices', next);}
    catch (failure) {if (live.current && live.owner.current()) setError(failure);}
    finally {pending.current = false; if (live.current && live.owner.current()) setBusy(false);}
  };
  return <View>
    <SettingsRow testID={nativeLossNoticesFocusId} label={t('next.settingsSpec.account.device.groups.1.rows.8.label')}
      description={t('next.settingsSpec.account.device.groups.1.rows.8.help')} scope={t('settings.thisDevice')}
      control={<Switch testID={nativeLossNoticesFocusId} checked={value} disabled={busy} accessibilityLabel={t('next.settingsSpec.account.device.groups.1.rows.8.label')} onCheckedChange={next => {void apply(next);}}/>}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => setError(undefined))}/>}
  </View>;
}
