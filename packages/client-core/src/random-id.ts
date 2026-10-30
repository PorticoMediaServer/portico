/**
 * A random UUID (version 4). `crypto.randomUUID` exists only in secure contexts (HTTPS and
 * localhost); a server's own web app opened over plain HTTP from another device on the LAN is
 * not one, and plain HTTP is first-class. `crypto.getRandomValues` is available everywhere, so
 * the same id is built from it when needed.
 */
export function randomId(): string {
  const c = globalThis.crypto;
  // A page may install this very function as crypto.randomUUID (the web app does on plain HTTP).
  if (typeof c?.randomUUID === 'function' && c.randomUUID !== randomId) return c.randomUUID();
  const b = c.getRandomValues(new Uint8Array(16));
  b[6] = (b[6]! & 0x0f) | 0x40;
  b[8] = (b[8]! & 0x3f) | 0x80;
  const h = Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
