import {WebCrypto} from '@amazon-devices/keplercrypto';
import {hex} from './encoding';

type Source = {getRandomValues<T extends ArrayBufferView>(array: T): T};
let source: Source | undefined;

/**
 * The system's random number generator: the runtime's `crypto` when it has one, otherwise the
 * SDK's. There is no fallback past those; keys and request identifiers are never made from
 * `Math.random`.
 */
function system(): Source {
  if (source) return source;
  const runtime = (globalThis as {crypto?: Source}).crypto;
  if (typeof runtime?.getRandomValues === 'function') return (source = runtime);
  const sdk = new WebCrypto().crypto as unknown as Source;
  if (typeof sdk?.getRandomValues !== 'function') throw new Error('Secure randomness is unavailable.');
  return (source = sdk);
}

export function randomBytes(count: number): Uint8Array {
  const bytes = new Uint8Array(count);
  system().getRandomValues(bytes);
  return bytes;
}

/** A version 4 UUID in upper case, as `NSUUID` writes it (the shared code lowercases where the
 * core wants lower case). */
export function uuid(): string {
  const b = randomBytes(16);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = hex(b).toUpperCase();
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
