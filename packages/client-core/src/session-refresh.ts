import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
/**
 * Keeps a device-bound server sign-in alive (lane C, 939a746: 15-minute access tokens with a
 * rotating refresh credential, `POST /v1/auth/refresh`).
 *
 * - Proactive: refreshes `leadMs` before the access token expires, and again whenever a request
 *   finds it about to expire (timers sleep with the app; `wake()` on resume too).
 * - Reactive: a 401 on the current token triggers one refresh; the request is retried once.
 * - Single flight: one refresh at a time in this runtime; concurrent callers wait for it. Across
 *   tabs, `refreshStoredSession` runs under the credential lock and adopts another tab's rotation.
 * - Atomic: the next access and refresh credential are stored in one change of the protected
 *   connection store (web: encrypted IndexedDB; Apple: Keychain), with the request id persisted
 *   before sending so an interrupted refresh replays instead of burning the credential.
 * - Patient: after a failed renewal nothing renews again before its retry is due, however many
 *   requests find the token expired in the meantime, and never before the wait the server asked
 *   for (`Retry-After`). Retries are spread with a little randomness so devices told the same
 *   thing do not all come back at once. Coming back to the foreground may retry early, but still
 *   not inside the server's wait.
 * - Refusal (reuse, revocation, device unapproved, expired sign-in) ends the sign-in: the store
 *   is cleared and `onSignedOut` runs. Transient failures keep the credential and retry: that
 *   includes a store that cannot be read right now. A stored sign-in the store reports as damaged
 *   for good ends the sign-in too, without touching the store.
 */
import {parseLocalSession} from "./local-session.ts";
import type {AuthRecovery, HttpLocalApi, LocalSession} from './index.ts';
import {REFRESH_MIN_VALID_MS, SessionRefreshError, refreshStoredSession, type ConnectionEnvironment, type RefreshStoredOptions, type RememberedServer} from './server-connections.ts';

export type RefresherSnapshot = Readonly<{
  phase: 'active' | 'refreshing' | 'retrying' | 'ended' | 'stopped';
  expiresAt: string;
  failures: number;
  error?: SessionRefreshError;
}>;

export type LocalSessionRefresherOptions = Readonly<{
  env: ConnectionEnvironment;
  api: Pick<HttpLocalApi, 'request' | 'setAccessToken' | 'setAuthRecovery'> & Partial<Pick<HttpLocalApi, 'setUserMutationSession'>>;
  /** The selected session (the viewer's copy is enough: the store supplies the refresh credential). */
  session: LocalSession;
  /** A renewed access token is in use: update the viewer snapshot (`ViewerService.rotate`) and the viewer record. */
  onRotated?: (session: LocalSession) => void;
  /** The sign-in is over on this device: the server refused the credential and the stored sign-in
   * was removed, or the stored sign-in is replaced, gone or damaged (`saved_sign_in_damaged`, left
   * in place for a new sign-in to replace). Show signed out; don't clear storage again. */
  onSignedOut?: (error: SessionRefreshError) => void;
  /** Refresh this long before expiry (default 2 minutes). */
  leadMs?: number;
  /** Backoff after transient failures. */
  retryMs?: readonly number[];
  /** Spreads retries: each waits up to a quarter longer, at random. Tests pass a fixed value. */
  random?: () => number;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (timer: unknown) => void;
  /** Test seam; defaults to `refreshStoredSession`. */
  refresh?: (options: RefreshStoredOptions) => Promise<LocalSession>;
}>;

export type PermissionReconciliation =
  | Readonly<{kind:'confirmed-unchanged'}>
  | Readonly<{kind:'adopted';role:string}>
  | Readonly<{kind:'deferred';nextAllowedAt:number;reason:'credential-wait'|'server-floor'}>
  | Readonly<{kind:'superseded';reason:'owner-stopped'|'session-moved'|'selection-moved'|'interest-cancelled'}>
  | Readonly<{kind:'failed';error:unknown;retryable:boolean}>
  | Readonly<{kind:'ended'}>;
type PermissionReason = Extract<PermissionReconciliation,{kind:'superseded'}>['reason'];
type PermissionBorrower = {finish(result:PermissionReconciliation):void};
type PermissionPrincipal = Pick<LocalSession,'viewer'|'sessionFamilyId'|'accessToken'|'deviceId'|'installationId'>;
type PermissionFlight = {session:LocalSession;proof:PermissionPrincipal;controller:AbortController;borrowers:Set<PermissionBorrower>;handover:boolean};
type PermissionWitness = Readonly<{selectionId:string|undefined;pinServer:string;session:PermissionPrincipal}>;
class PermissionRetired extends Error {}
const permissionFailure=(error:unknown,retryable=true):PermissionReconciliation=>({kind:'failed',error,retryable});
const permissionPositive=(value:unknown)=>typeof value==='number'&&value>0?Math.min(value,Number.MAX_VALUE):0;
function permissionDeadline(error:unknown,now:number):number {
  const e=error as {retryAfterMs?:unknown;retryAfterSeconds?:unknown}|null;
  const wait=Math.max(permissionPositive(e?.retryAfterMs),permissionPositive(typeof e?.retryAfterSeconds==='number'?e.retryAfterSeconds*1000:0));
  return wait?Math.min(Number.MAX_VALUE,now+wait):0;
}
function permissionSessionEqual(a:PermissionPrincipal,b:PermissionPrincipal):boolean {
  return a.sessionFamilyId===b.sessionFamilyId&&a.accessToken===b.accessToken&&a.deviceId===b.deviceId&&a.installationId===b.installationId&&
    (['serverId','authority','accountId','profileId','role'] as const).every(key=>a.viewer[key]===b.viewer[key]);
}
function permissionRecord(saved:RememberedServer|undefined):LocalSession|undefined {
  if(!saved)return;
  if(saved.version!=='1'||!saved.pin||typeof saved.pin.serverId!=='string'||!saved.pin.serverId||
    saved.selectionId!==undefined&&(typeof saved.selectionId!=='string'||!saved.selectionId||saved.selectionId.length>256))throw new Error('Unreadable protected permission selection.');
  return parseLocalSession(saved.session,{}, {stored:true});
}
function permissionMatches(saved:RememberedServer|undefined,witness:PermissionWitness):boolean {
  const session=permissionRecord(saved);
  return !!saved&&!!session&&saved.selectionId===witness.selectionId&&saved.pin.serverId===witness.pinServer&&permissionSessionEqual(session,witness.session);
}

export class LocalSessionRefresher implements AuthRecovery {
  private o: LocalSessionRefresherOptions;
  private session: LocalSession;
  private flight?: Promise<LocalSession>;
  /** A successful proactive rotation that did not advance this family's horizon. */
  private noProgress?: Readonly<{serverId: string; familyId: string; expiresAt: string; horizon: string}>;
  private timer?: unknown;
 private permissionTimer?:unknown;
 private permissionRead?:PermissionFlight;
 private permissionFullWaitUntil=0;
 private permissionCredentialWaitUntil=0;
 private permissionWaitUntil=0;
  private failures = 0;
  /** No renewal before this, from the app's own backoff (a return to the foreground may go sooner). */
  private retryAt = 0;
  /** No renewal before this at all: the server said how long to wait. */
  private serverWaitUntil = 0;
  private state: RefresherSnapshot;
  private listeners = new Set<() => void>();
  private now: () => number;
  private setTimer: (fn: () => void, ms: number) => unknown;
  private clearTimer: (timer: unknown) => void;

  constructor(options: LocalSessionRefresherOptions) {
    this.o = options;
    this.session = options.session;
    options.api.setUserMutationSession?.(options.session);
    this.now = options.now ?? Date.now;
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (t => clearTimeout(t as ReturnType<typeof setTimeout>));
    this.state = Object.freeze({phase: 'active', expiresAt: this.session.expiresAt, failures: 0});
    options.api.setAuthRecovery(this);
    this.schedule();
    this.schedulePermissions();
  }

  getSnapshot = (): RefresherSnapshot => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };
  private publish(p: Partial<RefresherSnapshot>) { this.state = Object.freeze({...this.state, ...p}); for (const l of [...this.listeners]) l(); }

  /** The session in use (without its refresh credential, which only the store holds). */
  current(): LocalSession { return this.session; }
  private get done() { return this.state.phase === 'ended' || this.state.phase === 'stopped'; }
  private get lead() { return this.o.leadMs ?? 120_000; }
  private expiresIn() { return Date.parse(this.session.expiresAt) - this.now(); }
  private proactiveSuppressed(): boolean {
    const marker = this.noProgress, session = this.session;
    return !!marker && this.expiresIn() > 0 && marker.serverId === session.viewer.serverId
      && marker.familyId === session.sessionFamilyId && marker.expiresAt === session.expiresAt
      && marker.horizon === session.authorizationHorizon;
  }

  // ── AuthRecovery (HttpLocalApi calls these) ─────────────────────
  token = async (current: string): Promise<string> => {
    if (this.done) return current;
    if (current !== this.session.accessToken) return this.session.accessToken;
    if (this.expiresIn() > REFRESH_MIN_VALID_MS) return current;
    if (this.waiting() || this.proactiveSuppressed()) return current;
    try { return (await this.run()).accessToken; } catch { return this.session.accessToken; }
  };

  recover = async (rejected: string): Promise<string | undefined> => {
    if (this.done) return undefined;
    if (rejected !== this.session.accessToken) return this.session.accessToken;
    // A renewal just failed and its retry is not due: this request does not start another.
    if (this.waiting()) return undefined;
    try { return (await this.run(rejected)).accessToken; } catch { return undefined; }
  };

  /** A renewal failed and the next try is not due yet (a renewal already in flight is joined, not blocked). */
  private waiting(): boolean {
    return !this.flight && this.now() < Math.max(this.retryAt, this.serverWaitUntil);
  }

  /** Check now (app returned to the foreground, network came back). */
  wake(): void {
    if (this.done) return;
    // Things changed (foreground, network): the app's own backoff may be cut short, the server's wait may not.
    if (this.now() < this.serverWaitUntil) return;
    if (this.expiresIn() <= this.lead && !this.proactiveSuppressed()) void this.run().catch(() => {});
    else this.schedule();
  }


  /** Compatibility: only a committed changed-role handover permits the original 403 retry. */
  permissionDenied=():Promise<boolean>=>this.permissionRequest(undefined,false).then(result=>result.kind==='adopted');

  reconcilePermissions=(interest?:{signal?:AbortSignal}):Promise<PermissionReconciliation>=>this.permissionRequest(interest?.signal,true);

  private permissionRequest(signal:AbortSignal|undefined,detailed:boolean):Promise<PermissionReconciliation> {
    if(signal?.aborted)return Promise.resolve({kind:'superseded',reason:'interest-cancelled'});
    if(this.done)return Promise.resolve(this.state.phase==='ended'?{kind:'ended'}:{kind:'superseded',reason:'owner-stopped'});
    const now=this.now(),credentialWait=this.waiting()?Math.max(this.retryAt,this.serverWaitUntil):0;
    const fullFloor=detailed?Math.max(this.permissionFullWaitUntil,this.permissionCredentialWaitUntil):0;
    // The new admission preserves a full floor even when an old poll observed it
    // first. Existing wrapper/poll admission deliberately keeps its legacy cap.
    if(detailed&&now<Math.max(fullFloor,credentialWait,this.permissionWaitUntil))return Promise.resolve({kind:'deferred',
      nextAllowedAt:Math.max(fullFloor,credentialWait,this.permissionWaitUntil),reason:credentialWait>now?'credential-wait':'server-floor'});
    const previous=this.permissionRead;
    if(previous){
      if(previous.session!==this.session)return Promise.resolve({kind:'superseded',reason:'session-moved'});
      if(previous.controller.signal.aborted)return Promise.resolve({kind:'superseded',reason:'interest-cancelled'});
      return this.permissionBorrow(previous,signal);
    }
    const floor=Math.max(credentialWait,this.permissionWaitUntil);
    if(now<floor)return Promise.resolve({kind:'deferred',nextAllowedAt:floor,reason:credentialWait>now?'credential-wait':'server-floor'});
    const original=this.session;
    const proof:PermissionPrincipal=Object.freeze({viewer:Object.freeze({...original.viewer}),sessionFamilyId:original.sessionFamilyId,
      accessToken:original.accessToken,deviceId:original.deviceId,installationId:original.installationId});
    const flight:PermissionFlight={session:original,proof,controller:new AbortController(),borrowers:new Set(),handover:false};
    this.permissionRead=flight;
    const answer=this.permissionBorrow(flight,signal);
    const deliver=(result:PermissionReconciliation)=>{
      if(this.permissionRead===flight)this.permissionRead=undefined;
      for(const borrower of flight.borrowers)borrower.finish(result);
    };
    void this.performPermission(flight).then(deliver,error=>deliver(permissionFailure(error)));
    return answer;
  }
  private permissionBorrow(flight:PermissionFlight,signal?:AbortSignal):Promise<PermissionReconciliation> {
    return new Promise(resolve=>{
      const stop=()=>borrower.finish({kind:'superseded',reason:'interest-cancelled'});
      const borrower:PermissionBorrower={finish:result=>{
        if(!flight.borrowers.delete(borrower))return;
        signal?.removeEventListener('abort',stop);
        if(!flight.borrowers.size)flight.controller.abort();
        resolve(result);
      }};
      flight.borrowers.add(borrower);signal?.addEventListener('abort',stop,{once:true});
      if(signal?.aborted)stop();
    });
  }
  private permissionReason(flight:PermissionFlight):PermissionReason|undefined {
    if(this.done)return 'owner-stopped';
    if(this.session!==flight.session||!permissionSessionEqual(this.session,flight.proof))return 'session-moved';
    if(flight.controller.signal.aborted||!flight.borrowers.size)return 'interest-cancelled';
    return;
  }
  private permissionSuperseded(reason:PermissionReason):PermissionReconciliation {
    return this.state.phase==='ended'?{kind:'ended'}:{kind:'superseded',reason};
  }
  private async performPermission(flight:PermissionFlight):Promise<PermissionReconciliation> {
    const current=flight.session;
    let retired:PermissionReason|undefined;
    try{
      let reason=this.permissionReason(flight);if(reason)return this.permissionSuperseded(reason);
      const saved=await this.o.env.storage.read();
      reason=this.permissionReason(flight);if(reason)return this.permissionSuperseded(reason);
      const stored=permissionRecord(saved);
      if(!saved||!stored||saved.pin.serverId!==current.viewer.serverId||!permissionSessionEqual(stored,flight.proof))return {kind:'superseded',reason:'selection-moved'};
      const witness:PermissionWitness={selectionId:saved.selectionId,pinServer:saved.pin.serverId,session:flight.proof};
      let raw:RouteResponse<'get_me'>;
      try{raw=await this.o.api.request<RouteResponse<'get_me'>>(routePath('get_me'),'GET',undefined,flight.controller.signal);}
      catch(error){
        this.permissionFullWaitUntil=Math.max(this.permissionFullWaitUntil,permissionDeadline(error,this.now()));
        const wait=(error as {retryAfterSeconds?:unknown}|null)?.retryAfterSeconds;
        if(typeof wait==='number'&&Number.isFinite(wait)&&wait>0)this.permissionWaitUntil=this.now()+Math.min(wait,3600)*1000;
        reason=this.permissionReason(flight);if(reason)return this.permissionSuperseded(reason);
        const e=error as {status?:number;retryable?:boolean}|null;
        return permissionFailure(error,e?.retryable??!(typeof e?.status==='number'&&e.status>=400&&e.status<500&&![408,425,429].includes(e.status)));
      }
      reason=this.permissionReason(flight);if(reason)return this.permissionSuperseded(reason);
      if(!raw||typeof raw!=='object'||!('viewer' in raw))throw new Error('Unreadable permission response.');
      const next=parseLocalSession({...current,viewer:raw.viewer},{accountId:current.viewer.accountId,profileId:current.viewer.profileId,
        serverId:current.viewer.serverId,authority:current.viewer.authority},{stored:true});
      if(!['owner','admin','member'].includes(next.viewer.role))return permissionFailure(Object.assign(new Error('Unsupported viewer role.'),{code:'unsupported_role'}),false);
      if(next.viewer.role===current.viewer.role){
        const final=await this.o.env.storage.read();
        reason=this.permissionReason(flight);if(reason)return this.permissionSuperseded(reason);
        return permissionMatches(final,witness)?{kind:'confirmed-unchanged'}:{kind:'superseded',reason:'selection-moved'};
      }
      let matched=false;
      await this.o.env.storage.change(record=>{
        if(!permissionMatches(record,witness))return record;
        matched=true;return {...record!,session:{...record!.session,viewer:next.viewer}};
      },()=>{
        const reason=this.permissionReason(flight);
        if(reason){retired=reason;throw new PermissionRetired();}
      });
      if(!matched)return {kind:'superseded',reason:'selection-moved'};
      reason=this.permissionReason(flight);if(reason)return this.permissionSuperseded(reason);
      flight.handover=true;this.session=next;this.o.onRotated?.(next);
      // Handover may synchronously stop this owner. No subsequent publication/timer.
      return {kind:'adopted',role:next.viewer.role};
    }catch(error){
      // Browser storage may wrap a guard exception. Only this attempt's private
      // guard marker proves owned retirement; arbitrary storage faults stay failed.
      return retired?this.permissionSuperseded(retired):permissionFailure(error);
    }
  }
  private stopPermission():void {
    const flight=this.permissionRead;if(!flight)return;
    flight.controller.abort();
    if(!flight.handover)for(const borrower of flight.borrowers)borrower.finish(this.permissionSuperseded('owner-stopped'));
  }
  private schedulePermissions(){
    if(this.permissionTimer!==undefined)this.clearTimer(this.permissionTimer);
    this.permissionTimer=undefined;
    if(this.done||this.failures>0)return;
    this.permissionTimer=this.setTimer(()=>{this.permissionTimer=undefined;void this.permissionDenied().finally(()=>this.schedulePermissions());},Math.max(30_000,this.permissionWaitUntil-this.now()));
  }

  /** Stop keeping this sign-in alive (sign-out, switching viewer). Does not touch storage. */
  stop(): void {
    if (this.timer !== undefined) this.clearTimer(this.timer);
    if(this.permissionTimer!==undefined)this.clearTimer(this.permissionTimer);this.permissionTimer=undefined;
    this.timer = undefined;
    if (!this.done) this.publish({phase: 'stopped'});
    this.stopPermission();
    this.o.api.setAuthRecovery(undefined);
  }

  /** One refresh at a time; concurrent callers share it. */
  private run(rejected?: string): Promise<LocalSession> {
    if (this.flight) return this.flight;
    if (this.done) return Promise.reject(this.state.error ?? new SessionRefreshError('stopped', 'Sign-in renewal stopped.', false));
    this.publish({phase: 'refreshing'});
    const previous = this.session;
    const refresh = this.o.refresh ?? refreshStoredSession;
    const flight = refresh({
      env: this.o.env,
      api: this.o.api,
      family: {sessionFamilyId: this.session.sessionFamilyId, serverId: this.session.viewer.serverId},
      rejected,
      now: this.now,
      // A proactive run renews inside the lead window; the store answers without a request when another context already did.
      minValidMs: Math.max(REFRESH_MIN_VALID_MS, this.lead),
    }).then(next => { this.adopt(next, previous, rejected === undefined); return next; }, (e: unknown) => { this.failed(e); throw e; });
    this.flight = flight;
    void flight.finally(() => { if (this.flight === flight) this.flight = undefined; }).catch(() => {});
    return flight;
  }

  private adopt(next: LocalSession, previous: LocalSession, proactive: boolean) {
    if (this.done) return;
    this.failures = 0;
    this.retryAt = 0;
    this.serverWaitUntil = 0;
    const sameFamily = next.sessionFamilyId === previous.sessionFamilyId && next.viewer.serverId === previous.viewer.serverId;
    const later = Date.parse(next.expiresAt) > Date.parse(previous.expiresAt)
      || Date.parse(next.authorizationHorizon) > Date.parse(previous.authorizationHorizon);
    if (!sameFamily || later) this.noProgress = undefined;
    else if (this.noProgress || proactive && Date.parse(next.expiresAt) - this.now() <= this.lead
      && Date.parse(next.expiresAt) <= Date.parse(previous.expiresAt)) {
      this.noProgress = Object.freeze({serverId: next.viewer.serverId, familyId: next.sessionFamilyId,
        expiresAt: next.expiresAt, horizon: next.authorizationHorizon});
    }
    const {refreshToken: _secret, ...visible} = next;
    const changed = visible.accessToken !== this.session.accessToken;
    this.session = Object.freeze(visible);
    if (changed) { this.o.api.setUserMutationSession ? this.o.api.setUserMutationSession(visible) : this.o.api.setAccessToken(visible.accessToken); this.o.onRotated?.(this.session); }
    // Adoption may synchronously retire this authority and stop the refresher.
    // Keep that terminal state before publishing or installing another timer.
    if (this.done) return;
    this.schedulePermissions();
    this.publish({phase: 'active', expiresAt: visible.expiresAt, failures: 0, error: undefined});
    this.schedule();
  }

  private failed(e: unknown) {
    if (this.done) return;
    const error = e instanceof SessionRefreshError ? e : new SessionRefreshError('refresh_unavailable_now', 'The server couldn’t renew your sign-in right now.', false);
    // Nothing more to try: refused, or no refresh credential and the access token is spent.
    if (error.signOut || (error.code === 'refresh_unavailable' && this.expiresIn() <= 0)) {
      if (this.timer !== undefined) this.clearTimer(this.timer);
      this.timer = undefined;
      if(this.permissionTimer!==undefined)this.clearTimer(this.permissionTimer);this.permissionTimer=undefined;
      this.publish({phase: 'ended', error});
      this.stopPermission();
      this.o.api.setAuthRecovery(undefined);
      this.o.onSignedOut?.(error);
      return;
    }
    this.failures++;
    this.permissionCredentialWaitUntil=Math.max(this.permissionCredentialWaitUntil,permissionDeadline(e,this.now()));
    this.schedulePermissions();
    const asked = (e as {retryAfterSeconds?: unknown} | null)?.retryAfterSeconds;
    // Bounded: a server cannot park renewal for longer than an hour with one header.
    if (typeof asked === 'number' && Number.isFinite(asked) && asked > 0) this.serverWaitUntil = this.now() + Math.min(asked, 3600) * 1000;
    this.publish({phase: 'retrying', failures: this.failures, error});
    this.schedule();
  }

  private schedule() {
    if (this.timer !== undefined) this.clearTimer(this.timer);
    this.timer = undefined;
    if (this.done) return;
    let delay: number;
    if (this.failures > 0) {
      const backoff = this.o.retryMs ?? [5_000, 15_000, 30_000, 60_000, 120_000];
      delay = backoff[Math.min(this.failures - 1, backoff.length - 1)] ?? 120_000;
      // An old server without the endpoint never expires tokens this way; check rarely.
      if (this.state.error?.code === 'refresh_unsupported') delay = Math.max(delay, 15 * 60_000);
      if (this.state.error?.code === 'refresh_unavailable') { const left = this.expiresIn(); if (left <= 0) return; delay = left + 1_000; }
      else {
        // Never before the server's wait, then spread: up to a quarter longer, at random.
        delay = Math.max(delay, this.serverWaitUntil - this.now());
        delay += Math.round(delay * 0.25 * Math.min(1, Math.max(0, (this.o.random ?? Math.random)())));
      }
      this.retryAt = this.now() + delay;
    } else delay = Math.max(0, this.expiresIn() - (this.proactiveSuppressed() ? 0 : this.lead));
    this.timer = this.setTimer(() => { this.timer = undefined; void this.run().catch(() => {}); }, Math.min(delay, 2_147_000_000));
  }
}
