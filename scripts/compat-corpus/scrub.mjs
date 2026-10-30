// Scrubbing rules for the O-159 compatibility corpus. Shared by the recorder (which applies them)
// and the CI guard in packages/client-core/test/compat-corpus.test.ts (which checks the result).
export const PLACEHOLDER = '<scrubbed>';
export const MEDIA_ROOT = '/media/…';

/** Field names that look like credentials. */
export const SECRET_KEY = /token|secret|signature|nonce|password|credential|key$/i;

/** Credential-looking field names that are plain identifiers or fences the parsers need. Keep small. */
export const KEEP_KEYS = new Set([
  'labelKey', // a translation key ("detail.action.play"), not a credential
  'key', // a position-index bucket in a browse result; kept only while short (see keepsValue)
]);
/** A kept key still loses a value that looks like a credential. */
const keepsValue = (key, value) => KEEP_KEYS.has(key) && value.length <= 48 && !JWT.test(value) && !/[A-Za-z0-9+/_=-]{32,}/.test(value);

export const JWT = /eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}/;
export const BEARER = /Bearer\s/i;
export const PEM = /-----BEGIN/;
export const HOME_PATH = /\/home\//;
/** 32+ chars of base64/base64url/hex alphabet in one run. */
export const LONG_RUN = /[A-Za-z0-9+/_=-]{32,}/;

/** Fields whose values are opaque identifiers, fences or cursors, and may legitimately be long runs. */
export const IDENTITY_KEY = /(^|[a-z])(id|ids|fence|cursor|basis|revision|etag|opening|queryId|selectionBasis|selectionRevision|nextAfter|after|before|hash)$|Id$|Ids$|Fence$|Cursor$|Basis$|Revision$|Opening$|occurrence$|^(id|navigation|entityId|selection)$/i;

export function scrubString(value) {
  let out = value;
  if (HOME_PATH.test(out)) out = out.replace(/\/home\/[^\s"'<>]*/g, MEDIA_ROOT);
  if (BEARER.test(out) || JWT.test(out) || PEM.test(out)) return PLACEHOLDER;
  return out;
}

export function scrub(value, key = '') {
  if (typeof value === 'string') {
    if (SECRET_KEY.test(key) && value !== '' && !keepsValue(key, value)) return PLACEHOLDER;
    return scrubString(value);
  }
  if (Array.isArray(value)) return value.map(item => scrub(item, key));
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, scrub(v, k)]));
  }
  return value;
}

/** Every credential-shaped finding in a parsed JSON document: [{path, reason}]. */
export function findCredentials(value, path = '$', key = '') {
  const found = [];
  if (typeof value === 'string') {
    if (JWT.test(value)) found.push({path, reason: 'jwt'});
    if (BEARER.test(value)) found.push({path, reason: 'bearer'});
    if (PEM.test(value)) found.push({path, reason: 'pem'});
    if (HOME_PATH.test(value)) found.push({path, reason: 'home path'});
    if (!IDENTITY_KEY.test(key) && value !== PLACEHOLDER && LONG_RUN.test(value) && !/^\/v1\//.test(value) && !value.includes(' ')) found.push({path, reason: 'long run'});
    if (SECRET_KEY.test(key) && value !== PLACEHOLDER && value !== '' && !keepsValue(key, value)) found.push({path, reason: 'unscrubbed secret field'});
  } else if (Array.isArray(value)) {
    value.forEach((item, i) => found.push(...findCredentials(item, `${path}[${i}]`, key)));
  } else if (value && typeof value === 'object') {
    for (const [k, v] of Object.entries(value)) found.push(...findCredentials(v, `${path}.${k}`, k));
  }
  return found;
}
