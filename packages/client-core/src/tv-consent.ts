import {routeBody} from '../../contracts/src/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {routePath as hostedPath, routeBody as hostedBody} from '../../contracts/src/hosted/routes.ts';
import {currentUTCTimestamp} from './local-session.ts';
import type {ProfileAPI} from './profile-management.ts';
import {normalizeSetupCode, type SetupPreview} from './setup-code.ts';
import {RouteError, routeOrigin} from './route-identity.ts';
import {hostedGate} from './hosted-gate.ts';

export type TVConsentAuthority = Readonly<{
  kind: 'portico' | 'local'; origin: string; accountId: string; sessionFamilyId: string;
  /** Required for local Quick Connect; absent for the Portico Account's own consent. */
  serverId?: string;
}>;
export type TVConsentTransport = ProfileAPI & Readonly<{baseUrl: string}>;
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && !/[\p{Cc}\p{Cf}]/u.test(v);
function checked(authority: TVConsentAuthority): TVConsentAuthority {
  if (!authority || !['portico', 'local'].includes(authority.kind) || !text(authority.accountId, 256) || !text(authority.sessionFamilyId, 128) ||
      (authority.kind === 'local' ? !text(authority.serverId, 256) : authority.serverId !== undefined)) throw new RouteError('invalid_consent_authority', 'Select the account that will sign in this TV.');
  const origin = routeOrigin(authority.origin), url = new URL(origin);
  if (origin !== authority.origin || authority.kind === 'portico' && url.protocol !== 'https:' && !['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)) throw new RouteError('invalid_consent_authority', 'Portico Account sign-in needs a secure connection.');
  return Object.freeze({...authority});
}
const same = (a: TVConsentAuthority | undefined, b: TVConsentAuthority) => !!a && (['kind', 'origin', 'accountId', 'sessionFamilyId', 'serverId'] as const).every(key => a[key] === b[key]);
/** Sign-in consent only. Any already signed-in device of this account may consent. No owner,
 * device trust or main-profile permission is required. The issuing service enforces the account.
 * TV create/poll/cancel receipts remain owned by SetupCodeService, including late-grant cleanup. */
export class TVConsentApprover {
  private readonly authority: TVConsentAuthority;
  private generation = 0;
  private controller?: AbortController;
  private preview?: Readonly<SetupPreview>;
  private disposed = false;
  private readonly api: TVConsentTransport; private readonly current: () => TVConsentAuthority | undefined; private readonly timeoutMs: number;
  constructor(api: TVConsentTransport, authority: TVConsentAuthority,
      current: () => TVConsentAuthority | undefined, timeoutMs = 15000) {
    this.api = api; this.current = current; this.timeoutMs = timeoutMs;
    this.authority = checked(authority);
    if (routeOrigin(api.baseUrl) !== this.authority.origin || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 120000) throw new RouteError('invalid_consent_authority', 'Consent belongs to the selected account service.');
  }
  cancel() { ++this.generation; this.controller?.abort(); this.controller = undefined; this.preview = undefined; }
  dispose() { this.cancel(); this.disposed = true; }
  private active() {
    if (this.disposed || !same(this.current(), this.authority)) throw new RouteError('consent_retired', 'The signed-in account changed. Enter the TV code again.');
  }
  private async run<T>(work: (signal: AbortSignal) => Promise<T>): Promise<T> {
    this.active(); const generation = this.generation, controller = this.controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const invoke = () => { this.active(); if (controller.signal.aborted) throw new RouteError('consent_cancelled', 'TV sign-in was cancelled.'); return work(controller.signal); };
      const pending = this.authority.kind === 'portico' ? hostedGate(this.authority.origin).run('interactive', invoke) : invoke();
      const value = await Promise.race([pending, new Promise<never>((_, reject) => {
        controller.signal.addEventListener('abort', () => reject(new RouteError('consent_cancelled', 'TV sign-in was cancelled.')), {once: true});
        timer = setTimeout(() => { reject(new RouteError('consent_timeout', 'The TV sign-in request did not answer. Review its code again.')); controller.abort(); }, this.timeoutMs);
      })]);
      this.active(); if (controller.signal.aborted || generation !== this.generation) throw new RouteError('consent_cancelled', 'TV sign-in was cancelled.');
      return value;
    } finally { clearTimeout(timer); if (this.controller === controller) this.controller = undefined; }
  }
  async review(code: string): Promise<Readonly<SetupPreview>> {
    this.cancel(); const userCode = normalizeSetupCode(code);
    const raw = await this.run(signal => this.authority.kind === 'portico'
      ? this.api.request<SetupPreview>(hostedPath('device_authorization_review'), 'POST', hostedBody('device_authorization_review', {userCode}), signal)
      : this.api.request<SetupPreview>(routePath('post_quick_connect_review'), 'POST', routeBody('post_quick_connect_review', {userCode}), signal));
    if (!raw || !text(raw.requestId, 128) || normalizeSetupCode(raw.userCode) !== userCode || !text(raw.deviceName, 100) || !text(raw.platform, 32) || !text(raw.appVersion, 40) || !currentUTCTimestamp(raw.expiresAt) || Date.parse(raw.expiresAt) <= Date.now() || Date.parse(raw.expiresAt) > Date.now() + 605000) throw new RouteError('invalid_consent', 'This TV sign-in request is no longer available.');
    const preview = Object.freeze({...raw}); this.preview = preview; return preview;
  }
  async decide(preview: Readonly<SetupPreview>, decision: 'approve' | 'deny'): Promise<void> {
    this.active();
    if (preview !== this.preview || !['approve', 'deny'].includes(decision) || Date.parse(preview.expiresAt) <= Date.now()) throw new RouteError('consent_retired', 'Review the current TV code before deciding.');
    // One decision per reviewed request; uncertain transport outcomes require a fresh review.
    this.preview = undefined;
    await this.run(signal => this.authority.kind === 'portico'
      ? this.api.request<void>(hostedPath('device_authorization_decide'), 'POST', hostedBody('device_authorization_decide', {requestId: preview.requestId, userCode: preview.userCode, decision}), signal)
      : this.api.request<void>(routePath('post_quick_connect_decision'), 'POST', routeBody('post_quick_connect_decision', {requestId: preview.requestId, userCode: preview.userCode, decision}), signal));
  }
}
