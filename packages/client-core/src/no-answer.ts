/**
 * Whether a failed request got no answer from the server at all: the only failure that says the server cannot be reached.
 * That is a network failure (the platform's fetch rejects with a TypeError: "Failed to fetch", "Network request failed",
 * "Load failed"), a connection that dropped part way through, a request the server never answered in time, or 502, 503 or 504 (a proxy or the server saying it cannot
 * serve now). Every other refusal is an answer: a 404 or 405 for a route this server does not serve yet, a 401 or 403, a
 * 409 or 422, a body that cannot be read. A screen shows those itself; they never mean the server is out of reach.
 * An error a screen wrapped around the one it got is looked through by its `cause`.
 */
/** What the native transport says, on Android and Apple alike, when a connection drops part way through a request. */
const DROPPED = 'The queue request could not be confirmed.';

export function serverDidNotAnswer(error: unknown, depth = 0): boolean {
  if (!error || typeof error !== 'object' || depth > 4) return false;
  const e = error as {status?: unknown; code?: unknown; name?: unknown; message?: unknown; cause?: unknown};
  if (typeof e.status === 'number' && e.status > 0) return e.status === 502 || e.status === 503 || e.status === 504;
  if (e.code === 'request_timeout' || e.code === 'network' || e.code === 'timeout' || e.name === 'TimeoutError') return true;
  if (e.name === 'TypeError' && typeof e.message === 'string' && /fetch|network|load failed/i.test(e.message)) return true;
  // The phone and TV apps' own transport: a connection that dropped part way through a request. Its other refusals under the
  // same code (too many requests at once, a request it would not send) are the app's, never the server's silence.
  if (e.code === 'queue_transport' && e.message === DROPPED) return true;
  return serverDidNotAnswer(e.cause, depth + 1);
}
