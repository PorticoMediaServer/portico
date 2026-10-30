import {routePath as hostedRoutePath} from "../../contracts/src/hosted/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {randomId} from './random-id.ts';
import {ApiError, HttpLocalApi} from './index.ts';
import type {DeviceAuthorization} from './device-authorization.ts';
import {routeOrigin} from './route-identity.ts';
import {hostedGate, type HostedGate, type HostedRequestKind} from './hosted-gate.ts';

export const setupCodeAlphabet = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789';
export function normalizeSetupCode(value: string): string {
  const raw = value.toUpperCase().replace(/[\s-]/gu, '');
  if (!new RegExp(`^[${setupCodeAlphabet}]{8}$`).test(raw))
    throw new Error('Enter the eight-character code displayed on the device.');
  return raw.slice(0, 4) + '-' + raw.slice(4);
}
export type SetupCodeEntry<T> = {
  requestId: string;
  authorization: DeviceAuthorization;
  grant?: T;
  nextPollAt: number;
};
export type SetupCodeJournal<T> = {
  version: 1;
  authority: string;
  revision: number;
  current?: SetupCodeEntry<T>;
  creating?: {requestId: string; startedAt: number; cancelled?: boolean};
  retired: SetupCodeEntry<T>[];
};
/** Implementations MUST use protected, atomic credential storage, not drafts or
 * localStorage. A failed write must leave the old record intact. */
export interface SetupCodeStorage<T> {
  change(update: (record: SetupCodeJournal<T> | undefined) => SetupCodeJournal<T>): Promise<SetupCodeJournal<T>>;
}
export interface SetupCodeApi<T> {
  readonly gate?: HostedGate;
  create(requestId: string, signal: AbortSignal, kind?: HostedRequestKind): Promise<DeviceAuthorization>;
  poll(entry: SetupCodeEntry<T>, signal: AbortSignal, checkpoint: (grant:T)=>Promise<void>): Promise<T>;
  cancel(entry: SetupCodeEntry<T>, signal: AbortSignal, kind?: HostedRequestKind): Promise<void>;
  forgotten?(entry: SetupCodeEntry<T>): void;
  displayed?(entry: SetupCodeEntry<T> | undefined): void;
}
export type SetupCodeSnapshot<T> = {
  phase: 'idle' | 'creating' | 'waiting' | 'approved' | 'denied' | 'error';
  userCode?: string;
  verificationUri?: string;
  verificationUriComplete?: string;
  expiresAt?: string;
  grant?: T;
  message?: string;
};
function validAuthorization(value: DeviceAuthorization, now: number) {
  if (!value || normalizeSetupCode(value.userCode) !== value.userCode ||
      typeof value.deviceCode !== 'string' || value.deviceCode.length < 43 || value.deviceCode.length > 128 ||
      !Number.isSafeInteger(value.interval) || value.interval < 5 || value.interval > 300 ||
      !Number.isFinite(Date.parse(value.expiresAt)) || Date.parse(value.expiresAt) > now + 605_000)
    throw new Error('The activation response was invalid.');
  const uri = new URL(value.verificationUri);
  routeOrigin(uri.origin);
  if (uri.pathname !== '/device' || uri.username || uri.password || uri.search || uri.hash ||
      value.verificationUriComplete !== value.verificationUri + '#code=' + value.userCode)
    throw new Error('The activation address was invalid.');
}
/** One durable owner of an activation operation. It resumes create/redeem after
 * crashes, keeps the last valid QR/code through replacement failures, and never
 * exposes a poll secret to presentation. Cancellation receipts are quarantined
 * until the authority confirms revocation, independently of a new visible code. */
export class SetupCodeService<T> {
  private journal?: SetupCodeJournal<T>;
  private state: SetupCodeSnapshot<T> = {phase: 'idle'};
  private listeners = new Set<() => void>();
  private running = false;
  private generation = 0;
  private timer?: ReturnType<typeof setTimeout>;
  private operation?: Promise<void>;
  private controllers = new Set<AbortController>();
  private createAfter = 0;
  private failures = 0;
  private pollFailures = 0;
  private cleanupAfter = 0;
  private mutations:Promise<unknown> = Promise.resolve();
  private approvedRequestId?:string;
  private explicitStart = false;
  private readonly authority:string;
  private readonly api:SetupCodeApi<T>;
  private readonly gate?:HostedGate;
  private readonly storage:SetupCodeStorage<T>;
  private readonly requestId:()=>Promise<string>;
  private readonly options:{requestTimeoutMs:number;tickMs:number;replaceBeforeMs:number;random?:()=>number};
  constructor(authority:string,api:SetupCodeApi<T>,storage:SetupCodeStorage<T>,requestId:()=>Promise<string>=async()=>randomId(),options:{requestTimeoutMs:number;tickMs:number;replaceBeforeMs:number;random?:()=>number}={requestTimeoutMs:15000,tickMs:1000,replaceBeforeMs:60000}) {
    this.authority=authority;this.api=api;this.storage=storage;this.requestId=requestId;this.options=options;
    // The native TV journal already binds this protocol to its Hosted origin.
    // Local Quick Connect uses local:<server ID> and never gets an account gate.
    const tvSuffix = '#tv-x25519-v1';
    this.gate = api.gate ?? (authority.endsWith(tvSuffix) ? hostedGate(routeOrigin(authority.slice(0, -tvSuffix.length))) : undefined);
  }
  /** The wait before retry number `failures` of a request that got no answer: about 5 s, doubling
   * to a 30 s cap, and spread by up to a fifth at every step so devices that lost the service in
   * the same moment do not come back in the same one. Below the cap the spread is added (never
   * sooner than the step itself); at the cap it is taken off, so the wait stays within 30 s. */
  private backoff(failures: number) {
    const step = 5_000 * 2 ** Math.min(3, failures - 1), spread = 0.2 * (this.options.random ?? Math.random)();
    return Math.round(step * (1 + 0.2) <= 30_000 ? step * (1 + spread) : 30_000 * (1 - spread));
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private emit(state: SetupCodeSnapshot<T>) {this.state = state; for (const listener of this.listeners) listener();}
  private change(update: (record: SetupCodeJournal<T>) => SetupCodeJournal<T>):Promise<SetupCodeJournal<T>> {
    // Serialize our own CAS operations (cleanup, adoption, poll checkpoint).
    // Another window still conflicts against the persisted revision.
    const operation=this.mutations.catch(()=>{}).then(()=>this.changeNow(update));
    this.mutations=operation;return operation;
  }
  private async changeNow(update: (record: SetupCodeJournal<T>) => SetupCodeJournal<T>) {
    const expected = this.journal?.revision;
    const result = await this.storage.change(raw => {
      const old = raw ?? {version: 1, authority: this.authority, revision: 0, retired: []};
      if (old.version !== 1 || old.authority !== this.authority || !Number.isSafeInteger(old.revision) ||
          !Array.isArray(old.retired) || old.retired.length > 8 ||
          expected !== undefined && old.revision !== expected)
        throw new Error('Activation changed in another window. Reopen this screen.');
      const next = update(old);
      if (next.retired.length > 8) throw new Error('Previous activations need cleanup before creating another code.');
      return {...next, revision: old.revision + 1};
    });
    this.journal = result;
    return result;
  }
  private show(message?: string) {
    const current = this.journal?.current;
    if (current?.grant !== undefined) {this.approvedRequestId=current.requestId;this.api.displayed?.(undefined); this.emit({phase: 'approved', grant: current.grant, message}); return;}
    this.approvedRequestId=undefined;
    if (current && Date.parse(current.authorization.expiresAt) > Date.now()) {
      const {userCode, verificationUri, verificationUriComplete, expiresAt} = current.authorization;
      this.api.displayed?.(current);
      this.emit({phase: 'waiting', userCode, verificationUri, verificationUriComplete, expiresAt});
    } else {this.api.displayed?.(undefined); this.emit({phase: 'creating', message});}
  }
  private async bounded<R>(work: (signal: AbortSignal) => Promise<R>): Promise<R> {
    const controller = new AbortController(); this.controllers.add(controller);
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {return await Promise.race([work(controller.signal), new Promise<never>((_, reject) => {
      timer = setTimeout(() => {controller.abort(); reject(new Error('Activation is offline. Retrying the saved request.'));}, this.options.requestTimeoutMs);
    })]);} finally {clearTimeout(timer); controller.abort(); this.controllers.delete(controller);}
  }
  async start() {
    if (this.running) return;
    this.running = true; // Resume the same durable operation; only cancellation changes its generation.
    try {await this.change(record => record); const current = this.journal?.current;
      if (current) validAuthorization(current.authorization, Date.now());
      this.explicitStart = true; this.show(); this.schedule(0);
    } catch (error) {this.running = false; this.emit({phase: 'error', message: error instanceof Error ? error.message : 'Activation could not be saved.'});}
  }
  /** Background/unmount is not abandonment. No remote cancellation or key loss. */
  pause() {this.running = false; clearTimeout(this.timer); this.api.displayed?.(undefined);}
  private schedule(delay = this.options.tickMs) {
    clearTimeout(this.timer);
    const wait = this.gate?.blockedFor() ?? 0;
    if (wait > 0 && !this.explicitStart) delay = Math.max(delay, wait + Math.floor((this.options.random ?? Math.random)() * 1000));
    const current = this.journal?.current;
    const expiry = current?.grant === undefined ? Date.parse(current?.authorization.expiresAt ?? '') : 0;
    // Expire the visible code locally even while network work is deferred.
    // Keep its protected receipt so an already consumed grant can be recovered.
    if (expiry > Date.now()) delay = Math.min(delay, expiry - Date.now());
    if (this.running) this.timer = setTimeout(() => {void this.step();}, delay);
  }
  private async step() {
    if (this.operation || !this.running) {this.schedule(); return;}
    const generation = this.generation;
    this.operation = this.cycle(generation).catch(error => {
      if (generation === this.generation) {
        this.failures++; this.createAfter = Math.max(this.createAfter, Date.now() + this.backoff(this.failures));
        this.show(error instanceof Error ? error.message : 'Activation is offline. Retrying the saved request.');
      }
    }).finally(() => {this.operation = undefined; this.schedule();});
    await this.operation;
  }
  private async cycle(generation: number) {
    const explicit = this.explicitStart; this.explicitStart = false;
    if ((this.gate?.blockedFor() ?? 0) > 0 && !explicit) {this.show(); return;}
    if(Date.now()>=this.cleanupAfter)await this.cleanup();
    if(generation!==this.generation||!this.running)return;
    if(this.journal?.creating?.cancelled){this.show('Finishing cancellation of the saved request.');return;}
    const current = this.journal?.current;
    if (current && current.grant === undefined && current.nextPollAt <= Date.now()) {
      // A consumed grant may still be recovered after code expiry. Only the
      // authority decides whether this is expired, pending, or exact replay.
      await this.change(record => ({...record, current: record.current && {...record.current, nextPollAt: Date.now() + record.current.authorization.interval * 1000}}));
      try {
        const grant = await this.bounded(signal => this.request('automatic', () => this.api.poll(current, signal, async grant => {
          if (generation !== this.generation || signal.aborted) throw new Error('Activation was cancelled or timed out.');
          await this.change(record => {
            if (generation !== this.generation || signal.aborted || record.current?.requestId !== current.requestId) throw new Error('Activation changed.');
            return {...record,current:{...record.current,grant}};
          });
        }), true));
        if (generation !== this.generation) return;
        this.pollFailures = 0;
        await this.change(record => {
          if (record.current?.requestId !== current.requestId) throw new Error('Activation changed.');
          return {...record, current: {...record.current, grant}};
        });
        this.show(); return;
      } catch (error) {
        if (generation !== this.generation) return;
        // Any of the authority's own answers ends a run of unanswered polls.
        if (error instanceof ApiError && ['access_denied', 'expired_token', 'invalid_grant', 'authorization_pending', 'slow_down'].includes(error.code)) this.pollFailures = 0;
        if (error instanceof ApiError && error.code === 'access_denied') {
          await this.retireCurrent(); this.pause(); this.emit({phase: 'denied', message: 'The request was denied or cancelled. Start a new code to try again.'}); return;
        }
        if (error instanceof ApiError && ['expired_token', 'invalid_grant'].includes(error.code)) {
          await this.retireCurrent(); this.createAfter = 0;
        } else if (error instanceof ApiError && ['authorization_pending', 'slow_down'].includes(error.code)) {
          const interval = error.code === 'slow_down' ? Math.max(current.authorization.interval, Math.min(30, current.authorization.interval + 5)) : current.authorization.interval;
          const wait = Math.max(interval * 1000, (error.retryAfterSeconds ?? 0) * 1000, this.gate?.blockedFor() ?? 0);
          await this.change(record => ({...record, current: record.current && {...record.current, authorization: {...record.current.authorization, interval}, nextPollAt: Date.now() + wait}}));
        } else {
          // No answer (offline, a timeout, a failing service): the same growing, spread wait as code
          // creation, and never sooner than the service asked for.
          const wait = Math.max(this.backoff(++this.pollFailures), (error instanceof ApiError ? error.retryAfterSeconds ?? 0 : 0) * 1000);
          await this.change(record => ({...record, current: record.current && {...record.current, nextPollAt: Date.now() + wait}}));
          this.show('Activation is offline. Your saved request will resume.');
        }
      }
    }
    if (generation !== this.generation || !this.running || this.journal?.current?.grant !== undefined) return;
    const now = Date.now(), remaining = this.journal?.current ? Date.parse(this.journal.current.authorization.expiresAt) - now : 0;
    if (remaining <= this.options.replaceBeforeMs && now >= this.createAfter) {
      // Reserve room to retire the visible request AND an uncertain creation.
      if((this.journal?.retired.length??0)>=7){this.show('Previous activations need cleanup before replacement.');return;}
      if (!this.journal?.creating) {
        const requestId = await this.requestId();
        if (generation !== this.generation) return;
        if (!/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(requestId)) throw new Error('Secure activation identifiers are unavailable.');
        await this.change(record => ({...record, creating: {requestId, startedAt: now}}));
      }
      const creating = this.journal!.creating!;
      let authorization: DeviceAuthorization;
      try {authorization = await this.bounded(signal => this.request(explicit ? 'interactive' : 'automatic', () => this.api.create(creating.requestId, signal, explicit ? 'interactive' : 'automatic')));}
      catch (error) {
        if (generation !== this.generation) return;
        if (error instanceof ApiError && error.code === 'invalid_grant') await this.change(record => ({...record, creating: undefined}));
        if (error instanceof ApiError && error.retryAfterSeconds) this.createAfter = Date.now() + error.retryAfterSeconds * 1000;
        throw error;
      }
      validAuthorization(authorization, Date.now());
      const entry: SetupCodeEntry<T> = {requestId: creating.requestId, authorization, nextPollAt: Date.now() + authorization.interval * 1000};
      if (generation !== this.generation) {
        // The response carries a secret learned after cancellation. Quarantine
        // it before cleanup rather than forgetting an uncertain issued grant.
        await this.change(record => ({...record, creating:record.creating?.requestId===entry.requestId?undefined:record.creating, retired:record.retired.some(v=>v.requestId===entry.requestId)?record.retired:[...record.retired, entry]})); return;
      }
      if (Date.parse(authorization.expiresAt) <= Date.now()) {
        await this.change(record => ({...record, creating: undefined, retired: [...record.retired, entry]}));
        throw new Error('Creating a new activation code.');
      }
      await this.change(record => ({...record, current: entry, creating: undefined, retired: record.current ? [...record.retired, record.current] : record.retired}));
      this.failures = 0; this.pollFailures = 0; this.createAfter = 0; this.show();
    } else this.show();
    if (generation === this.generation && Date.now() >= this.cleanupAfter) await this.cleanup();
  }
  private async retireCurrent() {await this.change(record => ({...record, current: undefined, retired: record.current ? [...record.retired, record.current] : record.retired}));}
  private request<R>(kind: HostedRequestKind, work:()=>Promise<R>, protocol=false):Promise<R> {
    return this.gate ? this.gate.run(kind, work, undefined, protocol ? ['authorization_pending', 'slow_down', 'access_denied', 'expired_token', 'invalid_grant'] : []) : work();
  }
  private async cleanup(kind:HostedRequestKind='automatic') {
    this.cleanupAfter = Date.now() + 30_000;
    const entry = this.journal?.retired[0];
    if(entry){try {await this.bounded(signal => this.request(kind, () => this.api.cancel(entry, signal, kind)));
      await this.change(record => ({...record, retired: record.retired.filter(old => old.requestId !== entry.requestId)}));
      this.api.forgotten?.(entry);
    } catch { /* The protected cancellation receipt survives an outage. */ }}
    const creating=this.journal?.creating;
    if(creating?.cancelled&&(this.journal?.retired.length??0)<8){
      try{
        // Cancellation cannot discard an idempotency key merely because the
        // create reply was lost. Reconcile that SAME operation, then revoke it.
        const authorization=await this.bounded(signal=>this.request(kind,()=>this.api.create(creating.requestId,signal,kind)));
        validAuthorization(authorization,Date.now());
        const retired:SetupCodeEntry<T>={requestId:creating.requestId,authorization,nextPollAt:0};
        await this.change(record=>record.creating?.requestId===creating.requestId?{...record,creating:undefined,retired:record.retired.some(v=>v.requestId===retired.requestId)?record.retired:[...record.retired,retired]}:record);
        await this.bounded(signal=>this.request(kind,()=>this.api.cancel(retired,signal,kind)));
        await this.change(record=>({...record,retired:record.retired.filter(v=>v.requestId!==retired.requestId)}));
        this.api.forgotten?.(retired);
      }catch(error){
        // Expired creation replay is conclusive only at the issuing authority.
        if(error instanceof ApiError&&['invalid_grant','expired_token'].includes(error.code))await this.change(record=>record.creating?.requestId===creating.requestId?{...record,creating:undefined}:record);
      }
    }
  }
  private ownsGrant(grant:T):boolean {
    return this.state.phase==='approved'&&this.state.grant===grant&&this.journal?.current?.requestId===this.approvedRequestId&&
      this.journal?.current?.grant!==undefined&&JSON.stringify(this.journal.current.grant)===JSON.stringify(grant);
  }
  isCurrent = (grant: T) => this.running && this.ownsGrant(grant);
  /** Called only AFTER the account/native family was durably adopted. */
  async complete(grant: T) {
    if (!this.ownsGrant(grant)) return false;
    const entry = this.journal!.current!;
    await this.change(record => {
      if (record.current?.requestId !== entry.requestId) throw new Error('Activation changed.');
      return {...record, current: undefined, creating: record.creating?{...record.creating,cancelled:true}:undefined};
    });
    this.pause(); this.api.forgotten?.(entry); this.emit({phase: 'idle'}); void this.cleanup(); return true;
  }
  async cancel() {
    ++this.generation; this.pause(); this.emit({phase:'idle'});
    for(const controller of this.controllers)controller.abort();
    await this.operation; // Bounded; stale results remain durable for cleanup.
    await this.retireCurrent();
    await this.change(record => ({...record, creating:record.creating?{...record.creating,cancelled:true}:undefined}));
    await this.cleanup('interactive');
  }
}

export type SetupPreview = {clientRequestId?:string;publicKey?:string;protocol?:string;requestId: string; userCode: string; deviceName: string; platform: string; appVersion: string; expiresAt: string};
/** The caller selects one authority BEFORE entering/reviewing a code. No search
 * across servers, no inferred authority from an ambiguous human code. */
export class SetupCodeApprover {
  private readonly api:HttpLocalApi;readonly authority:'hosted'|'local';
  private readonly gate?:HostedGate;
  constructor(api:HttpLocalApi,authority:'hosted'|'local'){this.api=api;this.authority=authority;this.gate=authority==='hosted'?hostedGate(api.baseUrl):undefined;}
  private request<T>(work:()=>Promise<T>):Promise<T>{return this.gate?this.gate.run('interactive',work):work();}
  private get path() {return this.authority === 'hosted' ? hostedRoutePath("device_authorization_create") : routePath("post_quick_connect");}
  async review(code: string, signal?: AbortSignal) {
    const result = await this.request(() => this.api.request<SetupPreview>(this.path + '/review', 'POST', {userCode: normalizeSetupCode(code)}, signal));
    if (!result || typeof result.requestId !== 'string' || result.requestId.length > 128 ||
        normalizeSetupCode(result.userCode) !== normalizeSetupCode(code) ||
        !Number.isFinite(Date.parse(result.expiresAt)) || Date.parse(result.expiresAt) <= Date.now() ||
        typeof result.deviceName !== 'string' || result.deviceName.length > 100)
      throw new Error('This device request is no longer available.');
    return result;
  }
  decide(preview: SetupPreview, decision: 'approve' | 'deny', signal?: AbortSignal) {
    return this.request(() => this.api.request<void>(this.path + '/decision', 'POST', {requestId: preview.requestId, userCode: normalizeSetupCode(preview.userCode), decision}, signal));
  }
}
