import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {PINProviderCode} from '@core/portico-pin-provider.ts';
import type {Provider} from '@core/portico-account.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import {account, accountSession} from '../services/account-session';
import {porticoAccountHolder} from './portico-account-holder';
import {ProfileRecoveryController, type ProfileRecoveryState, type PorticoPINProofSource} from '@core/profile-recovery.ts';
import {CodedError} from '@core/server-messages.ts';
import {currentWebEnvironment} from './environment';

/** The Portico Account a PIN recovery on a Portico Accounts server asks: whose password it is, whether
 * it has one, and whether its two-step code is asked with it (an empty code would count as a wrong one). */
export type PorticoRecoveryAccount = Readonly<{username: string; hasPassword: boolean; twoStep: boolean}>;
/** Where the Portico step of a recovery stands, beside the shared controller's own state. */
export type PorticoRecoveryStep = Readonly<{phase: 'loading'} | {phase: 'ready'; account: PorticoRecoveryAccount} | {phase: 'elsewhere'} | {phase: 'failed'; error: unknown}>;
export type BrowserRecoveryState = ProfileRecoveryState & Readonly<{portico?: PorticoRecoveryStep}>;

/** The PIN recovery lifecycle is shared with native in client-core. A temporary
 * sign-in proves one PIN reset. It never replaces the selected server/viewer,
 * enters the app session store, revokes the selected family or escapes this
 * controller. The browser adds only the guarded open-the-recovered-profile step. */
export class BrowserProfileRecovery {
  private readonly flow: ProfileRecoveryController;
  private disposed = false;
  private source?: PorticoPINProofSource;
  private portico?: PorticoRecoveryStep;
  private merged?: {base: ProfileRecoveryState; portico?: PorticoRecoveryStep; value: BrowserRecoveryState};
  private readonly listeners = new Set<() => void>();
  constructor(api: HttpLocalApi, readonly snapshot: DirectSnapshot, readonly profileId: string, kind: SignInKind, selectedFamilyId: string,
    private readonly authorityCurrent: () => boolean, temporaryAPI?: (session: LocalSession) => HttpLocalApi,
    private readonly openRecovered?: (pin: string, current: () => boolean) => Promise<void>) {
    this.flow = new ProfileRecoveryController({api, snapshot, profileId, selectedFamilyId, kind, authorityCurrent,
      temporary: temporaryAPI ?? (session => {
        const temporary = new HttpLocalApi(api.baseUrl, session.accessToken), route = api.getRouteConnection();
        if (route) temporary.useRoutes(route);
        return temporary;
      })});
  }
  /** The shared controller's state, with the Portico step's (one stable object per change). */
  getSnapshot = (): BrowserRecoveryState => {
    const base = this.flow.getSnapshot();
    if (this.merged?.base !== base || this.merged.portico !== this.portico) this.merged = {base, portico: this.portico, value: this.portico ? {...base, portico: this.portico} : base};
    return this.merged.value;
  };
  subscribe = (listener: () => void) => {
    this.listeners.add(listener); const off = this.flow.subscribe(listener);
    return () => {this.listeners.delete(listener); off();};
  };
  private step(next: PorticoRecoveryStep) {if (!this.disposed) {this.portico = next; this.listeners.forEach(listener => listener());}}
  /** Reads the Portico Account this browser holds for the Portico step. Off web.getportico.tv there is
   * none to use, and the step says where to go instead; a failed read stays in this flow's state. */
  async preparePortico() {
    if (this.disposed || this.flow.getSnapshot().phase !== 'portico' || this.portico?.phase === 'loading') return;
    if (!currentWebEnvironment().hostedWeb) {this.step({phase: 'elsewhere'}); return;}
    this.step({phase: 'loading'});
    try {
      const source = await browserPorticoPINProof(), who = await porticoAccountHolder.who();
      if (!source.isCurrent() || who.id !== source.accountId) throw new CodedError('account_changed', 'The Portico Account changed. Try again.');
      this.source = source;
      this.step({phase: 'ready', account: Object.freeze({username: who.username, hasPassword: who.hasPassword, twoStep: who.twoStep})});
    } catch (error) {this.step({phase: 'failed', error});}
  }
  password(password: string) {return this.flow.password(password);}
  async porticoPassword(password: string, twoStepCode = '') {
    const source = this.source;
    if (!this.disposed && source && this.portico?.phase === 'ready') await this.flow.porticoPassword(password, twoStepCode, source);
  }
  /** The account-pages return flow supplies its captured Services source; an own-address page has no Services cookie. */
  porticoProvider(provider: Provider, twoStepCode: PINProviderCode, source: PorticoPINProofSource) {
    if (!this.disposed) return this.flow.porticoProvider(provider, twoStepCode, source);
  }
  code(code: string) {return this.flow.code(code);}
  reset(pin: string) {return this.flow.reset(pin);}
  async openProfile(pin: string, current: () => boolean) {
    if (this.disposed || this.flow.getSnapshot().phase !== 'done' || !/^\d{4}$/.test(pin) || !this.authorityCurrent() || !current() || !this.openRecovered) return;
    await this.openRecovered(pin, current);
  }
  dispose() {this.disposed = true; this.source = undefined; this.listeners.clear(); this.flow.dispose();}
}

/** The browser account held for this one recovery attempt. No local recovery calls this. */
export async function browserPorticoPINProof(): Promise<PorticoPINProofSource> {
  // Only web.getportico.tv holds a Portico Account; a server's own address never uses the account client.
  if (location.origin !== 'https://web.getportico.tv') throw new CodedError('portico_account_elsewhere', 'Reset this PIN at web.getportico.tv.');
  const signIns = accountSession.signIns(), who = await porticoAccountHolder.who();
  return {accountId: who.id, isCurrent: () => accountSession.signedIn() && accountSession.signIns() === signIns,
    identity: (serverId, challenge, proof, signal) => account.serverPINIdentity(serverId, challenge, proof, signal)};
}
