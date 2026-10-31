import {defaultI18n} from '../../../../../packages/i18n/src';

/**
 * A profile PIN the server refused says so. The shared error presenter reads any `*_invalid`
 * code as an unreadable reply, so the PIN screens ask here first.
 */
export function profilePinFailureText(error: unknown): string | undefined {
  const {code, retryAfterSeconds} = (error ?? {}) as {code?: unknown; retryAfterSeconds?: unknown};
  if (code === 'profile_pin_invalid') return defaultI18n.t('accounts.profiles.pinFailed');
  if (code !== 'profile_pin_locked') return undefined;
  const seconds = typeof retryAfterSeconds === 'number' && Number.isFinite(retryAfterSeconds) && retryAfterSeconds > 0 ? Math.ceil(retryAfterSeconds) : undefined;
  return seconds ? defaultI18n.t('accounts.profiles.pinLocked', {seconds}) : defaultI18n.t('accounts.profiles.pinFailed');
}
