import {addressClass, hostAddress, isLocalHost, parseIP, type ServerNetworkPolicy} from './network-locality.ts';
import {browserRestrictedOrigin} from './browser-ports.ts';

/**
 * Which plain-HTTP (insecure) connections this device may use (P2-69, Plex's "Allow insecure
 * connections"). Secure connections are always allowed and always preferred: an insecure route is
 * used only when no secure one works.
 *
 * - `never`: HTTPS only, except an address the person typed with `http://` themselves.
 * - `local`: also plain HTTP on the home network (the default).
 * - `always`: also plain HTTP away from home, where the platform allows it.
 *
 * The server's "Secure connections: Required" refuses plain HTTP for everyone, typed or not.
 */
export type InsecureConnections = 'never' | 'local' | 'always';
export const DEFAULT_INSECURE_CONNECTIONS: InsecureConnections = 'local';
export const insecureConnectionChoices = ['never', 'local', 'always'] as const;

/**
 * - `apple`: iOS, iPadOS, tvOS. Plain HTTP only to local addresses App Transport Security allows;
 *   `always` reads as `local`.
 * - `native`: Android, Android TV, Fire OS, Vega.
 * - `web-hosted`: the web app on its HTTPS origin. Plain HTTP is mixed content, except private IP
 *   literals in a browser that supports Local Network Access (`webPrivateLiteralHttp`).
 * - `web-server`: the server's own web UI; the page's own transport is already the person's choice.
 * - `cast`: the Cast receiver page. HTTPS only.
 */
export type ConnectionPlatform = 'apple' | 'native' | 'web-hosted' | 'web-server' | 'cast';
export type ConnectionPolicy = Readonly<{insecure: InsecureConnections; platform: ConnectionPlatform; webPrivateLiteralHttp?: boolean}>;
export const DEFAULT_CONNECTION_POLICY: ConnectionPolicy = Object.freeze({insecure: DEFAULT_INSECURE_CONNECTIONS, platform: 'native'});

/** Where a candidate came from. `typed` is an address the person entered themselves. */
export type CandidateSource = 'listed' | 'nearby' | 'typed' | 'derived';

export function parseInsecureConnections(value: unknown): InsecureConnections {
  return value === 'never' || value === 'local' || value === 'always' ? value : DEFAULT_INSECURE_CONNECTIONS;
}

/** The choices a platform offers: Apple has no remote plain HTTP, so it never offers `always`. */
export function insecureChoicesFor(platform: ConnectionPlatform): readonly InsecureConnections[] {
  return platform === 'apple' ? ['never', 'local'] : insecureConnectionChoices;
}

/** The setting as the platform applies it. */
export function effectiveInsecure(policy: ConnectionPolicy): InsecureConnections {
  if (policy.platform === 'cast') return 'never';
  if (policy.platform === 'apple' && policy.insecure === 'always') return 'local';
  return policy.insecure;
}

const ATS_LOCAL_NAME = /^(?:[^.]+|.+\.(?:local|lan|internal|home\.arpa))$/;

/** The platform floor for plain HTTP to a host, before the person's setting is considered. */
export function platformAllowsPlainHttp(host: string, policy: ConnectionPolicy): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase(), cls = addressClass(h), literal = !!parseIP(h);
  switch (policy.platform) {
    case 'cast': return false;
    case 'web-server': return true;
    case 'web-hosted': return !!policy.webPrivateLiteralHttp && literal && (cls === 'lan' || cls === 'loopback');
    // ATS: local networking covers IP literals and unqualified/.local names; the Info.plist adds
    // .lan, .internal and .home.arpa. A public address or name is never plain HTTP on Apple.
    case 'apple': return (cls === 'loopback' || cls === 'lan' || cls === 'overlay') && (literal || (h !== 'ts.net' && !h.endsWith('.ts.net') && ATS_LOCAL_NAME.test(h)));
    case 'native': return true;
  }
}

/** Web pages and the Cast receiver run in a browser. */
export function browserPlatform(platform: ConnectionPlatform): boolean {return platform === 'web-hosted' || platform === 'web-server' || platform === 'cast';}

/** Whether a candidate may be tried at all. */
export function candidateAllowed(baseUrl: string, source: CandidateSource, policy: ConnectionPolicy, server?: ServerNetworkPolicy): boolean {
  let url: URL;
  try {url = new URL(baseUrl);} catch {return false;}
  // A browser refuses some ports outright; trying them only costs a failed attempt.
  if (browserPlatform(policy.platform) && browserRestrictedOrigin(baseUrl)) return false;
  if (url.protocol === 'https:') return true;
  if (url.protocol !== 'http:' || server?.secureConnections === 'required') return false;
  if (!platformAllowsPlainHttp(url.hostname, policy)) return false;
  // An address typed with http:// is the person's own choice; the setting covers everything else.
  if (source === 'typed') return true;
  const setting = effectiveInsecure(policy);
  return setting === 'always' || (setting === 'local' && isLocalHost(url.hostname, server));
}

/** Whether an insecure route must be confirmed local by the server before credentials go over it. */
export function insecureNeedsLocalConfirmation(baseUrl: string, source: CandidateSource, policy: ConnectionPolicy): boolean {
  return baseUrl.startsWith('http:') && source !== 'typed' && policy.platform !== 'web-server' && effectiveInsecure(policy) !== 'always';
}

/** The plain-HTTP twin of a signed LAN route, for when the router refuses to resolve its name. */
export function derivedLocalOrigin(address: string, port: number): string | undefined {
  const ip = parseIP(address);
  if (!ip || !Number.isInteger(port) || port < 1 || port > 65535) return;
  return 'http://' + (ip.family === 6 ? '[' + address + ']' : address) + ':' + port;
}

export type ConnectionDetails = Readonly<{
  origin: string;
  secure: boolean;
  /** The server's own answer when known, otherwise this device's classification. */
  local: boolean;
  confirmed: boolean;
  family: 'ipv4' | 'ipv6' | 'name';
}>;

/** What Settings › This device › Connection shows. `serverRemote` is the server's answer for this origin. */
export function connectionDetails(origin: string | null | undefined, server?: ServerNetworkPolicy, serverRemote?: boolean): ConnectionDetails | undefined {
  if (!origin) return;
  let url: URL;
  try {url = new URL(origin);} catch {return;}
  const ip = hostAddress(url.hostname);
  const family = ip ? (ip.family === 6 ? 'ipv6' : 'ipv4') : 'name';
  return Object.freeze({origin: url.origin, secure: url.protocol === 'https:', local: serverRemote === undefined ? isLocalHost(url.hostname, server) : !serverRemote, confirmed: serverRemote !== undefined, family});
}

/** The message key for the Status row: "Secure, at home" and its three siblings. */
export function connectionStatusKey(details: ConnectionDetails): 'secureHome' | 'secureAway' | 'insecureHome' | 'insecureAway' {
  return details.secure ? (details.local ? 'secureHome' : 'secureAway') : (details.local ? 'insecureHome' : 'insecureAway');
}

/**
 * The origin a Cast receiver (an HTTPS page on the same network as the sender) should use: the
 * sender's current route when it is secure, otherwise the best secure route the server has listed,
 * local first. Undefined when there is none: casting then needs a secure connection.
 */
export function castOrigin(current: string | null | undefined, candidates: readonly Readonly<{baseUrl: string; class: string}>[], server?: ServerNetworkPolicy): string | undefined {
  if (current?.startsWith('https://') && !browserRestrictedOrigin(current)) return current;
  const secure = candidates.filter(c => c.baseUrl.startsWith('https://') && !browserRestrictedOrigin(c.baseUrl));
  const rank = (c: {baseUrl: string; class: string}) => {
    try {return isLocalHost(new URL(c.baseUrl).hostname, server) ? 0 : c.class === 'manual' ? 2 : 1;} catch {return 3;}
  };
  return [...secure].sort((a, b) => rank(a) - rank(b))[0]?.baseUrl;
}
