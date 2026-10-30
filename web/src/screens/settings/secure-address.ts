import {routePath} from '../../../../packages/contracts/src/routes.ts';
import {parseRemoteAccess, type RemoteAccess} from '@core/remote-access.ts';
import {parseConnectivityStatus, type ConnectivityStatus} from '@core/server-administration.ts';

/** The bounded transport shape this read needs; the Settings build's own captured
 * proxy satisfies it, so the read keeps that screen's fence and authentication. */
export type SecureAddressReader = Readonly<{
  request: <T>(path: string, method: string, body?: unknown, signal?: AbortSignal) => Promise<T>;
}>;

/** The exact facts the drawn link was composed from. Nothing here is recomputed
 * from a later read or from the page's own address. */
export type SecureAddressReceipt = Readonly<{url: string; status: ConnectivityStatus; remote: RemoteAccess}>;

const LABEL = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
const NUMERIC_LABEL = /^[0-9]+$/;

/** A bare DNS hostname a certificate can serve: no scheme, port, path, userinfo or
 * trailing dot, and never an IP literal or an all-numeric name the server did not
 * publish as a DNS name. */
export function strictCertificateHostname(value: string): string | null {
  if (!value || value.length > 253 || value !== value.toLowerCase()) return null;
  const labels = value.split('.');
  if (labels.length < 2 || labels.some(label => !LABEL.test(label))) return null;
  // 127.0.0.1 and other IPv4 literals are numeric in every label; a numeric final
  // label is never a DNS name.
  if (labels.every(label => NUMERIC_LABEL.test(label)) || NUMERIC_LABEL.test(labels[labels.length - 1]!)) return null;
  return value;
}

/** The constructed URL must mean exactly what was composed: HTTPS, no credentials,
 * the same DNS hostname, and the configured port only when it is not 443. */
function strictSecureOrigin(domain: string, port: number): string | null {
  const origin = `https://${domain}${port === 443 ? '' : `:${port}`}`;
  let parsed: URL;
  try { parsed = new URL(origin); } catch { return null; }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password) return null;
  if (parsed.hostname !== domain || parsed.pathname !== '/' || parsed.search || parsed.hash) return null;
  if (port === 443 ? parsed.port !== '' : parsed.port !== String(port)) return null;
  return origin;
}

/** The own-address URL from the server's own facts: the owner's own address enabled,
 * a serving ready certificate for that exact domain, and the configured remote public
 * port. Omit the port only when it is 443, exactly as the server publishes it. No
 * listen port, route URL or current page origin is ever substituted. */
export function secureAddressReceipt(status: ConnectivityStatus, remote: RemoteAccess): SecureAddressReceipt | null {
  if (status.policy.ownAddressEnabled !== true) return null;
  const certificate = status.tls.customCertificate;
  if (!certificate || certificate.serving !== true || certificate.state !== 'ready') return null;
  const domain = strictCertificateHostname(status.policy.customCertificateDomain);
  if (!domain || certificate.domain !== domain) return null;
  const port = remote.config.publicPort;
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null;
  const url = strictSecureOrigin(domain, port);
  return url ? Object.freeze({url, status, remote}) : null;
}

/** One captured read of the two existing descriptors. The caller owns the signal and
 * its own lifetime check; a retired or replaced caller starts no request and parses
 * nothing from a response that arrived after retirement. */
export async function loadSecureAddress(api: SecureAddressReader, isCurrent: () => boolean, signal: AbortSignal): Promise<SecureAddressReceipt | null> {
  const live = () => { try { return !signal.aborted && isCurrent() === true; } catch { return false; } };
  if (!live()) return null;
  let status: unknown, remote: unknown;
  try {
    [status, remote] = await Promise.all([
      api.request<unknown>(routePath('get_connectivity_status'), 'GET', undefined, signal),
      api.request<unknown>(routePath('get_networking_remote'), 'GET', undefined, signal),
    ]);
  } catch (error) {
    if (signal.aborted || !live()) return null;
    throw error;
  }
  if (!live()) return null;
  try {
    return secureAddressReceipt(parseConnectivityStatus(status), parseRemoteAccess(remote));
  } catch {
    return null;
  }
}
