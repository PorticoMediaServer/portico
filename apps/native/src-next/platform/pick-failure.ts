/** E-mobile-01/25: what happens to a freshly picked session when a check after a successful
 * select fails. Only a proven different identity ends it on the server; a cancellation, a timeout
 * or a read that failed leaves it unpublished and unused, never signed out. Every failure names
 * its path and step (codes and classes only), release builds too. */
export function provenIdentityMismatch(error: unknown): boolean {
  return (error as {code?: unknown} | null)?.code === 'identity_mismatch';
}

export function logPickFailure(path: 'sign-in' | 'switch', step: string, error: unknown) {
  const value = (error ?? {}) as {code?: unknown; name?: unknown; message?: unknown};
  const token = (raw: unknown) => typeof raw === 'string' && /^[A-Za-z0-9_.:-]{1,64}$/.test(raw) ? raw : undefined;
  try {
    console.warn(`portico_pick_failed ${JSON.stringify({path, step, code: token(value.code), class: token(value.name), message: token(value.message), revoked: provenIdentityMismatch(error)})}`);
  } catch { /* passive */ }
}
