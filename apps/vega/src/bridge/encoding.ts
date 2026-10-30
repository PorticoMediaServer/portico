/** Byte and text encodings the Apple modules get from Foundation. */
const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
const lookup = new Int16Array(128).fill(-1);
for (let i = 0; i < alphabet.length; i++) lookup[alphabet.charCodeAt(i)] = i;

export function base64(bytes: Uint8Array): string {
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const a = bytes[i], b = bytes[i + 1], c = bytes[i + 2];
    out += alphabet[a >> 2] + alphabet[((a & 3) << 4) | ((b ?? 0) >> 4)];
    out += i + 1 < bytes.length ? alphabet[((b & 15) << 2) | ((c ?? 0) >> 6)] : '=';
    out += i + 2 < bytes.length ? alphabet[c & 63] : '=';
  }
  return out;
}

/** Strict standard base64 (padding optional); null for anything else. */
export function fromBase64(text: string): Uint8Array | null {
  const body = text.replace(/=+$/, '');
  if (body.length % 4 === 1) return null;
  const out = new Uint8Array(Math.floor((body.length * 3) / 4));
  let bits = 0, held = 0, at = 0;
  for (let i = 0; i < body.length; i++) {
    const code = body.charCodeAt(i);
    const value = code < 128 ? lookup[code] : -1;
    if (value < 0) return null;
    bits = (bits << 6) | value;
    held += 6;
    if (held >= 8) { held -= 8; out[at++] = (bits >> held) & 255; }
  }
  return out;
}

export function base64Url(bytes: Uint8Array): string {
  return base64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/**
 * URL-safe base64 as the Apple network module reads it: unpadded or padded, at most `limit`
 * bytes, and exactly `count` bytes when one is given. Throws on anything else.
 */
export function fromBase64Url(text: unknown, count?: number, limit = 65536): Uint8Array {
  if (typeof text !== 'string' || text.length > Math.ceil((limit * 4) / 3) + 4 || /[+/]/.test(text)) throw new Error('Invalid encoded data.');
  const bytes = fromBase64(text.replace(/-/g, '+').replace(/_/g, '/'));
  if (!bytes || bytes.length > limit || (count !== undefined && bytes.length !== count)) throw new Error('Invalid encoded data.');
  return bytes;
}

export function utf8(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

/** Strict UTF-8; null when the bytes are not text. */
export function fromUtf8(bytes: Uint8Array): string | null {
  try { return new TextDecoder('utf-8', {fatal: true}).decode(bytes); } catch { return null; }
}

export function hex(bytes: Uint8Array): string {
  let out = '';
  for (const byte of bytes) out += (byte < 16 ? '0' : '') + byte.toString(16);
  return out;
}

export function concat(...parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const part of parts) { out.set(part, at); at += part.length; }
  return out;
}

/** Length-independent comparison for authentication tags. */
export function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let difference = 0;
  for (let i = 0; i < a.length; i++) difference |= a[i] ^ b[i];
  return difference === 0;
}

export function isUuid(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
}
