/**
 * @portico/i18n — Portico's Product Language catalogue and locale-aware
 * formatting (A11Y-11…14, decision D-I18N). Zero dependencies; runs on web,
 * iOS/tvOS (Hermes) and in node tests.
 *
 *   const i18n = createI18n(resolveRegion(profilePrefs, deviceRegion()));
 *   i18n.t('home.row.recentlyAddedIn', {library: 'Movies'})  // "Recently added in Movies"
 *   i18n.duration(6120)                                       // "1h 42m"
 *   i18n.relativeTime(Date.now() - 3 * 3600e3)                // "3 hours ago"
 *
 * The UI locale (which catalogue) and the formatting locale (dates, numbers)
 * are separate: someone in France with UI en-US still gets 24-hour times and
 * "1 234,5". Region preferences use `auto` to mean "follow this device".
 * Times are always shown in the device's own time zone: it is never a preference.
 */
import {catalogue as catalogueFor, enUS, sourceLocale, supportedLocales, type MessageId, type SupportedLocale} from './catalog/index.ts';
import {formatMessage, type MessageValues} from './messageformat.ts';

export {enUS, supportedLocales, sourceLocale, catalogue, type MessageId, type SupportedLocale} from './catalog/index.ts';
export {formatMessage, parseMessage, messageArguments, MessageSyntaxError, type MessageValue, type MessageValues} from './messageformat.ts';

/**
 * The server's `region.*` profile preferences. Every field accepts `auto` (follow the device).
 * There is no time zone here: times are shown in the device's zone.
 */
export type RegionPreferences = Readonly<{
  locale?: string; // 'auto' | BCP 47
  hourCycle?: 'auto' | 'h12' | 'h23';
  formats?: string;
  dateOrder?: 'auto' | 'dmy' | 'mdy' | 'ymd';
  firstDay?: 'auto' | 'mon' | 'sun' | 'sat';
  sizeUnits?: 'binary' | 'decimal';
}>;

/**
 * What the device reports. Web: `deviceRegion()`. Apple: pass the system locales and zone.
 * `timeZone` is the device's own IANA zone; absent, the runtime's zone is used. Tests pass a
 * fixed zone here to get the same text on every machine.
 */
export type DeviceRegion = Readonly<{locales: readonly string[]; timeZone?: string; hour12?: boolean}>;

export type ResolvedRegion = Readonly<{
  /** Catalogue locale (a supported locale, with fallback). */
  uiLocale: SupportedLocale;
  /** Locale for dates, times and numbers (any valid BCP 47 tag). */
  formatLocale: string;
  dateOrder?: RegionPreferences['dateOrder'];
  firstDay?: 'mon' | 'sun' | 'sat';
  sizeUnits?: 'binary' | 'decimal';
  /** The device's zone. undefined = the runtime's own zone, which is the device's too. */
  timeZone?: string;
  /** undefined = the locale's default. */
  hour12?: boolean;
}>;

function canonical(tag: string | undefined): string | undefined {
  if (!tag || tag === 'auto') return undefined;
  try {
    return Intl.getCanonicalLocales(tag.replace(/_/g, '-'))[0];
  } catch {
    return undefined;
  }
}

/**
 * English as each region spells it. The United States and the Philippines read the source;
 * Canada has its own catalogue; every other English-speaking region spells as Britain does.
 * English with no region is the source.
 */
function englishFor(tag: string): SupportedLocale {
  const region = tag.split('-').find((part, i) => i > 0 && /^([a-z]{2}|\d{3})$/i.test(part))?.toUpperCase();
  if (!region || region === 'US' || region === 'PH') return 'en-US';
  return region === 'CA' ? 'en-CA' : 'en-GB';
}

/** Pick the best supported catalogue: exact → the region's English → same language → en-US (A11Y-14.1). */
export function resolveLocale(requested: string | readonly string[] | undefined): SupportedLocale {
  const list = (typeof requested === 'string' ? [requested] : requested ?? []).map(canonical).filter((x): x is string => !!x);
  for (const tag of list) {
    const exact = supportedLocales.find(l => l.toLowerCase() === tag.toLowerCase());
    if (exact) return exact;
  }
  for (const tag of list) {
    const language = tag.split('-')[0]!.toLowerCase();
    if (language === 'en') return englishFor(tag);
    const sameLanguage = supportedLocales.find(l => l.split('-')[0]!.toLowerCase() === language);
    if (sameLanguage) return sameLanguage;
  }
  return sourceLocale;
}

function validTimeZone(zone: string | undefined): string | undefined {
  if (!zone) return undefined;
  try {
    new Intl.DateTimeFormat('en-US', {timeZone: zone});
    return zone;
  } catch {
    return undefined;
  }
}

/**
 * Combine profile preferences with the device; `auto` and invalid values follow the device.
 * The time zone is the device's alone.
 */
export function resolveRegion(preferences: RegionPreferences | undefined, device: DeviceRegion): ResolvedRegion {
  const preferred = canonical(preferences?.locale);
  const deviceLocales = device.locales.map(canonical).filter((x): x is string => !!x);
  const formatLocale = canonical(preferences?.formats) ?? preferred ?? deviceLocales[0] ?? sourceLocale;
  // Words follow the region like dates do: a device set to Canada or Britain reads its own spelling.
  const uiLocale = resolveLocale(preferred ? [preferred, ...deviceLocales] : deviceLocales);
  const hourCycle = preferences?.hourCycle;
  return Object.freeze({
    uiLocale,
    formatLocale,
    dateOrder: preferences?.dateOrder ?? 'auto',
    firstDay: preferences?.firstDay && preferences.firstDay !== 'auto' ? preferences.firstDay : undefined,
    sizeUnits: preferences?.sizeUnits ?? 'binary',
    timeZone: validTimeZone(device.timeZone),
    hour12: hourCycle === 'h12' ? true : hourCycle === 'h23' ? false : device.hour12,
  });
}

/** The current device's region, from the JS runtime (web and node; Apple may pass its own). */
export function deviceRegion(): DeviceRegion {
  const nav = (globalThis as {navigator?: {languages?: readonly string[]; language?: string}}).navigator;
  let resolved: {locale?: string; timeZone?: string} = {};
  try { resolved = Intl.DateTimeFormat().resolvedOptions(); } catch { /* runtime without Intl */ }
  const locales = nav?.languages?.length ? nav.languages : nav?.language ? [nav.language] : resolved.locale ? [resolved.locale] : [sourceLocale];
  return {locales, timeZone: resolved.timeZone};
}

export type DateStyle = 'short' | 'medium' | 'long' | 'full' | 'dayMonth' | 'weekday' | 'monthYear' | 'year';
export type DurationStyle = 'short' | 'long';

export type I18n = Readonly<{
  region: ResolvedRegion;
  locale: SupportedLocale;
  /** A catalogue message. Missing translations fall back to en-US; never returns a raw ID. */
  t: (id: MessageId, values?: MessageValues) => string;
  has: (id: string) => id is MessageId;
  number: (value: number, options?: Intl.NumberFormatOptions) => string;
  percent: (ratio: number, fractionDigits?: number) => string;
  /** Binary sizes (1024 steps) with the locale's own number format and unit: "1.4 GB", "1,4 Go". */
  bytes: (bytes: number | undefined) => string;
  date: (value: Date | number | string, style?: DateStyle) => string;
  time: (value: Date | number | string) => string;
  /** Date and time together, joined the way the locale joins them. */
  dateTime: (value: Date | number | string) => string;
  /**
   * A date or time with fields the named styles do not cover ("Tue, Sep 22, 3:05 PM"), in the
   * profile's locale and clock. `timeZone` defaults to the device's; pass one to show another
   * zone's wall clock (a guide's, or `UTC` for a plain calendar date).
   */
  formatDate: (value: Date | number | string, options: Intl.DateTimeFormatOptions) => string;
  /** A language's name in the viewer's language ("French" for `fr`); the code itself when unknown. */
  languageName: (code: string) => string;
  /** "1h 42m" (short) or "1 hour 42 minutes" (long). Empty for 0/invalid. */
  duration: (seconds: number | undefined, style?: DurationStyle) => string;
  /** Media counter: "1:02:03" / "4:05". */
  clock: (seconds: number) => string;
  /** "Just now", "5 minutes ago", "3 hours ago", "Yesterday", "4 days ago", then a date. */
  relativeTime: (value: Date | number | string, now?: number) => string;
  /** "A, B and C" in the UI locale; `'or'` gives "A, B or C". */
  list: (items: readonly string[], type?: 'and' | 'or') => string;
}>;

const toDate = (value: Date | number | string): Date => (value instanceof Date ? value : new Date(value));

/** Build the formatter set for a resolved region. Cheap; memoise per region in the app. */
export function createI18n(region: ResolvedRegion = resolveRegion(undefined, {locales: [sourceLocale]})): I18n {
  const catalogue = catalogueFor(region.uiLocale);
  const fmt = {locale: region.formatLocale, timeZone: region.timeZone, hour12: region.hour12};
  const t = (id: MessageId, values?: MessageValues): string => {
    const source = catalogue[id] ?? enUS[id];
    if (source === undefined) return '';
    return formatMessage(source, values, fmt);
  };
  const safe = <T>(fn: () => T, fallback: T): T => { try { return fn(); } catch { return fallback; } };
  const number = (value: number, options?: Intl.NumberFormatOptions) => (Number.isFinite(value) ? safe(() => new Intl.NumberFormat(region.formatLocale, options).format(value), String(value)) : '');
  const dateOptions = (style: DateStyle): Intl.DateTimeFormatOptions => {
    switch (style) {
      case 'dayMonth': return {day: 'numeric', month: 'short'};
      case 'weekday': return {weekday: 'long', day: 'numeric', month: 'short'};
      case 'monthYear': return {month: 'long', year: 'numeric'};
      case 'year': return {year: 'numeric'};
      default: return {dateStyle: style};
    }
  };
  // One Intl formatter per distinct set of options: a guide ruler formats hundreds of times a render.
  const dateFormats = new Map<string, Intl.DateTimeFormat>();
  const dateFormat = (options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat => {
    const clocked = options.hour !== undefined || options.timeStyle !== undefined;
    const resolved: Intl.DateTimeFormatOptions = {timeZone: region.timeZone, ...(clocked && region.hour12 !== undefined ? {hour12: region.hour12} : {}), ...options};
    const key = JSON.stringify(resolved);
    let made = dateFormats.get(key);
    if (!made) dateFormats.set(key, made = new Intl.DateTimeFormat(region.formatLocale, resolved));
    return made;
  };
  const formatDate = (value: Date | number | string, options: Intl.DateTimeFormatOptions, fallback: (d: Date) => string = d => d.toISOString()) => {
    const d = toDate(value);
    if (!Number.isFinite(d.getTime())) return '';
    return safe(() => dateFormat(options).format(d), fallback(d));
  };
  const date = (value: Date | number | string, style: DateStyle = 'medium') => {
    if (!region.dateOrder || region.dateOrder === 'auto' || ['dayMonth', 'weekday', 'monthYear', 'year'].includes(style)) return formatDate(value, dateOptions(style), d => d.toDateString());
    const d = toDate(value); if (!Number.isFinite(d.getTime())) return '';
    return safe(() => {
      const parts = dateFormat({day: 'numeric', month: style === 'short' || region.dateOrder === 'ymd' ? '2-digit' : 'short', year: 'numeric'}).formatToParts(d);
      const get = (type: string) => parts.find(part => part.type === type)?.value ?? '';
      const order = region.dateOrder === 'dmy' ? ['day', 'month', 'year'] : region.dateOrder === 'mdy' ? ['month', 'day', 'year'] : ['year', 'month', 'day'];
      return order.map(get).join(region.dateOrder === 'ymd' ? '-' : style === 'short' ? '/' : ' ');
    }, d.toDateString());
  };
  const time = (value: Date | number | string) => formatDate(value, {hour: 'numeric', minute: '2-digit'}, d => d.toTimeString().slice(0, 5));
  // One formatter for both halves, so the locale supplies what goes between them.
  const dateTime = (value: Date | number | string) => region.dateOrder && region.dateOrder !== 'auto' ? `${date(value)} ${time(value)}` : formatDate(value, {dateStyle: 'medium', timeStyle: 'short'}, d => `${d.toDateString()} ${d.toTimeString().slice(0, 5)}`);
  let languageNames: {of(code: string): string | undefined} | null | undefined;
  const languageName = (code: string) => {
    if (languageNames === undefined) {
      const DisplayNames = (Intl as {DisplayNames?: new (l: string, o: object) => {of(code: string): string | undefined}}).DisplayNames;
      languageNames = DisplayNames ? safe(() => new DisplayNames(region.uiLocale, {type: 'language'}), null) : null;
    }
    const names = languageNames;
    return names ? safe(() => names.of(code), undefined) ?? code : code;
  };
  const duration = (seconds: number | undefined, style: DurationStyle = 'short') => {
    if (!seconds || !Number.isFinite(seconds) || seconds <= 0) return '';
    const total = Math.round(seconds);
    const hours = Math.floor(total / 3600);
    const minutes = Math.round((total % 3600) / 60);
    const prefix = style === 'long' ? 'duration.long.' : 'duration.';
    if (hours > 0) return minutes ? t(`${prefix}hoursMinutes` as MessageId, {hours, minutes}) : t(`${prefix}hours` as MessageId, {hours});
    if (minutes > 0) return t(`${prefix}minutes` as MessageId, {minutes});
    return t(`${prefix}seconds` as MessageId, {seconds: total});
  };
  const clock = (seconds: number) => {
    const total = Math.max(0, Math.floor(Number.isFinite(seconds) ? seconds : 0));
    const h = Math.floor(total / 3600), m = Math.floor((total % 3600) / 60), s = total % 60;
    return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`;
  };
  const relativeTime = (value: Date | number | string, now = Date.now()) => {
    const d = toDate(value);
    if (!Number.isFinite(d.getTime())) return '';
    const seconds = Math.max(0, Math.round((now - d.getTime()) / 1000));
    if (seconds < 45) return t('relative.justNow');
    const minutes = Math.round(seconds / 60);
    if (minutes < 60) return t('relative.minutesAgo', {count: minutes});
    const hours = Math.round(minutes / 60);
    if (hours < 24) return t('relative.hoursAgo', {count: hours});
    const days = Math.round(hours / 24);
    if (days === 1) return t('relative.yesterday');
    if (days < 7) return t('relative.daysAgo', {count: days});
    return date(d, new Date(now).getFullYear() === d.getFullYear() ? 'dayMonth' : 'medium');
  };
  const list = (items: readonly string[], type: 'and' | 'or' = 'and') => {
    const ListFormat = (Intl as {ListFormat?: new (l: string, o: object) => {format(x: readonly string[]): string}}).ListFormat;
    if (ListFormat) return safe(() => new ListFormat(region.uiLocale, {style: 'long', type: type === 'or' ? 'disjunction' : 'conjunction'}).format(items), items.join(', '));
    if (items.length <= 1) return items[0] ?? '';
    if (type === 'or') return items.join(', ');
    return t('list.and', {first: items.slice(0, -1).join(', '), second: items[items.length - 1]!});
  };
  // The locale words the unit ("GB", "Go") where the runtime knows byte units; an engine that
  // does not keeps the English abbreviations.
  const byteUnits = [['byte', 'B'], ['kilobyte', 'KB'], ['megabyte', 'MB'], ['gigabyte', 'GB'], ['terabyte', 'TB']] as const;
  let localizedUnits: boolean | undefined;
  const bytes = (value: number | undefined) => {
    if (value == null || !Number.isFinite(value) || value < 0) return '—';
    let v = value, i = 0;
    const step = region.sizeUnits === 'decimal' ? 1000 : 1024;
    while (v >= step && i < byteUnits.length - 1) { v /= step; i++; }
    const maximumFractionDigits = v < 10 && i > 0 ? 1 : 0;
    const [unit, abbreviation] = byteUnits[i]!;
    if (localizedUnits !== false) {
      try {
        // Whole bytes are spelled out ("512 bytes"): the short form in English is the bare word "byte".
        const text = new Intl.NumberFormat(region.formatLocale, {style: 'unit', unit, unitDisplay: i === 0 ? 'long' : 'short', maximumFractionDigits}).format(v);
        localizedUnits = true;
        return text;
      } catch {
        localizedUnits = false;
      }
    }
    return `${number(v, {maximumFractionDigits})} ${abbreviation}`;
  };
  const percent = (ratio: number, fractionDigits = 0) => number(ratio, {style: 'percent', maximumFractionDigits: fractionDigits});
  return Object.freeze({
    region, locale: region.uiLocale, t,
    has: (id: string): id is MessageId => Object.prototype.hasOwnProperty.call(enUS, id),
    number, percent, bytes, date, time, dateTime, formatDate, languageName, duration, clock, relativeTime, list,
  });
}

/** The en-US formatter set, for code that runs before preferences load and for tests. */
export const defaultI18n: I18n = createI18n();

export {calendarDate, calendarMonth} from './calendar-date.ts';
