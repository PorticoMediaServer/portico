import {porticoAccount, type Device} from '../../../packages/client-core/src/portico-account.ts';
import {heldFor} from '../../../packages/client-core/src/portico-services-native.ts';
import {routeBase64} from '../../../packages/client-core/src/route-identity.ts';
import type {PorticoServerAccess} from '../../../web/src/platform/portico-server-access.ts';
import type {Invoke} from './mpv-player.ts';

/** Portico Services, as the apps reach it (src-tauri/src/services.rs). */
export const SERVICES = 'https://web.getportico.tv';
type Answer = Readonly<{status: number; headers: [string, string][]; body: string}>;

/** `fetch` for Portico Services, through the app: Services doesn't answer the window's page. */
export function servicesFetch(invoke: Invoke): typeof fetch {
  return async (input, init) => {
    init?.signal?.throwIfAborted();
    const url = new URL(input instanceof Request ? input.url : String(input));
    if (url.origin !== SERVICES) throw new TypeError('Only Portico Services is reached this way.');
    const answer = await invoke<Answer>('services_request', {request: {
      method: init?.method ?? 'GET', path: url.pathname + url.search, headers: [...new Headers(init?.headers).entries()],
      body: typeof init?.body === 'string' ? init.body : null,
    }});
    init?.signal?.throwIfAborted();
    return new Response([101, 204, 205, 304].includes(answer.status) || !answer.body ? null : answer.body, {status: answer.status, headers: answer.headers});
  };
}

const wait = (ms: number, signal: AbortSignal) => new Promise<void>((resolve, reject) => {
  const timer = setTimeout(() => { signal.removeEventListener('abort', stop); resolve(); }, ms);
  const stop = () => { clearTimeout(timer); reject(signal.reason); };
  signal.addEventListener('abort', stop, {once: true});
});

/**
 * Signs in with a Portico Account the TV apps' way: Services makes a code for this computer, the
 * person's browser opens on it to approve, and the app collects the sign-in once approved. The
 * account is held only for the server sign-in that asked; nothing of it is saved here.
 */
export async function signInWithCode(invoke: Invoke, openBrowser: (url: string) => void, device: Device, signal: AbortSignal,
  random: (bytes: number) => Uint8Array = bytes => crypto.getRandomValues(new Uint8Array(bytes))): Promise<PorticoServerAccess> {
  const fetcher = servicesFetch(invoke), anyone = porticoAccount(heldFor(''), SERVICES, {fetcher});
  const secret = routeBase64(random(32));
  const secretHash = routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(secret))));
  const code = await anyone.createTV(device, secretHash, crypto.randomUUID(), signal);
  openBrowser(code.verifyUrlWithCode);
  let pause = code.pollAfterMs;
  for (;;) {
    await wait(pause, signal);
    const answer = await anyone.redeemTV(code.id, secret, signal);
    if (answer.state === 'approved' && answer.tokens) {
      const held = porticoAccount(heldFor(answer.tokens.accessToken), SERVICES, {fetcher});
      const person = await held.account(signal);
      return Object.freeze({origin: SERVICES, accountId: person.id, isCurrent: () => !signal.aborted, observe: () => () => {},
        identity: (serverId: string, challenge: string, given?: AbortSignal) => held.serverIdentity(serverId, challenge, given)});
    }
    pause = answer.pollAfterMs ?? code.pollAfterMs;
  }
}
