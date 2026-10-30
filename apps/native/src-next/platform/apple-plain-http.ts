import {RouteError} from '../../../../packages/client-core/src/route-identity.ts';
import {platformAllowsPlainHttp} from '../../../../packages/client-core/src/connection-policy';

/**
 * Apple's rule for plain HTTP (App Transport Security): the app may use it only on the local
 * network, and every other address must use HTTPS. The Info.plist allows local networking (IP
 * addresses, unqualified names, `.local`) and the private home names `.home.arpa`, `.internal`
 * and `.lan`. This is the platform floor from the shared definition (client-core
 * connection-policy.ts): home and overlay IP literals (100.64/10 and Tailscale's IPv6 range among
 * them) and those names. Whether a given connection may use it is the insecure setting's call.
 */
export function localPlainHttpHost(hostname: string): boolean {
  return !!hostname && platformAllowsPlainHttp(hostname, {insecure: 'local', platform: 'apple'});
}

/** Throws the plain explanation when Apple would refuse a plain HTTP request to this URL. */
export function checkApplePlainHttp(url: string): void {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return;
  }
  if (parsed.protocol === 'http:' && !localPlainHttpHost(parsed.hostname)) {
    throw new RouteError('plain_http_remote', 'On Apple devices, plain HTTP works only on your home network. Use this server’s https:// address.');
  }
}
