import {createI18n, defaultI18n, deviceRegion, resolveRegion, type DeviceRegion, type I18n} from '../../../../../packages/i18n/src';
import {regionPreferences} from '../../../../../packages/client-core/src/preference-effects';
import type {PreferenceSnapshot} from '../../../../../packages/client-core/src/preferences';
import type {TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';

type Owner = Readonly<{current: () => boolean; preferences: () => PreferenceSnapshot}>;
const keys = ['region.locale', 'region.hourCycle', 'region.formats', 'region.dateOrder', 'region.firstDay', 'region.sizeUnits'] as const;
export type NativeRuntimeRegionText = Omit<TextEnvironment, 't'> & Readonly<{t: I18n['t']; region: I18n['region']; refreshDeviceRegion: () => boolean}>;

/** Borrows the confirmed bootstrap snapshot. This memoises formatters, not a
 * preference reader, default, writer or independent preference lifetime. */
export function nativeRuntimeRegionText(owner: Owner, readDeviceRegion: () => DeviceRegion = deviceRegion): NativeRuntimeRegionText {
  let previous: PreferenceSnapshot | undefined, text: I18n = defaultI18n;
  const snapshot = () => {
    if (!owner.current()) throw Object.assign(new Error('native_runtime_region_retired'), {name: 'AbortError'});
    return owner.preferences();
  };
  const update = (next: PreferenceSnapshot) => {
    // Older registries retain their existing runtime text, without invented fields.
    const region = keys.every(key => next.registry.fields.some(field => field.key === key))
      ? resolveRegion(regionPreferences(next), readDeviceRegion()) : undefined;
    const changed = region ? JSON.stringify(region) !== JSON.stringify(text.region) : text !== defaultI18n;
    if (changed) text = region ? createI18n(region) : defaultI18n;
    previous = next;
    return changed;
  };
  const current = () => {
    const next = snapshot();
    if (next !== previous) update(next);
    return text;
  };
  return Object.freeze({get t() {return current().t;}, get locale() {return current().locale;}, get region() {return current().region;},
    refreshDeviceRegion: () => update(snapshot())});
}
