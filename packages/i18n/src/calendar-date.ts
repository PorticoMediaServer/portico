import type {I18n} from './index.ts';

/** A calendar fact has no time zone. Preserve its actual month/day precision
 * while using the current locale; never shift it into the device's prior day. */
export function calendarDate(i18n: Pick<I18n, 'formatDate'>, value: string): string {
  if (!/^[0-9]{4}-[0-9]{2}(-[0-9]{2})?$/.test(value) || value.startsWith('0000')) return '';
  const month = value.length === 7;
  const date = new Date((month ? value + '-01' : value) + 'T12:00:00Z');
  if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, value.length) !== value) return '';
  const options: Intl.DateTimeFormatOptions = {year: 'numeric', month: 'long', ...(month ? {} : {day: 'numeric' as const}), timeZone: 'UTC'};
  return i18n.formatDate(date, options);
}

/** Real Gregorian month geometry, independent of edition availability. */
export function calendarMonth(i18n: Pick<I18n, 'formatDate' | 'region'>, month: string) {
  if (month.length !== 7 || !calendarDate(i18n, month)) return undefined;
  const first = new Date(month + '-01T12:00:00Z');
  const next = new Date(first); next.setUTCMonth(next.getUTCMonth() + 1); next.setUTCDate(0);
  const firstDay = i18n.region.firstDay === 'sun' ? 0 : i18n.region.firstDay === 'sat' ? 6 : 1;
  const weekdays = Array.from({length: 7}, (_, index) => {
    const day = new Date('2024-01-07T12:00:00Z'); day.setUTCDate(day.getUTCDate() + (firstDay + index) % 7);
    return i18n.formatDate(day, {weekday: 'short', timeZone: 'UTC'});
  });
  return {label: calendarDate(i18n, month), weekdays, offset: (first.getUTCDay() - firstDay + 7) % 7,
    dates: Array.from({length: next.getUTCDate()}, (_, index) => month + '-' + String(index + 1).padStart(2, '0'))};
}
