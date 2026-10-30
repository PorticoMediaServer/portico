import {routeOrigin} from './route-identity.ts';

/** One address rule for an UNCLAIMED server's setup, written identically in the Server's and
 * Hosted's claimweb (PLEX-SETUP-NOTES S1/S5). `origin` must be an exact canonical origin.
 * `https:` is always accepted. `http:` is accepted for `localhost`, an IP literal in the local
 * set (loopback, RFC 1918, CGNAT 100.64/10, link-local, IPv6 ULA; no zone, no IPv4-mapped form)
 * or a DNS name. A public IP literal over `http:` is refused. Owner switch does not use this. */
export function setupAddressAllowed(origin: string): boolean {
  let url: URL;
  try {
    if (routeOrigin(origin) !== origin) return false;
    url = new URL(origin);
  } catch {return false;}
  if (url.protocol === 'https:') return true;
  if (url.protocol !== 'http:') return false;
  const host = url.hostname;
  if (host === 'localhost') return true;
  if (host.startsWith('[')) return localIPv6(host.slice(1, -1));
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (v4) return localIPv4(Number(v4[1]), Number(v4[2]));
  // A host name (as claimweb.SetupStartAddress): its last label is a name, never a number.
  const last = host.slice(host.lastIndexOf('.') + 1);
  return last !== '' && !last.startsWith('0x') && !last.startsWith('-') && /[a-z]/.test(last);
}
function localIPv4(a: number, b: number): boolean {
  return a === 127 || a === 10 || a === 172 && b >= 16 && b <= 31 || a === 192 && b === 168 || a === 100 && b >= 64 && b <= 127 || a === 169 && b === 254;
}
function localIPv6(inner: string): boolean {
  // The URL parser has already normalized this; a zone or an IPv4-mapped tail never matches below.
  if (inner === '::1') return true;
  if (inner.includes('%') || inner.includes('.') || !/^[0-9a-f:]+$/.test(inner)) return false;
  const first = parseInt(inner.split(':')[0] || '0', 16);
  return (first & 0xfe00) === 0xfc00 || (first & 0xffc0) === 0xfe80;
}
