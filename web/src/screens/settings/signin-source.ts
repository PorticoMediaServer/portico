import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {ReadOnlySignInClient, type SignInSettings} from '@core/account-move.ts';
import type {ManagementCapabilities, ProfileAPI} from '@core/profile-management.ts';
import type {SignInKind} from '@core/app-sessions.ts';
import {defaultI18n} from '@i18n';
import type {SettingPresentation} from '../../kit/settings';
import type {SettingsOwnerSwitch} from './owner-switch';
import type {LocalOwnerTransferSource} from './local-owner-transfer';

type Snapshot = Readonly<{loading: boolean; document?: SignInSettings; error?: unknown}>;
const empty: Snapshot = Object.freeze({loading: true});
export type SignInSettingsSourceOptions = Readonly<{
  serverId: string; signIn: SignInKind; isCurrent: () => boolean;
  capabilities: () => ManagementCapabilities;
  ownerSwitch?: SettingsOwnerSwitch;
  /** The owner-supplied guarded local transfer source; absent keeps its row hidden. */
  localOwnerTransfer?: LocalOwnerTransferSource;
  /** The shell rereads actual authority/kind; the Settings source grants nothing. */
  onAuthorityChanged: () => void;
}>;

/** The Sign-in page's private facts, read through the checked guarded client. */
export class SignInSettingsSource {
  readonly ownerSwitch?: SettingsOwnerSwitch;
  readonly localOwnerTransfer?: LocalOwnerTransferSource;
  private readonly client: ReadOnlySignInClient;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private snapshot: Snapshot = empty;
  private loading?: Promise<void>;
  private closed = false;
  private quietRead?: AbortController;
  invalidateQuiet = () => {this.quietRead?.abort();this.quietRead=undefined;};
  constructor(api: ProfileAPI, private readonly options: SignInSettingsSourceOptions) {
    this.client = new ReadOnlySignInClient(api, options);
    this.ownerSwitch = options.ownerSwitch;
    this.localOwnerTransfer = options.localOwnerTransfer;
  }
  private current = () => !this.closed && this.options.isCurrent() && this.options.capabilities().manageSignIn;
  getSnapshot = (): Snapshot => this.current() ? this.snapshot : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(snapshot: Snapshot) {
    if (!this.current()) return;
    this.snapshot = Object.freeze(snapshot);
    for (const listener of this.listeners) listener();
  }
  refresh = async (): Promise<QuietRefreshOutcome> => {
    if (!this.current() || this.loading || !this.snapshot.document) return {kind: 'deferred'};
    this.invalidateQuiet();const before = this.snapshot.document, read = this.quietRead = new AbortController();
    try {
      const document = await this.client.read(AbortSignal.any([this.lifetime.signal,read.signal]));
      if (!this.current() || read.signal.aborted || this.snapshot.document !== before) return {kind: 'deferred'};
      if (document.signIn !== this.options.signIn) {this.dispose();this.options.onAuthorityChanged();return {kind: 'deferred'};}
      this.publish({...this.snapshot, document});return {kind: 'adopted'};
    } catch (error) {
      if (!this.current()) return {kind: 'deferred'};
      if ([401, 403].includes((error as {status?: number})?.status ?? 0)) {this.dispose();this.options.onAuthorityChanged();}
      return read.signal.aborted?{kind:'deferred'}:{kind: 'unreadable', error};
    } finally {if(this.quietRead===read)this.quietRead=undefined;}
  };
  load = (): Promise<void> => {
    if (!this.current() || this.snapshot.document) return Promise.resolve();
    if (this.loading) return this.loading;
    this.invalidateQuiet();this.publish({loading: true});
    const task = this.client.read(this.lifetime.signal).then(document => {
      if (!this.current()) return;
      if (document.signIn !== this.options.signIn) {this.dispose();this.options.onAuthorityChanged();return;}
      this.publish({loading: false, document});
    }, error => {
      if (!this.current()) return;
      if (error && typeof error === 'object' && 'status' in error && (error.status === 401 || error.status === 403)) {
        this.dispose();this.options.onAuthorityChanged();return;
      }
      this.publish({loading: false, error});
    }).finally(() => {if (this.loading === task) this.loading = undefined;});
    this.loading = task;return task;
  };
  /** Only facts from this source are projected. Other domain rows use their actual adapters. */
  rowState = (rowId: string): SettingPresentation | undefined => {
    if (!['signin.method', 'signin.methodLocal', 'signin.owner', 'signin.ownerLocal'].includes(rowId)) return undefined;
    const document = this.getSnapshot().document;
    if (!document) return {present: false};
    const local = document.signIn === 'local';
    if (rowId.endsWith('Local') !== local) return {present: false};
    if (rowId.startsWith('signin.method')) return {present: true, displayValue: defaultI18n.t(local ? 'accounts.settings.local' : 'accounts.settings.portico'), statusTone: 'ok',
      help: defaultI18n.t(local ? 'accounts.settings.localPrivacy' : 'accounts.settings.porticoPrivacy')};
    return {present: true, displayValue: document.owner.username, readOnly: true,
      help: !local && document.owner.email ? defaultI18n.t('accounts.settings.porticoOwner', {email: document.owner.email}) : undefined};
  };
  dispose = () => {
    if (this.closed) return;
    this.invalidateQuiet();this.closed = true;this.lifetime.abort();this.client.dispose();this.ownerSwitch?.dispose();this.localOwnerTransfer?.dispose();this.snapshot = empty;
    for (const listener of this.listeners) listener();
    this.listeners.clear();
  };
}
