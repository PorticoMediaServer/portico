/**
 * The one definition of "local" every Portico client uses (P2-69; the server's Go mirror is P2-67's).
 *
 * - Real home ranges (RFC 1918, link-local, unique local IPv6) are `lan`; loopback is `loopback`.
 * - Overlay networks (the shared 100.64/10 range Tailscale uses, Tailscale's IPv6 range and
 *   `*.ts.net` names) are `overlay`: remote unless the owner lists them as LAN networks.
 * - Everything else is `public`.
 *
 * When the server's LAN networks list is known it is the whole answer for an address: local means
 * loopback or inside a listed network, and an empty list means everything is remote. Before the
 * list is known, `lan` and `loopback` are local. For the connection in use, the server's own
 * answer (`ConnectionDocument.remote`) outranks this guess.
 *
 * Nothing here contacts anything or grants trust; a route still has to prove the server's key.
 */
export type AddressClass = 'loopback' | 'lan' | 'overlay' | 'public';
export type IPAddress = Readonly<{family: 4 | 6; bytes: Uint8Array}>;

const LAN_SUFFIXES = ['.local', '.lan', '.internal', '.home.arpa'];

function parseIPv4(value: string): Uint8Array | undefined {
  const parts = value.split('.');
  if (parts.length !== 4 || parts.some(p => !/^(?:0|[1-9]\d{0,2})$/.test(p) || Number(p) > 255)) return;
  return Uint8Array.from(parts.map(Number));
}

function parseIPv6(value: string): Uint8Array | undefined {
  const v = value.toLowerCase();
  if (!/^[0-9a-f:]+$/.test(v) || v.length > 39) return;
  const halves = v.split('::');
  if (halves.length > 2) return;
  const groups = (s: string) => (s === '' ? [] : s.split(':'));
  const head = groups(halves[0]!), tail = halves.length === 2 ? groups(halves[1]!) : [];
  if ([...head, ...tail].some(g => !/^[0-9a-f]{1,4}$/.test(g))) return;
  const missing = 8 - head.length - tail.length;
  if (halves.length === 2 ? missing < 1 : missing !== 0) return;
  const all = [...head, ...Array<string>(missing).fill('0'), ...tail];
  const out = new Uint8Array(16);
  all.forEach((g, i) => {const n = parseInt(g, 16); out[i * 2] = n >> 8; out[i * 2 + 1] = n & 255;});
  return out;
}

/** An IP literal (IPv6 with or without brackets), or undefined. No zones, no IPv4-in-IPv6 dotted forms. */
export function parseIP(host: string): IPAddress | undefined {
  const h = host.replace(/^\[(.*)\]$/, '$1');
  const v4 = parseIPv4(h);
  if (v4) return {family: 4, bytes: v4};
  const v6 = h.includes(':') ? parseIPv6(h) : undefined;
  return v6 ? {family: 6, bytes: v6} : undefined;
}

/** RFC 5952 text: lowercase, no leading zeros, the longest run (two or more) of zero groups compressed. */
export function formatIP(ip: IPAddress): string {
  if (ip.family === 4) return [...ip.bytes].join('.');
  const g = Array.from({length: 8}, (_, i) => (ip.bytes[i * 2]! << 8) | ip.bytes[i * 2 + 1]!);
  let best = -1, bestLength = 0;
  for (let i = 0; i < 8;) {
    if (g[i] !== 0) {i++; continue;}
    let j = i; while (j < 8 && g[j] === 0) j++;
    if (j - i > bestLength && j - i >= 2) {best = i; bestLength = j - i;}
    i = j;
  }
  const hex = (from: number, to: number) => g.slice(from, to).map(n => n.toString(16)).join(':');
  return best < 0 ? hex(0, 8) : hex(0, best) + '::' + hex(best + bestLength, 8);
}

const sameIP = (a: IPAddress, b: IPAddress) => a.family === b.family && a.bytes.every((x, i) => x === b.bytes[i]);
export {sameIP};

/** IPv4-mapped IPv6 (::ffff:a.b.c.d) is never a route address: it hides the real family. */
const mappedIPv4 = (ip: IPAddress) => ip.family === 6 && ip.bytes.slice(0, 10).every(b => b === 0) && ip.bytes[10] === 255 && ip.bytes[11] === 255;

function ipClass(ip: IPAddress): AddressClass {
  const b = ip.bytes;
  if (ip.family === 4) {
    if (b[0] === 127) return 'loopback';
    if (b[0] === 10 || (b[0] === 172 && b[1]! >= 16 && b[1]! <= 31) || (b[0] === 192 && b[1] === 168) || (b[0] === 169 && b[1] === 254)) return 'lan';
    if (b[0] === 100 && b[1]! >= 64 && b[1]! <= 127) return 'overlay';
    return 'public';
  }
  if (b.slice(0, 15).every(x => x === 0) && b[15] === 1) return 'loopback';
  if (mappedIPv4(ip)) return ipClass({family: 4, bytes: b.slice(12)});
  // Tailscale's IPv6 range (fd7a:115c:a1e0::/48) is an overlay, not a home network.
  if (b[0] === 0xfd && b[1] === 0x7a && b[2] === 0x11 && b[3] === 0x5c && b[4] === 0xa1 && b[5] === 0xe0) return 'overlay';
  if ((b[0]! & 0xfe) === 0xfc || (b[0] === 0xfe && (b[1]! & 0xc0) === 0x80)) return 'lan';
  return 'public';
}

const SERVER_NAMESPACE = /^ptc-[a-z2-7]{19}[aq]$/;

/** Decode the first label of a Portico direct name to its address: IPv4 dashed, IPv6 with `:` as `-`. */
export function decodeAddressLabel(label: string): IPAddress | undefined {
  if (!label || label.length > 63 || label.startsWith('-') || label.endsWith('-') || label !== label.toLowerCase()) return;
  if (/^\d+(?:-\d+){3}$/.test(label)) {
    const v4 = parseIPv4(label.replace(/-/g, '.'));
    return v4 && {family: 4, bytes: v4};
  }
  const v6 = parseIPv6(label.replace(/-/g, ':'));
  if (!v6) return;
  // Exactly the producer's spelling (live portico.direct DNS refuses every other one).
  const ip: IPAddress = {family: 6, bytes: v6};
  return label === encodeAddressLabel(ip) ? ip : undefined;
}

/** The encoded label for an address (the producer's form; used by tests and diagnostics). */
export function encodeAddressLabel(ip: IPAddress): string {
  if (ip.family === 4) return [...ip.bytes].join('-');
  const canonical = formatIP(ip);
  if (!canonical.startsWith('::') && !canonical.endsWith('::')) return canonical.replace(/:/g, '-');
  return Array.from({length: 8}, (_, i) => ((ip.bytes[i * 2]! << 8) | ip.bytes[i * 2 + 1]!).toString(16)).join('-');
}

export type DirectName = Readonly<{address: IPAddress; namespace: string; domain: string; suffix: string}>;

/** `<encoded-ip>.ptc-<namespace>.<direct domain>`. The domain is operator configuration (production:
 * portico.direct), so only its shape is checked here. */
export function parseDirectName(hostname: string): DirectName | undefined {
  const labels = hostname.toLowerCase().split('.');
  if (labels.length < 4 || hostname !== hostname.toLowerCase()) return;
  const [first, namespace, ...rest] = labels as [string, string, ...string[]];
  if (!SERVER_NAMESPACE.test(namespace) || rest.some(l => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(l))) return;
  const address = decodeAddressLabel(first);
  if (!address || mappedIPv4(address)) return;
  const domain = rest.join('.');
  return {address, namespace, domain, suffix: namespace + '.' + domain};
}

/** The class of a host as it appears in a URL: IP literal, Portico direct name, or other name. */
export function addressClass(host: string): AddressClass {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase();
  if (h === 'localhost') return 'loopback';
  const ip = parseIP(h);
  if (ip) return ipClass(ip);
  const direct = parseDirectName(h);
  if (direct) return ipClass(direct.address);
  if (h === 'ts.net' || h.endsWith('.ts.net')) return 'overlay';
  if (!h.includes('.') || LAN_SUFFIXES.some(s => h.endsWith(s))) return 'lan';
  return 'public';
}

/** The address a host names, when it is an IP literal or a Portico direct name. */
export function hostAddress(host: string): IPAddress | undefined {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase();
  return parseIP(h) ?? parseDirectName(h)?.address;
}

export type CIDR = Readonly<{ip: IPAddress; bits: number}>;
/** A canonical `address/bits` network; anything else is left out by the caller. */
export function parseCIDR(value: string): CIDR | undefined {
  const m = /^([^/]+)\/(\d{1,3})$/.exec(value);
  if (!m) return;
  const ip = parseIP(m[1]!), bits = Number(m[2]);
  if (!ip || bits > (ip.family === 4 ? 32 : 128)) return;
  return {ip, bits};
}
export function cidrContains(network: CIDR, ip: IPAddress): boolean {
  if (network.ip.family !== ip.family) return false;
  for (let bit = 0; bit < network.bits; bit++) {
    const mask = 0x80 >> (bit % 8), i = bit >> 3;
    if ((network.ip.bytes[i]! & mask) !== (ip.bytes[i]! & mask)) return false;
  }
  return true;
}

/** The server's network policy as last read from `/v1/networking/connection`. */
export type ServerNetworkPolicy = Readonly<{lanNetworks?: readonly string[]; secureConnections?: 'preferred' | 'required'}>;

/** Whether a host is on the viewer's home network, by the one definition above. */
export function isLocalHost(host: string, policy?: ServerNetworkPolicy): boolean {
  const cls = addressClass(host);
  if (cls === 'loopback') return true;
  const ip = hostAddress(host);
  if (policy?.lanNetworks && ip) {
    for (const raw of policy.lanNetworks) {const network = parseCIDR(raw); if (network && cidrContains(network, ip)) return true;}
    return false;
  }
  return cls === 'lan';
}
