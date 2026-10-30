import {ApiError} from '@core/index.ts';
import {buildHostedSignInStartURL, parseHostedSignInStartURL, verifyHostedSignInStart} from '@core/hosted-sign-in-start.ts';
import {completeRegisteredReturn} from '@core/same-tab-return.ts';
import {BrowserProtectedStore} from '../platform/server-connections';
import {servicesTab} from './intake';
import {currentAccountWork, type AccountIdentity, type ServicesAccount} from './session';

export type SignInCapture = Readonly<{href?: string}>;
type Pending = Readonly<{version: 1; href: string}>;
export type SignInStore = Pick<BrowserProtectedStore<Pending>, 'read' | 'change'>;
export type HostedSignIn = Awaited<ReturnType<typeof verifyHostedSignInStart>>;
const invalid = () => new ApiError(400, 'invalid_return', 'invalid_return', false);

export function captureHostedSignIn(page: Pick<Location, 'href' | 'pathname' | 'search' | 'hash'>,
  history: Pick<History, 'state' | 'replaceState'>, origin: string): SignInCapture | undefined {
  if (!page.hash.startsWith('#signIn=')) return;
  const href = page.href;
  history.replaceState(history.state, '', page.pathname + page.search);
  // Shape only. Signature and all signed bindings are checked before rendering.
  return parseHostedSignInStartURL(href, origin) ? {href} : {};
}
export function hostedSignInStore(): SignInStore {
  return new BrowserProtectedStore<Pending>('portico.services-sign-in.v1:' + servicesTab(), 16384, true);
}
export async function restoreHostedSignIn(store: SignInStore, origin: string, capture?: SignInCapture): Promise<HostedSignIn | undefined> {
  // Claim this tab's pending slot before async signature work. A later capture
  // supersedes it; neither a late success nor failure may replace that capture.
  if (capture && (!capture.href || !parseHostedSignInStartURL(capture.href, origin))) {
    await store.change(() => undefined); throw invalid();
  }
  const saved = capture ? {version: 1 as const, href: capture.href!} : await store.read();
  if (!saved) return;
  if (capture) await store.change(() => saved);
  const mine = (current: Pending | undefined) => current?.version === saved.version && current?.href === saved.href;
  try {
    if (saved.version !== 1) throw invalid();
    const parsed = parseHostedSignInStartURL(saved.href, origin);
    if (!parsed) throw invalid();
    const verified = await verifyHostedSignInStart(parsed);
    if (!mine(await store.read())) throw invalid();
    return verified;
  } catch (error) {await store.change(current => mine(current) ? undefined : current); throw error;}
}

/** The only answer-producing entry point. The view calls this on Continue;
 * restoration and cached-account presentation never call Start or Complete. */
export async function continueHostedSignIn(account: ServicesAccount, pending: HostedSignIn, expected: AccountIdentity,
  store: SignInStore, signal: AbortSignal, navigate: (location: string) => void) {
  const {handoff} = pending;
  if (account.api.origin !== handoff.hostedOrigin) throw invalid();
  const href = buildHostedSignInStartURL(handoff);
  const current = async () => {
    signal.throwIfAborted();
    const saved = await store.read();
    const parsed = saved && parseHostedSignInStartURL(saved.href, handoff.hostedOrigin);
    if (!parsed || saved?.version !== 1 || saved.href !== href) throw invalid();
    await verifyHostedSignInStart(parsed);
    signal.throwIfAborted();
  };
  await current();
  const location = await completeRegisteredReturn({request: async <T>(path: string, method = 'GET', body?: unknown) => {
    await current();
    return currentAccountWork(account, session => account.api.send<T>(path, method, body, signal, session.accessToken), expected);
  }}, handoff.start, handoff.registration, signal);
  await current();
  await currentAccountWork(account, async () => {signal.throwIfAborted();}, expected);
  let cleared = false;
  await store.change(saved => {
    if (saved?.version === 1 && saved.href === href) {cleared = true; return undefined;}
    return saved;
  });
  if (!cleared) throw invalid();
  signal.throwIfAborted();
  if (Date.parse(handoff.expiresAt) <= Date.now()) throw invalid();
  // No await between the final live-family check and same-tab navigation.
  const session = account.service.getSnapshot().session;
  if (!session || session.account.id !== expected.accountId || session.familyId !== expected.familyId) throw invalid();
  navigate(location);
}
