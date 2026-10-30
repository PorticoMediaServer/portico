/** Only a real rate-limit response supplies a wait; there is no client limit. */
export function signInRetryDeadline(error: unknown, now = Date.now()): number | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const value = error as {status?: unknown; code?: unknown; retryAfterSeconds?: unknown};
  if (value.status !== 429 && value.code !== 'rate_limited' && value.code !== 'too_many_requests') return undefined;
  const seconds = value.retryAfterSeconds;
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0 || !Number.isFinite(now)) return undefined;
  const deadline = now + seconds * 1000;
  return Number.isFinite(deadline) ? deadline : undefined;
}

export function signInRetrySeconds(deadline: number | undefined, now = Date.now()): number {
  return deadline === undefined || !Number.isFinite(deadline) ? 0 : Math.max(0, Math.ceil((deadline - now) / 1000));
}

export function signInRetryClock(seconds: number, locale: string): string {
  const whole = Math.max(0, Math.ceil(seconds));
  const minutes = new Intl.NumberFormat(locale, {useGrouping: false}).format(Math.floor(whole / 60));
  const rest = new Intl.NumberFormat(locale, {useGrouping: false, minimumIntegerDigits: 2}).format(whole % 60);
  return `${minutes}:${rest}`;
}
