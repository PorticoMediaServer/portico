/** The Portico Services API as the account pages call it: same origin, JSON, one error format
 * (`{"error": {"code", "message", "retry", "fields"}}`). Every route is in the Services
 * contract (`hosted-services/hosted-next/API.md` in the private repository); nothing here
 * invents one. */

export class ServicesError extends Error {
  constructor(readonly status: number, readonly code: string, readonly fields: Readonly<Record<string, string>> = {}) {
    super(code);
  }
}

export type Tokens = Readonly<{accessToken: string; refreshToken: string; expiresAt: string}>;

/** What "Signed-in devices" shows for this browser: its name and system, never more. */
export function thisDevice(agent = typeof navigator === 'undefined' ? '' : navigator.userAgent): {name: string; kind: string} {
  const browser = /Edg\//.test(agent) ? 'Edge' : /Firefox\//.test(agent) ? 'Firefox' : /Chrome\//.test(agent) ? 'Chrome' : /Safari\//.test(agent) ? 'Safari' : 'Web browser';
  const system = /iPhone|iPad/.test(agent) ? 'iOS' : /Android/.test(agent) ? 'Android' : /Mac OS X/.test(agent) ? 'macOS' : /Windows/.test(agent) ? 'Windows' : /Linux|CrOS/.test(agent) ? 'Linux' : '';
  return {name: system ? `${browser}, ${system}` : browser, kind: 'web'};
}

type Options = Readonly<{token?: string; create?: boolean; signal?: AbortSignal}>;

/** One call. A POST that makes something (`create`) carries a fresh Idempotency-Key, so a
 * repeat of the same press is answered once; a new press is a new request. */
export async function servicesCall<T>(method: string, path: string, body?: unknown, options: Options = {}): Promise<T> {
  const headers: Record<string, string> = {Accept: 'application/json'};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (options.token) headers.Authorization = `Bearer ${options.token}`;
  if (options.create) headers['Idempotency-Key'] = crypto.randomUUID();
  let response: Response;
  try {
    response = await fetch(path, {method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: options.signal, credentials: 'omit', cache: 'no-store'}); // query-read: kept (the Portico Services account pages' one requester)
  } catch (error) {
    if (options.signal?.aborted) throw error;
    throw new ServicesError(0, 'unreachable');
  }
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  let value: unknown;
  try { value = text ? JSON.parse(text) : undefined; } catch { value = undefined; }
  if (response.ok) return value as T;
  const problem = (value as {error?: {code?: unknown; fields?: unknown}} | undefined)?.error;
  const code = typeof problem?.code === 'string' ? problem.code : response.status >= 500 ? 'internal' : 'bad_request';
  const fields = problem?.fields && typeof problem.fields === 'object' ? problem.fields as Record<string, string> : {};
  throw new ServicesError(response.status, code, fields);
}

/** A signed document's contents. The pages only show what is in it; the apps and servers that
 * act on one check its signature. */
export function envelopePayload<T>(envelope: {payload: string}): T {
  const base64 = envelope.payload.replace(/-/g, '+').replace(/_/g, '/');
  const bytes = Uint8Array.from(atob(base64 + '='.repeat((4 - base64.length % 4) % 4)), c => c.charCodeAt(0));
  return JSON.parse(new TextDecoder().decode(bytes)) as T;
}
