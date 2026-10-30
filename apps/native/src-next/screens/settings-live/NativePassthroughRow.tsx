import React, {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {View} from 'react-native';
import {Select} from '../../kit/controls';
import {StateView} from '../../kit/states';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {passthroughFormats, type PassthroughFormat} from '../../platform/playback/passthrough-formats';
import {nativeDeviceSettings} from '../../platform/device-settings';

export const nativePassthroughFocusId = 'settings-device-audio-passthrough';

/** The existing disable-passthrough override; Automatic leaves the receiver capability report in charge. */
export function NativePassthroughRow({context, runtime, format}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; format?: PassthroughFormat}) {
  const source = useMemo(() => nativeDeviceSettings(context), [context]);
  const value = useSyncExternalStore(source.subscribe, () => format ? source.readPassthroughFormat(format) : source.read('disablePassthrough') === true ? 'off' : passthroughFormats.some(key => source.readPassthroughFormat(key) === 'off') ? 'custom' : 'auto');
  const live = useMemo(() => ({owner: context, current: false}), [context]);
  const pending = useRef(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  useLayoutEffect(() => {live.current = true; return () => {live.current = false;};}, [live]);
  const t = runtime.text.t;
  const focusId = format ? `settings-passthrough-${format}` : nativePassthroughFocusId;
  const label = format ? t(`next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.${passthroughFormats.indexOf(format)}.label` as Parameters<typeof t>[0]) : t('next.settingsSpec.account.device.groups.1.rows.6.label');
  const apply = async (next: string) => {
    if (!live.current || !live.owner.current() || pending.current || (next !== 'auto' && next !== 'off')) return;
    pending.current = true; setBusy(true); setError(undefined);
    try {await source.writePassthroughFormat(format ?? 'all', next as 'auto' | 'off');
      if (live.current && live.owner.current() && (format ? source.readPassthroughFormat(format) !== next : passthroughFormats.some(key => source.readPassthroughFormat(key) !== next))) throw new Error('native_device_preference_unreadable');}
    catch (failure) {if (live.current && live.owner.current()) setError(failure);}
    finally {pending.current = false; if (live.current && live.owner.current()) setBusy(false);}
  };
  return <View>
    <Select testID={focusId} focusId={focusId} rowLabel={label}
      description={format ? undefined : t('next.settingsSpec.account.device.groups.1.rows.6.help')} disabled={busy} value={value}
      options={[
        {value: 'auto', label: t('next.settingsSpec.account.device.groups.1.rows.6.control.options.0.label')},
        {value: 'off', label: t('next.settingsSpec.account.device.groups.1.rows.6.control.options.1.label')},
        ...(!format ? [{value: 'custom', label: t('next.settingsSpec.account.device.groups.1.rows.6.control.options.2.label'), disabled: true}] : []),
      ]} onValueChange={next => {void apply(next);}}/>
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, () => setError(undefined))}/>}
  </View>;
}
