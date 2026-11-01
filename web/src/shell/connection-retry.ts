import {presentError} from '@core/presentation/errors.ts';

/** states.html#3: the drawn retry waits 2, 4 and 8 seconds and then keeps going. */
const STEPS = [2000, 4000, 8000, 15000, 30000, 60000] as const;

/** `attempt` counts completed failed connections; 0 is the first scheduled retry. */
export function retryDelayMs(attempt: number, random: () => number = Math.random): number {
  const base = STEPS[Math.min(Math.max(Math.trunc(attempt), 0), STEPS.length - 1)];
  // Spread tabs by ±10% so a restored session does not answer in lockstep. Never under a second.
  const jitter = 0.9 + Math.min(1, Math.max(0, random())) * 0.2;
  return Math.max(1000, Math.round(base * jitter));
}

export function retrySeconds(nextRetryAt: number, now: number): number {
  return Math.max(0, Math.ceil((nextRetryAt - now) / 1000));
}

/** Remaining fraction for the drawn ring; null when the same-instant window is unknown. */
export function retryProgress(startedAt: number, nextRetryAt: number, now: number): number | null {
  const total = nextRetryAt - startedAt;
  if (!Number.isFinite(total) || total <= 0) return null;
  return Math.max(0, Math.min(1, (nextRetryAt - now) / total));
}

/** The only failure codes this page may show or copy. A code is a bounded
 * protocol fact from the transport; anything else (and any message) stays off
 * the page, so a credential-bearing value can never be disclosed by accident. */
const DISPLAYABLE_CODES: ReadonlySet<string> = new Set([
  'network', 'request_failed', 'server_unavailable', 'route_unavailable', 'offline',
  'timeout', 'remote_stalled', 'console_unavailable', 'delivery_http_failure', 'remote_unavailable',
]);

export function displayableTransportCode(code: unknown): string | undefined {
  return typeof code === 'string' && DISPLAYABLE_CODES.has(code) ? code : undefined;
}

export type LaunchFailure = 'unreachable' | 'offline' | 'timeout' | 'other';

/** Only genuine transport failures become the drawn wait. Identity, access and
 * contract failures keep the existing error path, so a guard is never softened
 * into "try again later". */
export function launchFailure(error: unknown, deviceOnline: boolean | undefined): LaunchFailure {
  const category = presentError(error, 'generic', {deviceOnline}).category;
  return category === 'unreachable' || category === 'offline' || category === 'timeout' ? category : 'other';
}

/** The saved sign-in can't open here, and waiting won't change that. `kept`: the address now
 * proves a different server (it was reset or replaced), the saved record can never be read again,
 * or the server refuses this profile; the record stays for the sign-in page to offer its choices.
 * `ended`: the server or this device ended the sign-in. Either way launch goes to sign-in, never
 * the page failure, and nothing is softened into a retry. */
export type SavedSignInUnusable = 'kept' | 'ended';
export function savedSignInUnusable(error: unknown): SavedSignInUnusable | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const value = error as {code?: unknown; signOut?: unknown};
  if (value.signOut === true) return 'ended';
  if (value.code === 'identity_mismatch' || value.code === 'invalid_saved_connection' || value.code === 'access_refused') return 'kept';
  return undefined;
}

/** The early-bound server listener publishes this exact unavailable response.
 * No message, arbitrary503 or identity refusal can become startup progress. */
export function serverIsStarting(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const value = error as {status?: unknown; code?: unknown};
  return value.status === 503 && value.code === 'starting';
}
