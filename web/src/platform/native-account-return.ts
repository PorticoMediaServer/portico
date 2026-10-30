import {accountReturnHandoff} from '@core/hosted-security.ts';

export type NativeAccountReturnDispatch = 'forwarded' | 'invalid';

/** Services calls this synchronously, before its generic fragment dispatcher,
 * rendering, storage or any await. The canonical native return owns tx/code;
 * this page never receives the app's private binding or an account session.
 * Undefined means another route. No completion value leaves this function. */
export function dispatchNativeAccountReturn(
  location: Pick<Location, 'href' | 'replace'>,
  history: Pick<History, 'replaceState'>,
  expectedHostedOrigin: string,
): NativeAccountReturnDispatch | undefined {
  let url: URL;
  const href = location.href;
  try {url = new URL(href);} catch {return undefined;}
  if (url.pathname !== '/app/account-return') return undefined;
  // Even an invalid return loses its fragment before validation or handoff.
  // If history refuses this, do not open a code still present in the address bar.
  try {history.replaceState(null, '', url.pathname + url.search);} catch {return 'invalid';}
  try {
    const expected = new URL(expectedHostedOrigin);
    // The existing account client permits explicit local development origins;
    // this is not a browser owner-claim or native LAN callback permission.
    if (expected.origin !== expectedHostedOrigin || expected.username || expected.password || expected.pathname !== '/' || expected.search || expected.hash ||
      !(expected.protocol === 'https:' || expected.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(expected.hostname)) ||
      url.origin !== expected.origin || url.username || url.password) return 'invalid';
    const handoff = accountReturnHandoff(href);
    if (!handoff) return 'invalid';
    location.replace(handoff);
    return 'forwarded';
  } catch {return 'invalid';}
}
