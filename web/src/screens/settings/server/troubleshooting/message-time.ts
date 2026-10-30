import type {I18n} from '@i18n';

/** The drawn log has seconds and no year; retain the viewer's locale, device
 * zone and clock preference rather than forcing an English server clock. */
export function messageTime(i18n: Pick<I18n, 'formatDate'>, at: string): string {
  return i18n.formatDate(at, {month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit'});
}
