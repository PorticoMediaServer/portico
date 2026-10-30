import {privateRouteHost} from './route-identity.ts';
import type {InsecureConnections} from './connection-policy.ts';
import {CodedError} from './server-messages.ts';

/**
 * What a person types for a server is rarely a URL: "192.168.1.20", "nas", "media.example.com",
 * "nas.local:8443". This turns it into the short list of addresses worth trying, most likely
 * first, for every client that has an address field. Nothing here contacts anything, and nothing
 * here is trusted: the server at whichever address answers still has to prove its identity
 * before any credential is sent.
 *
 * - An address typed with its scheme is used exactly as typed: one candidate.
 * - A port the person typed is always kept.
 * - With no port, the server's own port is tried as well as the scheme's default.
 * - A name or address that looks like it is on the home network is tried over plain HTTP first;
 *   anything else is tried over HTTPS first.
 * - With insecure connections set to Never, a guess is HTTPS only. An address typed with `http://`
 *   is the person's own choice and is kept.
 */
export const DEFAULT_SERVER_PORT = 32500;
const MAX_TYPED = 300;
const HOME_SUFFIXES = ['.local', '.lan', '.home', '.internal', '.home.arpa', '.localdomain'];

function invalid(): never {
  throw new CodedError('invalid_address', 'Enter a server address.');
}

/** A host that looks like it lives on the home network: a private or link-local address, a bare name, or a home-network suffix. */
export function homeLookingHost(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase();
  if (privateRouteHost(host)) return true;
  if (h.includes(':')) return false;
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(h)) return false;
  return !h.includes('.') || HOME_SUFFIXES.some(suffix => h.endsWith(suffix));
}

function split(value: string): {host: string; port?: string} {
  const authority = value.split(/[/?#]/)[0] ?? '';
  // A bracketed IPv6 address, with or without a port.
  const bracketed = /^(\[[0-9a-f:.]+\])(?::(\d{1,5}))?$/i.exec(authority);
  if (bracketed) return {host: bracketed[1]!.toLowerCase(), port: bracketed[2]};
  // A bare IPv6 address has several colons and cannot carry a port.
  if ((authority.match(/:/g) ?? []).length >= 2) {
    if (!/^[0-9a-f:.]+$/i.test(authority)) invalid();
    return {host: '[' + authority.toLowerCase() + ']'};
  }
  const named = /^([a-z0-9](?:[a-z0-9.-]*[a-z0-9])?)(?::(\d{1,5}))?$/i.exec(authority);
  if (!named) invalid();
  return {host: named[1]!.toLowerCase(), port: named[2]};
}

/** The addresses to try for what was typed, most likely first; at most four. Throws `invalid_address` for something that cannot be an address. */
export function serverAddressCandidates(typed: string, options: {insecure?: InsecureConnections} = {}): readonly string[] {
  const all = guessedAddresses(typed);
  if (options.insecure !== 'never' || /^https?:\/\//i.test(typed.trim())) return all;
  return Object.freeze(all.filter(origin => origin.startsWith('https://')));
}

function guessedAddresses(typed: string): readonly string[] {
  const value = typeof typed === 'string' ? typed.trim().replace(/\/+$/, '') : '';
  if (!value || value.length > MAX_TYPED || /[\s\\\x00-\x1f\x7f@]/.test(value)) invalid();
  if (/^https?:\/\//i.test(value)) return Object.freeze([value]);
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(value)) invalid();
  const {host, port} = split(value);
  if (port !== undefined && (Number(port) < 1 || Number(port) > 65535)) invalid();
  const home = homeLookingHost(host);
  const at = (scheme: 'http' | 'https', p?: string | number) => `${scheme}://${host}${p === undefined ? '' : ':' + p}`;
  if (port !== undefined) return Object.freeze(home ? [at('http', port), at('https', port)] : [at('https', port), at('http', port)]);
  return Object.freeze(home
    ? [at('http', DEFAULT_SERVER_PORT), at('https', DEFAULT_SERVER_PORT), at('https'), at('http')]
    : [at('https'), at('https', DEFAULT_SERVER_PORT), at('http', DEFAULT_SERVER_PORT)]);
}

/** The most likely address for what was typed (for showing it back, and for flows that try only one). */
export function serverAddress(typed: string): string {
  return serverAddressCandidates(typed)[0]!;
}

/**
 * Tries each candidate in order with `probe` and returns the first that answers. When none
 * does, the failure of the most likely candidate is the one reported. A cancelled attempt stops
 * the whole search.
 */
export async function firstAnswering<T>(candidates: readonly string[], probe: (origin: string, index: number) => Promise<T>, signal?: AbortSignal): Promise<{origin: string; value: T}> {
  let first: unknown;
  for (let i = 0; i < candidates.length; i++) {
    if (signal?.aborted) break;
    try {
      return {origin: candidates[i]!, value: await probe(candidates[i]!, i)};
    } catch (error) {
      if (i === 0) first = error;
      if (signal?.aborted) break;
    }
  }
  throw first ?? new CodedError('invalid_address', 'Enter a server address.');
}
