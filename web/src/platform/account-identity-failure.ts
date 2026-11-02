import type {AppServerSession, AppSessionStore} from '@core/app-sessions.ts';
import {RouteError, sameServerPin} from '@core/route-identity.ts';
import type {BrowserAppSessionActions} from './app-sessions';

export type AccountIdentityFailureBinding = Readonly<{
  serverName: string; failure: 'local-changed' | 'unconfirmed';
  isCurrent: () => boolean; onRetry: () => Promise<void>; onChooseServer: () => void; onForget: () => Promise<void>;
}>;
type Options = Readonly<{
  saved: AppServerSession; revision: number; serverName: string; store: AppSessionStore;
  actions: BrowserAppSessionActions; current: () => boolean; notify: () => void;
  observe?: (changed: () => void) => () => void;
  retry: () => Promise<void>; retire: () => void;
}>;
const ended = () => new RouteError('cancelled', 'The saved server selection changed.');

/** Only a real saved entry may supply the Forget target. Error metadata and
 * the address that failed are never used to reconstruct a session or a pin. */
export class CapturedAccountIdentityFailure {
  readonly failure: 'local-changed' | 'unconfirmed';
  private active = true;
  private disposed = false;
  private pending = false;
  private checks = 0;
  private off?: () => void;
  constructor(private readonly options: Options) {
    this.failure = options.saved.signIn === 'local' ? 'local-changed' : 'unconfirmed';
    try {this.off = options.observe?.(() => {void this.recheck();});}
    catch {this.dispose();}
    if (!this.disposed) void this.recheck();
  }
  isCurrent = () => !this.disposed && this.active && this.options.current();
  private async matches(): Promise<boolean> {
    const record = await this.options.store.read(), saved = this.options.saved;
    const actual = record.servers.find(value => value.connection.pin.serverId === record.selectedServerId);
    return record.revision === this.options.revision && actual?.signIn === saved.signIn
      && sameServerPin(actual.connection.pin, saved.connection.pin)
      && actual.connection.session.sessionFamilyId === saved.connection.session.sessionFamilyId
      && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => actual.connection.session.viewer[key] === saved.connection.session.viewer[key]);
  }
  private async recheck() {
    const check = ++this.checks;
    this.active = false; this.options.notify();
    let matches = false;
    try {matches = await this.matches();} catch { /* A failed read grants no action. */ }
    if (this.disposed || check !== this.checks || !this.options.current()) return;
    if (!matches) this.dispose(); else this.active = true;
    this.options.notify();
  }
  private async command(work: () => Promise<void>): Promise<void> {
    if (this.pending || !this.isCurrent()) throw ended();
    this.pending = true;
    try {
      if (!await this.matches() || !this.isCurrent()) throw ended();
      await work();
    } finally {this.pending = false;}
  }
  binding(chooseServer: () => void): AccountIdentityFailureBinding {
    const choose = () => {if (this.isCurrent()) {this.options.retire(); chooseServer();}};
    return Object.freeze({serverName: this.options.serverName, failure: this.failure, isCurrent: this.isCurrent,
      onRetry: () => this.command(this.options.retry), onChooseServer: choose,
      onForget: () => this.command(async () => {
        if (this.failure !== 'local-changed') throw new RouteError('not_permitted', 'Only the captured Local server can be forgotten here.');
        const removed = await this.options.actions.forgetServer(this.options.saved.connection.session,
          {isCurrent: this.isCurrent, expectedRevision: this.options.revision});
        // This write emits its own change hint after committing. Its success
        // may leave the failure capture inactive, but may not revive a retired entry.
        if (removed && this.options.current()) {this.options.retire(); chooseServer();}
      })});
  }
  dispose() {if (this.disposed) return;this.disposed = true;this.active = false;++this.checks;this.off?.();this.off = undefined;}
}
