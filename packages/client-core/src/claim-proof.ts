import {transportJSON} from './index.ts';
import {currentUTCTimestamp} from './local-session.ts';
import {routeBytes} from './route-identity.ts';

/** Canonical signed-byte syntax shared by distinct claim-purpose verifiers.
 * These helpers do not verify signatures or confer any setup/owner authority. */
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v) && [Object.prototype, null].includes(Object.getPrototypeOf(v));
export function readCanonicalClaimProof<T extends readonly string[]>(value: unknown, fields: T, invalid: () => never): {raw: Uint8Array; signature: Uint8Array; facts: Readonly<Record<T[number], string>>} {
  if (!object(value) || Object.keys(value).sort().join(',') !== 'payload,signature') invalid();
  const raw = routeBytes(value.payload, undefined, 8192), signature = routeBytes(value.signature, 64);
  let parsed: unknown;
  try {parsed = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(raw));} catch {invalid();}
  if (!object(parsed) || Object.keys(parsed).join(',') !== fields.join(',') || fields.some(field => typeof parsed[field] !== 'string') || transportJSON(parsed) !== new TextDecoder().decode(raw)) invalid();
  return {raw, signature, facts: Object.freeze(parsed) as Readonly<Record<T[number], string>>};
}
export function canonicalClaimTimestamp(value: string, invalid: () => never): bigint {
  if (!currentUTCTimestamp(value) || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{0,8}[1-9])?Z$/.test(value)) invalid();
  const fraction = value.includes('.') ? value.slice(20, -1) : '';
  return BigInt(Date.parse(value.slice(0, 19) + 'Z')) * 1000000n + BigInt(fraction.padEnd(9, '0'));
}
export function canonicalClaimCounter(value: string): boolean {return /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;}
