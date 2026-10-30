import {HomeService, type HomeSnapshot} from '../../../../packages/client-core/src/home-service';
import {InboxService} from '../../../../packages/client-core/src/inbox';
import {parseNotificationSummary} from '../../../../packages/client-core/src/notifications';
import {PreferencesReader} from '../../../../packages/client-core/src/preferences-reader';
import type {DeviceClass, PreferencePatch, PreferenceScope, PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';
import {fetchGuideSourceSummaries} from '../../../../packages/client-core/src/guide/directory';
import {parseDirectSnapshot, type DirectSnapshot} from '../../../../packages/client-core/src/profile-management';
import {routePath} from '../../../../packages/contracts/src/routes';
import {createSharedReads} from './shared-read';
import {profileSessionRole} from './account-binding';
import {createHomeReadLog} from './home-read-log';
import type {HttpLocalApi, LocalSession} from '../../../../packages/client-core/src';
import type {NativeShellAccountBinding} from './account-binding';

type Domain = 'preferences' | 'inbox' | 'guide' | 'home';
type Facts = Readonly<{preferences: PreferenceSnapshot; unread: number; hasLiveTV: boolean}>;
type Options = {
  viewer: LocalSession['viewer']; deviceClass: Extract<DeviceClass, 'mobile' | 'television'>;
  signal: AbortSignal; current: () => boolean; onAuthorityEnded: () => void;
  /** CC-03: the server changed this account's role; role-dependent services rebuild. */
  onRoleChanged?: () => void;
  accountBinding: NativeShellAccountBinding;
};
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const cancelled = () => Object.assign(new Error('native_shell_cancelled'), {name: 'AbortError'});
const invalid = () => Object.assign(new Error('invalid_shell_response'), {code: 'invalid_response'});
const changed = () => Object.assign(new Error('viewer_scope_changed'), {code: 'access_refused'});
const closedHome: HomeSnapshot = Object.freeze({phase: 'idle', paging: Object.freeze(new Set<string>())});
export const nativeShellPermissionDenied = (error: unknown): boolean => object(error) &&
  ([401, 403].includes(Number(error.status)) || (typeof error.code === 'string' && ['unauthorized', 'forbidden', 'access_refused', 'identity_mismatch'].includes(error.code)));

/** Portico-linked members also hold local server sessions. Legacy Hosted principals
 * must re-admit through the signed challenge, never be relabelled by the shell. */
export function nativeShellDirectViewer(viewer: LocalSession['viewer']): void {
  if (viewer.authority !== 'local') throw Object.assign(new Error('native_shell_hosted_account_binding_unavailable'), {code: 'native_shell_hosted_account_binding_unavailable'});
  if (!['owner', 'admin', 'member'].includes(viewer.role)) throw changed();
}

const opaqueFence = (fence: string): boolean => !!fence && fence.length <= 256 && !/[\x00-\x1f\x7f]/.test(fence);

/** Genuine core readers, owned by one restored native authority. Their opaque fences belong
 * to separate response domains; none is derived from, or compared with, another domain. */
export class NativeShellBootstrapServices {
  readonly home: HomeService;
  private readonly preferences: PreferencesReader;
  private readonly inbox: InboxService;
  private readonly controllers = new Set<AbortController>();
  private readonly fences = new Map<Domain, string>();
  /** H-11: the Home manifest's privacy fence (or its viewer fence on older servers) owns retirement. */
  private homeShellFence?: string;
  private disposed = false;
  private guideDenied = false;
  private inboxLoaded = false;
  private inboxFailure?: unknown;
  private snapshot?: Facts;
  private reading?: Promise<Facts>;
  private readingController?: AbortController;
  private unreadReading?: Promise<number>;
  private unreadController?: AbortController;
  private guideReading?: Promise<boolean>;
  private guideController?: AbortController;
  private preferenceRequestBatch?: AbortSignal;
  private retirement?: Error;
  private readonly api: Pick<HttpLocalApi, 'request'>;
  private readonly options: Options;
  constructor(api: Pick<HttpLocalApi, 'request'>, options: Options) {
    this.api = api; this.options = options;
    nativeShellDirectViewer(options.viewer);
    this.preferences = new PreferencesReader({request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.call<T>('preferences', path, method, body, signal, this.preferenceRequestBatch)},
      {serverId: options.viewer.serverId, viewerId: JSON.stringify([options.viewer.authority, options.viewer.accountId, options.viewer.profileId])});
    this.inbox = new InboxService({request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      const batch = this.unreadController;
      const noteCancelled = () => {if (batch === this.unreadController && !batch?.signal.aborted) this.inboxFailure = cancelled();};
      signal?.addEventListener('abort', noteCancelled, {once: true});
      try {
        const raw = await this.call<T>('inbox', path, method, body, signal);
        // refreshUnread intentionally swallows malformed summaries as well as transport
        // failures. Validate with its genuine parser before that forgiving boundary.
        if (path.split('?')[0] === routePath('get_notifications_unread_count')) {
          if (!object(raw)) throw invalid();
          parseNotificationSummary(raw.data);
        }
        return raw;
      }
      catch (error) {if (batch === this.unreadController && !batch?.signal.aborted) this.inboxFailure = error; throw error;}
      finally {signal?.removeEventListener('abort', noteCancelled);}
    }});
    // Home's document is read once at a time (E-mobile-01 cold launch): a load and the quiet
    // re-reads that arrive while it runs share one request.
    const homeReads = createSharedReads(), logHomeRead = createHomeReadLog();
    this.home = new HomeService({request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) =>
      (method ?? 'GET') === 'GET' && body === undefined && path.split('?')[0] === routePath('get_home')
        ? homeReads<T>(path, signal, shared => {logHomeRead(); return this.call<T>('home', path, method, body, shared);})
        : this.call<T>('home', path, method, body, signal)},
      options.deviceClass === 'television' ? {initialLimit: 20} : {});
    // Core disposal aborts owned reads but retains its last snapshot. A retained runtime
    // must never expose that body after this authority has been retired.
    const readHome = this.home.getSnapshot;
    this.home.getSnapshot = () => this.isCurrent() ? readHome() : closedHome;
    options.signal.addEventListener('abort', this.dispose, {once: true});
    if (options.signal.aborted) this.dispose();
  }
  private isCurrent(): boolean {
    if (!this.disposed && (!this.options.current() || this.options.signal.aborted)) this.dispose();
    return !this.disposed;
  }
  private assertCurrent(): void {if (!this.isCurrent()) throw cancelled();}
  private retire(): never {
    const error = this.retirement ??= changed();
    if (!this.disposed) {this.dispose(); this.options.onAuthorityEnded();}
    throw error;
  }
  private retireForRole(): never {
    const error = this.retirement ??= changed();
    if (!this.disposed) {this.dispose(); (this.options.onRoleChanged ?? this.options.onAuthorityEnded)();}
    throw error;
  }
  private checkFence(domain: Domain, raw: unknown): void {
    if (!object(raw)) throw invalid();
    const scope = domain === 'preferences' || domain === 'inbox' ? raw.scope : raw;
    // Layout mutation receipts have no viewer fence. Their canonical parser still validates them.
    if (domain === 'home' && !('viewerFence' in raw)) return;
    if (!object(scope) || typeof scope.viewerFence !== 'string' || domain !== 'home' && typeof scope.serverId !== 'string') throw invalid();
    // Home row pages carry the Home fence without repeating the server ID.
    if ((domain !== 'home' || 'serverId' in scope) && scope.serverId !== this.options.viewer.serverId) this.retire();
    const fence = scope.viewerFence;
    if (domain === 'preferences' || domain === 'inbox') {
      if (!/^[a-f0-9]{64}$/.test(fence)) throw invalid();
    } else if (!opaqueFence(fence)) throw invalid();
    if (domain === 'home' && this.checkHomeFence(scope, fence)) return;
    if (this.fences.has(domain) && this.fences.get(domain) !== fence || domain === 'guide' && this.guideDenied) this.retire();
    this.fences.set(domain, fence);
  }
  /** A library added or removed moves only the viewer fence: Home re-reads, nothing retires.
   * A row page never carries the privacy fence, so a newer viewer fence there is left to Home's
   * own row checks; the next manifest decides. Older servers keep retiring on the viewer fence. */
  private checkHomeFence(scope: Record<string, unknown>, fence: string): boolean {
    const privacy = scope.privacyFence;
    if (privacy !== undefined && (typeof privacy !== 'string' || !opaqueFence(privacy))) throw invalid();
    if ('serverId' in scope) {
      const shell = privacy ?? fence;
      if (this.homeShellFence !== undefined && this.homeShellFence !== shell) this.retire();
      this.homeShellFence = shell; this.fences.set('home', fence); return true;
    }
    if (this.homeShellFence === undefined || this.homeShellFence === this.fences.get('home')) return false;
    if (!this.fences.has('home')) this.fences.set('home', fence);
    return true;
  }
  private async call<T>(domain: Domain | undefined, path: string, method = 'GET', body?: unknown, signal?: AbortSignal, preferenceBatch?: AbortSignal): Promise<T> {
    this.assertCurrent();
    const controller = new AbortController(), abort = () => controller.abort();
    const batch = domain === 'preferences' ? preferenceBatch : domain === 'inbox' ? this.unreadController?.signal : domain === 'guide' ? this.guideController?.signal : undefined;
    this.controllers.add(controller);
    signal?.addEventListener('abort', abort, {once: true});
    batch?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted || batch?.aborted) abort();
    try {
      const raw = await this.api.request<T>(path, method, body, controller.signal);
      this.assertCurrent();
      if (controller.signal.aborted) throw cancelled();
      if (domain) this.checkFence(domain, raw);
      return raw;
    } catch (error) {
      if (this.retirement && error === this.retirement) throw error;
      if (!this.isCurrent() || controller.signal.aborted) throw cancelled();
      // A profile with no guide entitlement can still open Home. Once a guide scope
      // was accepted, losing that permission is an authority change, not an empty list.
      const initialGuideDenial = domain === 'guide' && object(error) && error.status === 403 && !this.fences.has('guide');
      if (this.isCurrent() && nativeShellPermissionDenied(error) && !initialGuideDenial) this.retire();
      throw error;
    } finally {signal?.removeEventListener('abort', abort); batch?.removeEventListener('abort', abort); this.controllers.delete(controller);}
  }
  /** Other bootstrap reads share the same abort/current-authority boundary. */
  request = <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => this.call<T>(undefined, path, method, body, signal);
  private async assertAccountBinding(account?: DirectSnapshot): Promise<void> {
    try {
      if (account) this.options.accountBinding.assertAccount(account);
      await this.options.accountBinding.assertCurrent();
    }
    catch (error) {
      if (this.isCurrent() && nativeShellPermissionDenied(error)) this.retire();
      throw error;
    }
    this.assertCurrent();
  }
  readAccount = async (signal?: AbortSignal) => {
    if (signal?.aborted) throw cancelled();
    await this.assertAccountBinding();
    this.assertCurrent();
    const viewer = this.options.viewer;
    const raw = await this.request(routePath('get_direct_account'), 'GET', undefined, signal);
    if (object(raw) && (typeof raw.serverId === 'string' && raw.serverId !== viewer.serverId
      || typeof raw.authority === 'string' && raw.authority !== viewer.authority
      || object(raw.account) && typeof raw.account.id === 'string' && raw.account.id !== viewer.accountId)) this.retire();
    const account = parseDirectSnapshot(raw, {authority: 'local', serverId: viewer.serverId, accountId: viewer.accountId}, true);
    await this.assertAccountBinding(account);
    this.assertCurrent();
    if (signal?.aborted) throw cancelled();
    if (account.account.disabled) this.retire();
    // A non-primary profile's session is a member's, whatever the account's own role (E-mobile-01/25).
    if (profileSessionRole(account.account, viewer.profileId) !== viewer.role) this.retireForRole();
    return account;
  };
  private async readInbox(): Promise<number> {
    const batch = this.unreadController;
    const currentRead = () => {this.assertCurrent(); if (batch !== this.unreadController || batch?.signal.aborted) throw cancelled();};
    this.inboxFailure = undefined;
    // A profile inbox advertises only that audience. The actual owner's account-admin
    // inbox advertises both; let that server answer drive the core badge's audience set.
    if (!this.inboxLoaded) {
      await this.inbox.load(this.options.viewer.role === 'owner' ? 'account-admin' : 'profile'); currentRead();
      if (this.inboxFailure) throw this.inboxFailure;
      if (this.inbox.getSnapshot().phase !== 'ready') throw invalid();
      this.inboxLoaded = true;
    }
    await this.inbox.refreshUnread(); currentRead();
    // InboxService intentionally preserves the badge on failure. Bootstrap must still
    // report that failed read and must never mistake a swallowed denial for success.
    if (this.inboxFailure) throw this.inboxFailure;
    return this.inbox.getSnapshot().unread;
  }
  private async readGuide() {
    try {return await fetchGuideSourceSummaries({request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.call<T>('guide', path, method, body, signal)},
      this.options.viewer.serverId, Intl.DateTimeFormat().resolvedOptions().timeZone, this.options.signal);}
    catch (error) {
      this.assertCurrent();
      if (object(error) && error.status === 403 && !this.fences.has('guide')) {this.guideDenied = true; return undefined;}
      throw error;
    }
  }
  /** Badges and guide availability may arrive after the admitted Home document.
   * Each resource retains its own cancellation and fence, including late denials. */
  readUnread = (signal?: AbortSignal, afterCurrent = false): Promise<number> => {
    if (signal?.aborted) return Promise.reject(cancelled());
    if (this.unreadReading) {
      const previous = this.unreadReading;
      // The shared inbox has mutable load/unread state. Let a cancelled read
      // settle before starting its replacement, but do not hand a fresh caller
      // the old, already-aborted result after foregrounding. A new event hint
      // also needs a read issued after it, not a full refresh's earlier answer.
      if (afterCurrent || this.unreadController?.signal.aborted) return previous.then(() => this.readUnread(signal), () => this.readUnread(signal));
      return previous;
    }
    const controller = new AbortController(), abort = () => controller.abort();
    this.unreadController = controller;
    signal?.addEventListener('abort', abort, {once: true});
    const pending = this.readInbox().then(unread => {
      this.assertCurrent(); if (controller.signal.aborted) throw cancelled(); return unread;
    }).finally(() => {
      controller.abort(); signal?.removeEventListener('abort', abort);
      if (this.unreadReading === pending) {this.unreadReading = undefined; this.unreadController = undefined;}
    });
    this.unreadReading = pending; return pending;
  };
  readGuideAvailability = (signal?: AbortSignal, afterCurrent = false): Promise<boolean> => {
    if (signal?.aborted) return Promise.reject(cancelled());
    if (this.guideReading) {
      const previous = this.guideReading;
      if (afterCurrent || this.guideController?.signal.aborted) return previous.then(() => this.readGuideAvailability(signal), () => this.readGuideAvailability(signal));
      return previous;
    }
    const controller = new AbortController(), abort = () => controller.abort();
    this.guideController = controller;
    signal?.addEventListener('abort', abort, {once: true});
    const pending = this.readGuide().then(guide => {
      this.assertCurrent(); if (controller.signal.aborted) throw cancelled(); return !!guide?.sources.length;
    }).finally(() => {
      controller.abort(); signal?.removeEventListener('abort', abort);
      if (this.guideReading === pending) {this.guideReading = undefined; this.guideController = undefined;}
    });
    this.guideReading = pending; return pending;
  };
  /** Preference operations belong to the authority, not a refresh batch. Closing
   * that batch must not cancel a concurrent Settings CAS on the same reader. */
  readPreferences = async (deviceClass: DeviceClass, signal?: AbortSignal): Promise<PreferenceSnapshot> => {
    this.assertCurrent();
    if (signal?.aborted) throw cancelled();
    if (deviceClass !== this.options.deviceClass) throw invalid();
    const next = signal ? await this.readBatchPreferences(signal) : await this.preferences.preferences(deviceClass);
    this.assertCurrent();
    if (signal?.aborted) throw cancelled();
    if (next.deviceClass !== deviceClass) throw invalid();
    return next;
  };
  applyPreferences = async (scope: PreferenceScope, deviceClass: DeviceClass, revision: number, values: PreferencePatch): Promise<PreferenceSnapshot> => {
    this.assertCurrent();
    if (deviceClass !== this.options.deviceClass) throw invalid();
    const next = await this.preferences.applyPreferences(scope, deviceClass, revision, values);
    this.assertCurrent();
    if (next.deviceClass !== deviceClass) throw invalid();
    return next;
  };
  private readBatchPreferences(signal: AbortSignal): Promise<PreferenceSnapshot> {
    // PreferencesReader invokes its API synchronously before its first await.
    // Capture only this GET's batch in call(); later Settings requests are independent.
    this.preferenceRequestBatch = signal;
    try {return this.preferences.preferences(this.options.deviceClass);}
    finally {this.preferenceRequestBatch = undefined;}
  }
  read = (signal?: AbortSignal): Promise<Facts> => {
    if (this.reading) return this.reading;
    const controller = new AbortController(), abort = () => controller.abort();
    this.readingController = controller;
    signal?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted) abort();
    const pending = (async () => {
      this.assertCurrent();
      const [preferences, unread, hasLiveTV] = await Promise.all([this.readBatchPreferences(controller.signal), this.readUnread(controller.signal), this.readGuideAvailability(controller.signal)]);
      this.assertCurrent();
      if (controller.signal.aborted) throw cancelled();
      if (preferences.deviceClass !== this.options.deviceClass) throw invalid();
      return this.snapshot = Object.freeze({preferences, unread, hasLiveTV});
    })().finally(() => {
      controller.abort(); signal?.removeEventListener('abort', abort);
      if (this.reading === pending) {this.reading = undefined; this.readingController = undefined;}
    });
    this.reading = pending; return pending;
  };
  getSnapshot = (): Facts | undefined => this.isCurrent() ? this.snapshot : undefined;
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.options.signal.removeEventListener('abort', this.dispose);
    this.readingController?.abort();
    this.unreadController?.abort(); this.guideController?.abort();
    for (const controller of this.controllers) controller.abort();
    this.controllers.clear(); this.preferences.dispose(); this.inbox.dispose(); this.home.dispose();
    this.snapshot = undefined; this.inboxFailure = undefined; this.fences.clear(); this.homeShellFence = undefined;
  };
}
