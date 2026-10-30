/** A failed probe is a wait only when the transport said it could come back; everything
 * else is the honest can't-reach state with the real action. */
export type ChooserFailureState = 'waiting' | 'unreachable' | 'refused';
export type ProfileResume = 'entry' | 'refused' | 'error' | 'inspected';
/** After a restored profile read, a terminal error is preserved before any anonymous
 * fallback. Only a pre-credential inspect may follow a clean no-snapshot read. */
export async function resumeAfterProfiles(
  entry: Readonly<{getSnapshot: () => Readonly<{error?: unknown; snapshot?: unknown; server?: unknown}>; inspect: (origin: string) => Promise<unknown>}>,
  selected: () => Promise<{connection: {pin: {serverId: string}}} | undefined>,
  serverId: string, origin: string, isCurrent: () => boolean,
): Promise<ProfileResume> {
  const current = entry.getSnapshot();
  if (current.error) return chooserFailureState(current.error) === 'refused' ? 'refused' : 'error';
  if (current.snapshot || current.server) return 'entry';
  const chosen = await selected();
  if (!isCurrent() || chosen?.connection.pin.serverId !== serverId) return 'error';
  await entry.inspect(origin);
  return 'inspected';
}
/** Map only the real failures the chooser can prove: a 403/removal is refused and
 * transport timeouts keep waiting. The drawn unconfirmed copy stays unwired until
 * Accounts supplies a sanitized pre-credential stage fact; the view state remains static. */
export function chooserFailureState(error: unknown): ChooserFailureState {
  if (!error || typeof error !== 'object') return retryableFailure(error) ? 'waiting' : 'unreachable';
  const value = error as {status?: unknown; code?: unknown};
  if (value.status === 403 || ['access_refused', 'forbidden', 'permission_changed', 'access_revoked', 'owner_required', 'remote_denied', 'remote_sign_in_not_allowed', 'outside_allowed_hours'].includes(String(value.code))) return 'refused';
  return retryableFailure(error) ? 'waiting' : 'unreachable';
}
function retryableFailure(error: unknown) {
  if (error instanceof TypeError) return true;
  if (!error || typeof error !== 'object') return false;
  const value = error as {retryable?: unknown; code?: unknown; name?: unknown};
  if (value.retryable === true) return true;
  if (value.name === 'TimeoutError') return true;
  return typeof value.code === 'string' && ['probe_timeout', 'timeout', 'request_failed', 'route_unavailable', 'offline', 'network'].includes(value.code);
}

