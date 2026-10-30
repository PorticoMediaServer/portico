import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import type {DirectSnapshot} from '@core/profile-management.ts';
import type {HostedRequestApi} from '@core/portico-servers.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import {ProfileRecoveryController, type ProfileRecoveryState} from '@core/profile-recovery.ts';

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
  portico(hosted: HostedRequestApi, origin: string, accountCurrent: () => boolean) {
    return this.flow.portico(async () => ({hosted, origin, accountCurrent}));
  }
  code(code: string) {return this.flow.code(code);}
  reset(pin: string) {return this.flow.reset(pin);}
  async openProfile(pin: string, current: () => boolean) {
    if (this.disposed || this.flow.getSnapshot().phase !== 'done' || !/^\d{4}$/.test(pin) || !this.authorityCurrent() || !current() || !this.openRecovered) return;
    await this.openRecovered(pin, current);
  }
  dispose() {this.disposed = true; this.flow.dispose();}
}
