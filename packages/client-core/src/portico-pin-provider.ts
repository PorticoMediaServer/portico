import {porticoAccount, ServicesError, type Provider} from './portico-account.ts';
import {parseAccountReturn} from './hosted-security.ts';
import {routeBase64, type RouteCrypto} from './route-identity.ts';
import {throwIfAborted} from './abort-lifetime.ts';

export type PINProviderCode = string | (() => Promise<string>);

/** Platform interaction only: the proof, binding secret and provider token never enter a screen snapshot. */
export type PINProviderPorts = Readonly<{
  accountOrigin: string; current: () => boolean; signal: AbortSignal;
  crypto: Pick<RouteCrypto, 'random' | 'sha256'>;
  browser?: (url: string, expectedFlow: string) => Promise<string>;
  apple?: (nonce: string) => Promise<{idToken: string}>;
}>;

/** Exactly one new provider flow for this server challenge. No sign-in/session adoption or retry. */
export async function providerPINIdentity(client: ReturnType<typeof porticoAccount>, provider: Provider,
  pin: {serverId: string; challenge: string}, twoStepCode: PINProviderCode, ports: PINProviderPorts): Promise<{identity: unknown}> {
  const check = () => {
    throwIfAborted(ports.signal);
    if (!ports.current()) throw new ServicesError(409, 'account_changed');
  };
  const code = async () => {const value = typeof twoStepCode === 'string' ? twoStepCode : await twoStepCode(); check(); return value;};
  check();
  if (provider === 'apple' && ports.apple) {
    const started = await client.startPINApple(pin, ports.signal); check();
    const proof = await ports.apple(started.nonce); check();
    const answer = await client.completePINApple(started.id, proof.idToken, await code(), ports.signal); check();
    return answer;
  }
  if (!ports.browser) throw new ServicesError(409, 'provider_unavailable_here');
  const secret = routeBase64(ports.crypto.random(32));
  const digest = routeBase64(await ports.crypto.sha256(new TextEncoder().encode(secret))); check();
  const started = await client.startPINProvider(provider, digest, pin, ports.signal); check();
  const returned = await ports.browser(started.authorizeUrl, started.id); check();
  const proof = parseAccountReturn(returned, started.id, ports.accountOrigin);
  if (!proof.completionCode) throw new ServicesError(409, 'provider_return_incomplete');
  const answer = await client.completePINProvider(started.id, proof.completionCode, secret, await code(), ports.signal); check();
  return answer;
}
