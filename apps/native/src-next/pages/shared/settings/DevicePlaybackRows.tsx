import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Select, Switch} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {DeviceRowBlock, RowValueSelect} from '../../../kit/settings/DeviceRows';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {passthroughFormats, type PassthroughFormat} from '../../../platform/playback/passthrough-formats';
import type {NativeDevicePreferences} from '../../../screens/settings-live/device-preference-consumer';
import {nativeDynamicRangeFocusId, nativeFrameRateFocusId, nativeLossNoticesFocusId, nativePassthroughFocusId, nativeSafeModeFocusId,
  useApplePassthrough, useDisplayMatch, useLossNotices, usePassthrough, useSafeMode} from './useDeviceRows';

type Row = {context: NativeShellBootstrapContext; runtime: NativeShellRuntime};
const D = 'next.settingsSpec.account.device.groups.1.rows';

/** Match frame rate: the device record feeds both installed Android display-matching engines. */
export function NativeFrameRateRow({context, runtime}: Row) {
  const t = runtime.text.t, view = useDisplayMatch(context, 'matchFrameRate');
  if (!view.value) return null;
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime}>
    <Select testID={nativeFrameRateFocusId} focusId={nativeFrameRateFocusId} rowLabel={t(`${D}.0.label`)} description={t(`${D}.0.help`)} disabled={view.busy} value={view.value}
      options={[{value: 'auto', label: t(`${D}.0.control.options.0.label`)}, {value: 'off', label: t(`${D}.0.control.options.1.label`)}]} onValueChange={view.choose}/>
  </DeviceRowBlock>;
}

/** Match dynamic range: an ordinary row of the Picture and sound card (tv-more 31). */
export function NativeDynamicRangeRow({context, runtime}: Row) {
  const t = runtime.text.t, view = useDisplayMatch(context, 'matchDynamicRange');
  if (!view.value) return null;
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime}>
    <SettingsRow testID={`${nativeDynamicRangeFocusId}-row`} label={t(`${D}.2.label`)} scope={t('settings.thisDevice')}
      control={<RowValueSelect testID={nativeDynamicRangeFocusId} focusId={nativeDynamicRangeFocusId} accessibilityLabel={t(`${D}.2.label`)} disabled={view.busy} value={view.value}
        options={[{value: 'auto', label: t(`${D}.2.control.options.0.label`)}, {value: 'off', label: t(`${D}.2.control.options.1.label`)}]} onValueChange={view.choose}/>}/>
  </DeviceRowBlock>;
}

/** Audio passthrough for every format, or one format under Advanced. */
export function NativePassthroughRow({context, runtime, format}: Row & {format?: PassthroughFormat}) {
  const t = runtime.text.t, view = usePassthrough(context, format);
  const focusId = format ? `settings-passthrough-${format}` : nativePassthroughFocusId;
  const label = format ? t(`next.settingsSpec.account.device.advanced.groups.1.rows.0.control.items.${passthroughFormats.indexOf(format)}.label` as Parameters<typeof t>[0]) : t(`${D}.6.label`);
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime}>
    <Select testID={focusId} focusId={focusId} rowLabel={label} description={format ? undefined : t(`${D}.6.help`)} disabled={view.busy} value={view.value}
      options={[{value: 'auto', label: t(`${D}.6.control.options.0.label`)}, {value: 'off', label: t(`${D}.6.control.options.1.label`)},
        ...(!format ? [{value: 'custom', label: t(`${D}.6.control.options.2.label`), disabled: true}] : [])]} onValueChange={view.choose}/>
  </DeviceRowBlock>;
}

/** Show loss notices. */
export function NativeLossNoticesRow({context, runtime}: Row) {
  const t = runtime.text.t, view = useLossNotices(context);
  if (view.value === undefined) return null;
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime}>
    <SettingsRow testID={nativeLossNoticesFocusId} label={t(`${D}.8.label`)} description={t(`${D}.8.help`)} scope={t('settings.thisDevice')}
      control={<Switch testID={nativeLossNoticesFocusId} checked={view.value} disabled={view.busy} accessibilityLabel={t(`${D}.8.label`)} onCheckedChange={view.set}/>}/>
  </DeviceRowBlock>;
}

/** Safe mode (tv-more 22): Automatic, Safe or (shown only) Custom. */
export function NativeSafeModeRow({source, runtime, current}: {source: NativeDevicePreferences; runtime: NativeShellRuntime; current: () => boolean}) {
  const t = runtime.text.t, view = useSafeMode(source, current), Q = 'next.settingsSpec.account.quality.groups.2.rows.1';
  if (!view.value) return null;
  return <DeviceRowBlock error={view.error} onDismiss={view.clearError} runtime={runtime}>
    <SettingsRow testID={`${nativeSafeModeFocusId}-row`} label={t(`${Q}.label`)} description={t(`${Q}.help`)} scope={t('settings.thisDevice')}
      control={<RowValueSelect focusId={nativeSafeModeFocusId} testID={nativeSafeModeFocusId} disabled={view.busy} accessibilityLabel={t(`${Q}.label`)} value={view.value}
        options={(['automatic', 'safe', 'custom'] as const).map((choice, index) => ({value: choice, label: t(`${Q}.control.options.${index}.label` as Parameters<typeof t>[0]), disabled: choice === 'custom'}))}
        onValueChange={view.choose}/>}/>
  </DeviceRowBlock>;
}

/** Frame 12's Audio passthrough on Apple TV is a capability line, not a control: the full Dolby Digital Plus with Atmos
 * line only when the normalised engine vouches for Atmos, Dolby Digital Plus otherwise, nothing for unsupported. */
export function NativeApplePassthroughRow({testID}: {testID?: string}) {
  const t = defaultI18n.t, facts = useApplePassthrough();
  if (facts === undefined) return null;
  const value = facts?.atmos === true ? t('next.deviceCapabilities.applePassthrough') : facts?.eligible === true ? t('next.kit.codec.eac3') : facts?.eligible === false ? undefined : t('web.capabilities.unknown');
  return <SettingsRow testID={testID} label={t(`${D}.7.label`)} description={t('next.deviceCapabilities.applePassthroughHint')} value={value} readable/>;
}
