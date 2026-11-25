import {createContext, useContext, useEffect, useMemo, type ReactNode} from 'react';
import {createI18n, defaultI18n, deviceRegion, resolveRegion, type DeviceRegion, type I18n, type RegionPreferences} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import {regionPreferences} from '@core/preference-effects.ts';
import {setServiceI18n} from '@core/presentation/service-text.ts';

export const I18nContext = createContext<I18n>(defaultI18n);

/** Resolve a live I18n instance from a locale tag, region preferences, or a preference snapshot. */
export function resolveShellI18n(
  input?: PreferenceSnapshot | RegionPreferences | string,
  device: DeviceRegion = deviceRegion(),
): I18n {
  if (typeof input === 'string') return createI18n(resolveRegion({locale: input}, device));
  if (input && 'effective' in input) return createI18n(resolveRegion(regionPreferences(input), device));
  return createI18n(resolveRegion(input, device));
}

/** Propagate the active I18n instance to document lang, service-text, and API delivery headers. */
export function applyShellI18n(i18n: I18n, api?: Pick<HttpLocalApi, 'setAppLanguage'>): void {
  if (typeof document !== 'undefined' && document.documentElement) {
    document.documentElement.lang = i18n.locale;
  }
  setServiceI18n(i18n);
  api?.setAppLanguage?.(i18n.locale);
}

export type I18nProviderProps = Readonly<{
  /** Active profile preferences (read from shell bootstrap). */
  preferences?: PreferenceSnapshot;
  /** Explicit BCP 47 locale override (takes precedence over preferences' region.locale). */
  locale?: string;
  /** Optional pre-built I18n instance (takes precedence when provided). */
  i18n?: I18n;
  /** Active API client whose Accept-Language header tracks the active UI locale. */
  api?: Pick<HttpLocalApi, 'setAppLanguage'>;
  /** Optional device region override (primarily for deterministic tests). */
  device?: DeviceRegion;
  children: ReactNode;
}>;

export function I18nProvider({preferences, locale, i18n: explicitI18n, api, device, children}: I18nProviderProps) {
  const prefRegion = preferences ? regionPreferences(preferences) : undefined;
  const regionLocale = locale ?? prefRegion?.locale;
  const hourCycle = prefRegion?.hourCycle;
  const formats = prefRegion?.formats;
  const dateOrder = prefRegion?.dateOrder;
  const firstDay = prefRegion?.firstDay;
  const sizeUnits = prefRegion?.sizeUnits;
  const value = useMemo<I18n>(
    () => explicitI18n ?? resolveShellI18n({locale: regionLocale, hourCycle, formats, dateOrder, firstDay, sizeUnits}, device),
    [explicitI18n, regionLocale, hourCycle, formats, dateOrder, firstDay, sizeUnits, device],
  );
  useEffect(() => {
    applyShellI18n(value, api);
  }, [value, api]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

/** Active locale & formatter set; defaults to `defaultI18n` outside `I18nProvider`. */
export function useI18n(): I18n {
  return useContext(I18nContext) ?? defaultI18n;
}
