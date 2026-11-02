import {defaultI18n, type I18n} from '@i18n';
import {presentError} from '@core/presentation/errors.ts';
/** Whether Continue is available in the profile dialog: a PIN profile needs all four digits and nothing may be in flight. */
export const profileContinueEnabled = (pinRequired: boolean | undefined, pin: string, busy: boolean): boolean =>
  !busy && (!pinRequired || /^\d{4}$/.test(pin));
/** Enter does what Continue does, and only when Continue is enabled. */
export const enterContinuesProfile = (event: {key: string; isComposing?: boolean}, pinRequired: boolean | undefined, pin: string, busy: boolean): boolean =>
  event.key === 'Enter' && !event.isComposing && profileContinueEnabled(pinRequired, pin, busy);

/** What the profile dialog says about its last refusal (PR-3): a wrong PIN and a lockout under the PIN field, in the
 * picker's words; any other failure (a no-PIN profile's switch too) as the shared presenter's sentence, so the dialog is
 * never empty and never silent. A cancelled request says nothing. */
export function profileDialogText(error: unknown, lockSeconds: number, i18n: I18n = defaultI18n): Readonly<{pin?: string; failure?: string}> {
  if (!error) return {};
  const code = (error as {code?: unknown}).code;
  if (code === 'profile_pin_invalid') return {pin: i18n.t('accounts.profiles.pinFailed')};
  if (code === 'profile_pin_locked') return lockSeconds > 0 ? {pin: i18n.t('accounts.profiles.pinLocked', {seconds: lockSeconds})} : {};
  const presented = presentError(error, 'profiles', {operation: 'action', i18n});
  return presented.silent ? {} : {failure: presented.body};
}
