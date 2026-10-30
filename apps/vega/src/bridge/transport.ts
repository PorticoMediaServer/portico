import {failure} from './events';
import {checkResponse, forgetSession, jsonValue, onCredentialExposure, rememberCredentials, requestCredentials} from './credential-boundary';

/** Bounded SDK control transport. Redirects are detected after following, not prevented. */
export type WireResponse = {status: number; headers: Record<string, string>; body: string};
type Settle = {resolve(value: WireResponse): void; reject(error: Error): void};
type Pending = Settle & {identifier: string; domain: string; url: string; method: string; headers: Record<string, string>; body: string | null; limit: number; sensitive: boolean; xhr?: XMLHttpRequest; started: boolean; done: boolean; timer?: ReturnType<typeof setTimeout>};

const MAX_ACTIVE = 12, MAX_WAITING = 64, TIMEOUT_MS = 15000, OBSERVATION_TIMEOUT_MS = 1500;
const OBSERVATION = /\/v1\/playback\/sessions\/[^/?#]+\/observation(?:$|\?)/;
const requests = new Map<string, Pending>();
const waiting: Pending[] = [];
let active = 0;
let retiring = false;

export type Admission = {identifier: string; url: string; method: string; headers: unknown; body: string | null; limit: number; domain: string; busy: string};

export function submit(admission: Admission): Promise<WireResponse> {
  return new Promise((resolve, reject) => {
    const {identifier, domain} = admission;
    let sensitive: boolean;
    try { sensitive = requestCredentials(admission.url, admission.headers, admission.body); } catch (error) { reject(error as Error); return; }
    // A playback observation never waits behind other requests: the next one, a second later,
    // replaces it. Queueing it would only deliver stale state late.
    const observation = admission.method === 'PUT' && OBSERVATION.test(admission.url);
    if (requests.has(identifier) || (active >= MAX_ACTIVE && (observation || waiting.length >= MAX_WAITING))) { reject(failure(domain, admission.busy)); return; }
    const headers: Record<string, string> = {};
    if (admission.headers && typeof admission.headers === 'object') {
      for (const [key, value] of Object.entries(admission.headers as Record<string, unknown>)) if (typeof value === 'string') headers[key] = value;
    }
    const pending: Pending = {identifier, domain, url: admission.url, method: admission.method, headers, body: admission.body, limit: admission.limit, sensitive, resolve, reject, started: false, done: false};
    pending.timer = setTimeout(() => finish(pending, 'The queue request could not be confirmed.'), limitFor(pending.url));
    requests.set(identifier, pending);
    if (active < MAX_ACTIVE) start(pending); else waiting.push(pending);
  });
}

export function cancel(identifier: string): void {
  const pending = requests.get(identifier);
  if (pending) finish(pending, 'Queue request cancelled.');
}

/**
 * A request that asks the server to hold it (`waitMs`, the events long poll) is given that
 * long and a margin; every other request has the Apple module's 15 s. Without this each quiet
 * poll ended as a transport failure, which the player took for a lost route: it paused and
 * resumed whatever was playing, every 15 s.
 */
function limitFor(url: string): number {
  if (OBSERVATION.test(url)) return OBSERVATION_TIMEOUT_MS;
  const asked = /[?&]waitMs=(\d{1,5})(&|$)/.exec(url);
  return asked ? Math.max(TIMEOUT_MS, Number(asked[1]) + 10_000) : TIMEOUT_MS;
}

/** Bytes of `text` as UTF-8, without building the encoding. */
export function utf8Length(text: string): number {
  let n = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c < 0xdc00) { n += 4; i++; }
    else n += 3;
  }
  return n;
}

function start(pending: Pending): void {
  try { requestCredentials(pending.url, pending.headers, pending.body); } catch (error) { finish(pending, undefined, undefined, error as Error); return; }
  pending.started = true;
  active++;
  try {
    const xhr = new XMLHttpRequest();
    pending.xhr = xhr;
    xhr.open(pending.method, pending.url);
    xhr.timeout = limitFor(pending.url);
    xhr.withCredentials = false;
    xhr.responseType = 'text';
    for (const [key, value] of Object.entries(pending.headers)) xhr.setRequestHeader(key, value);
    xhr.setRequestHeader('Cache-Control', 'no-store');
    const over = () => {
      const declared = Number(xhr.getResponseHeader('Content-Length'));
      return (Number.isFinite(declared) && declared > pending.limit) || (xhr.responseText?.length ?? 0) > pending.limit;
    };
    xhr.onreadystatechange = () => {
      if (xhr.readyState >= 2 && xhr.status !== 0 && !pending.done) {
        // Kepler dispatches HEADERS_RECEIVED synchronously before assigning responseURL.
        // No response is adopted here; validate the actual URL on LOADING/DONE (and load),
        // where an absent or changed URL must still fail closed.
        if (xhr.readyState === 2 && !xhr.responseURL) return;
        try { checkResponse(pending.url, xhr.responseURL, xhr.status, pending.sensitive); } catch (error) { finish(pending, undefined, undefined, error as Error); return; }
        if (over()) finish(pending, 'Queue response exceeded its limit.');
      }
    };
    xhr.onprogress = () => { if (over()) finish(pending, 'Queue response exceeded its limit.'); };
    xhr.onerror = () => finish(pending, 'The queue request could not be confirmed.');
    xhr.ontimeout = () => finish(pending, 'The queue request could not be confirmed.');
    xhr.onabort = () => finish(pending, 'Queue request cancelled.');
    xhr.onload = () => {
      if (pending.done) return;
      try { checkResponse(pending.url, xhr.responseURL, xhr.status, pending.sensitive); } catch (error) { finish(pending, undefined, undefined, error as Error); return; }
      const body = xhr.responseText ?? '';
      if (utf8Length(body) > pending.limit) { finish(pending, 'Queue response exceeded its limit.'); return; }
      if (xhr.status >= 200 && xhr.status < 300) {
        if (pending.method === 'DELETE' && new URL(pending.url).pathname === '/v1/sessions/current') forgetSession(pending.url, pending.headers);
        try { rememberCredentials(pending.url, jsonValue(body)); } catch (error) { finish(pending, undefined, undefined, error as Error); return; }
      }
      finish(pending, undefined, {status: xhr.status, headers: parseHeaders(xhr.getAllResponseHeaders()), body});
    };
    xhr.send(pending.body ?? undefined);
  } catch { finish(pending, 'The queue request could not be confirmed.'); }
}

function finish(pending: Pending, problem?: string, response?: WireResponse, error?: Error): void {
  if (pending.done) return;
  pending.done = true;
  clearTimeout(pending.timer);
  if (requests.get(pending.identifier) === pending) requests.delete(pending.identifier);
  if (!pending.started) {
    const at = waiting.indexOf(pending);
    if (at >= 0) waiting.splice(at, 1);
  } else {
    active = Math.max(0, active - 1);
    if (problem || error) { try { pending.xhr?.abort(); } catch { /* already finished */ } }
    while (!retiring && active < MAX_ACTIVE && waiting.length) start(waiting.shift()!);
  }
  if (error) pending.reject(error);
  else if (problem || !response) pending.reject(failure(pending.domain, problem ?? 'Invalid queue response.'));
  else pending.resolve(response);
}

export function parseHeaders(raw: string | null): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const line of (raw ?? '').split(/\r?\n/)) {
    const at = line.indexOf(':');
    if (at <= 0) continue;
    const key = line.slice(0, at).trim(), value = line.slice(at + 1).trim();
    headers[key] = key in headers ? `${headers[key]}, ${value}` : value;
  }
  return headers;
}

// Retire queued and in-flight work before another credentialed request can be dispatched.
onCredentialExposure(({origin, origins = [origin]}) => {
  retiring = true;
  for (const pending of [...requests.values()]) if (origins.includes(new URL(pending.url).origin)) finish(pending, undefined, undefined, failure('credential_exposed', 'The connection disclosed a credential.'));
  retiring = false;
  while (active < MAX_ACTIVE && waiting.length) start(waiting.shift()!);
});
