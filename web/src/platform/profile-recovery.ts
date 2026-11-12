import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {PINProviderCode} from '@core/portico-pin-provider.ts';
import type {Provider} from '@core/portico-account.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import {account, accountSession} from '../services/account-session';
import {porticoAccountHolder} from './portico-account-holder';
import {ProfileRecoveryController, type ProfileRecoveryState, type PorticoPINProofSource} from '@core/profile-recovery.ts';

/** The PIN recovery lifecycle is shared with native in client-core. A temporary
 * sign-in proves one PIN reset. It never replaces the selected server/viewer,
 * enters the app session store, revokes the selected family or escapes this
 * controller. The browser adds only the guarded open-the-recovered-profile step. */
export class BrowserProfileRecovery {
  private readonly flow: ProfileRecoveryController;
  private disposed = false;
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
  getSnapshot = (): ProfileRecoveryState => this.flow.getSnapshot();
  subscribe = (listener: () => void) => this.flow.subscribe(listener);
  password(password: string) {return this.flow.password(password);}
  /** A Portico Account's proof for a PIN has no route on the new server yet; the flow says so. */
  portico() {
    return this.flow.portico();
  }
  async porticoPassword(password: string, twoStepCode = '') {
    const source = await browserPorticoPINProof();
    if (!this.disposed) await this.flow.porticoPassword(password, twoStepCode, source);
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
  dispose() {this.disposed = true; this.flow.dispose();}
}

/** The browser account held for this one recovery attempt. No local recovery calls this. */
export async function browserPorticoPINProof(): Promise<PorticoPINProofSource> {
  const signIns = accountSession.signIns(), who = await porticoAccountHolder.who();
  return {accountId: who.id, isCurrent: () => accountSession.signedIn() && accountSession.signIns() === signIns,
    identity: (serverId, challenge, proof, signal) => account.serverPINIdentity(serverId, challenge, proof, signal)};
}
