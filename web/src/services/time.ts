import type {I18n} from '@i18n';

/** account20/25/27 use localized data words (now, today, weeks ago), without a time-zone label. */
export function servicesRelativeTime(i18n: I18n, value: string, now = Date.now()) {
  const date = new Date(value), time = date.getTime();
  if (!Number.isFinite(time)) return '';
  const seconds = Math.max(0, Math.round((now - time) / 1000));
  const relative = new Intl.RelativeTimeFormat(i18n.region.formatLocale, {numeric: 'auto'});
  if (seconds < 45) return relative.format(0, 'second');
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return relative.format(-minutes, 'minute');
  const hours = Math.round(minutes / 60);
  if (hours < 24) return relative.format(-hours, 'hour');
  const days = Math.round(hours / 24);
  if (days < 7) return relative.format(-days, 'day');
  if (days < 28) return relative.format(-Math.max(1, Math.floor(days / 7)), 'week');
  return i18n.date(date);
}

export function servicesSignInDate(i18n: I18n, value: string, now = Date.now()) {
  if (!Number.isFinite(Date.parse(value))) return '';
  const fields = {year: 'numeric', month: '2-digit', day: '2-digit'} as const;
  return i18n.formatDate(value, fields) === i18n.formatDate(now, fields)
    ? new Intl.RelativeTimeFormat(i18n.region.formatLocale, {numeric: 'auto'}).format(0, 'day')
    : i18n.date(value);
}

/** Same calendar-column formatter as the checked MAIN account Notifications page. */
export function servicesNoticeTime(i18n: I18n, value: string, now = Date.now()) {
  const at = Date.parse(value), age = now - at;
  if (!Number.isFinite(at)) return '';
  if (age >= 0 && age < 3600000) return i18n.relativeTime(at, now);
  const day = {year: 'numeric', month: '2-digit', day: '2-digit'} as const;
  if (i18n.formatDate(at, day) === i18n.formatDate(now, day)) return i18n.t('web.account.notif.today');
  if (age >= 0 && age < 7 * 86400000) return i18n.formatDate(at, {weekday: 'long'});
  return i18n.date(at, 'dayMonth');
}
