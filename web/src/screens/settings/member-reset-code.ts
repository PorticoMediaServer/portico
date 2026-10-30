import {makeMemberResetCode, replaceMemberResetCode, type MemberResetCode} from '@core/account-reset.ts';
import type {ProfileAPI} from '@core/profile-management.ts';
import type {AccessMember} from '@core/server-administration.ts';

export type MemberResetCaller = Readonly<{isCurrent: () => boolean; signal: AbortSignal}>;
export type MemberResetState =
  | Readonly<{kind: 'pending'; turnOffTwoStep: boolean}>
  | Readonly<{kind: 'ready'; turnOffTwoStep: boolean; result: MemberResetCode; error?: unknown}>
  | Readonly<{kind: 'unresolved'; turnOffTwoStep: boolean; error: unknown}>
  | Readonly<{kind: 'retired'}>;
type Target = Readonly<Pick<AccessMember, 'accountId' | 'username' | 'role' | 'revision' | 'hostedAccountId'>>;
type Options = Readonly<{serverId: string; accountId: string; isCurrent: () => boolean;
  copy: (code: string) => Promise<void>; now?: () => number}>;
const ended = () => new DOMException('The original reset opening ended.', 'AbortError');
const changed = () => Object.assign(new Error('The reset code changed.'), {code: 'profile_changed'});
const active = (caller: MemberResetCaller) => {try {return !caller.signal.aborted && caller.isCurrent();} catch {return false;}};

/** Constructed only for the captured Local owner/main-profile Settings build.
 * Local-kind's global no-link invariant supplies linkage admission; a known
 * contradictory linked fact still refuses. No People reread or latest revision. */
export class MemberResetCodeSource {
  private disposed = false;
  private opening?: MemberResetCodeOpening;
  private running?: MemberResetCodeOpening;
  private claiming = false;
  private readonly listeners = new Set<() => void>();
  constructor(private readonly api: ProfileAPI, private readonly options: Options) {}
  current = () => {try {return !this.disposed && this.options.isCurrent();} catch {return false;}};
  getSnapshot = () => this.running !== undefined;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private emit() {for (const listener of this.listeners) {try {listener();} catch { /* Busy publication cannot authorize a new flight. */ }}}
  eligible = (target: Target) => this.current() && target.accountId !== this.options.accountId &&
    (target.role === 'member' || target.role === 'admin') && !target.hostedAccountId &&
    typeof target.accountId === 'string' && target.accountId.length > 0 && target.accountId.length <= 128 &&
    !/[^A-Za-z0-9._:-]/.test(target.accountId) && /[A-Za-z0-9]/.test(target.accountId) && typeof target.username === 'string' && target.username.length > 0 &&
    Number.isSafeInteger(target.revision) && target.revision > 0;
  available = (target: Target) => !this.claiming && !this.running && this.eligible(target);
  open = (target: Target, caller: MemberResetCaller): MemberResetCodeOpening | undefined => {
    if (this.claiming || this.running) return undefined;
    this.claiming = true;
    try {
      if (!this.eligible(target) || !active(caller)) return undefined;
      this.opening?.retire();
      const captured: Target = Object.freeze({accountId: target.accountId, username: target.username,
        role: target.role, revision: target.revision, hostedAccountId: target.hostedAccountId});
      const opening = new MemberResetCodeOpening(this.api, captured, Object.freeze({...caller}), this.options,
        () => this.current() && this.opening === opening,
        () => {if (this.running) return false;this.running = opening;this.emit();return true;},
        () => {if (this.running === opening) {this.running = undefined;this.emit();}});
      this.opening = opening;
      void opening.issue();
      return opening;
    } finally {this.claiming = false;}
  };
  dispose = () => {if (this.disposed) return;this.disposed = true;this.opening?.retire();this.opening = undefined;this.emit();this.listeners.clear();};
}

/** One explicit Make intent. Replacement carries only this exact published
 * code, target revision, choice and unchanged expiry. Closing never authorizes
 * another opening; uncertain replacement cannot restore the withdrawn code. */
export class MemberResetCodeOpening {
  private live = true;
  private started = false;
  private flight?: object;
  private readonly abort = new AbortController();
  private readonly listeners = new Set<() => void>();
  private state: MemberResetState = Object.freeze({kind: 'pending', turnOffTwoStep: false});
  private readonly stop: () => void;
  constructor(private readonly api: ProfileAPI, readonly target: Target, private readonly caller: MemberResetCaller,
    private readonly options: Options, private readonly parentCurrent: () => boolean,
    private readonly claim: () => boolean, private readonly release: () => void) {
    this.stop = () => this.retire();
    caller.signal.addEventListener('abort', this.stop, {once: true});
  }
  current = () => this.live && !this.abort.signal.aborted && active(this.caller) && this.parentCurrent();
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: MemberResetState) {
    if (!this.current()) return;
    this.state = Object.freeze(state);
    for (const listener of this.listeners) {try {listener();} catch { /* A view cannot change the command outcome. */ }}
  }
  private now() {return (this.options.now ?? Date.now)();}
  unexpired = (expected: MemberResetState) => this.state === expected && expected.kind === 'ready' && this.current() &&
    Date.parse(expected.result.expiresAt) > this.now();
  private assert() {if (!this.current()) throw ended();}
  private valid(result: MemberResetCode, previous?: MemberResetCode) {
    if (result.serverId !== undefined && result.serverId !== this.options.serverId) throw changed();
    if (Date.parse(result.expiresAt) <= this.now()) throw changed();
    if (previous && (result.expiresAt !== previous.expiresAt || result.code === previous.code)) throw changed();
  }
  issue = async () => {
    if (this.started || this.flight || !this.current()) return;
    this.started = true;
    const flight = {};this.flight = flight;
    if (!this.claim()) {this.flight = undefined;return;}
    try {
      this.assert();
      const result = await makeMemberResetCode(this.api, this.target.accountId, this.target.revision, false, this.abort.signal);
      this.assert();this.valid(result);
      this.publish({kind: 'ready', turnOffTwoStep: false, result});
    } catch (error) {this.publish({kind: 'unresolved', turnOffTwoStep: false, error});}
    finally {if (this.flight === flight) {this.flight = undefined;this.release();}}
  };
  change = async (expected: MemberResetState, turnOffTwoStep: boolean) => {
    if (this.state !== expected || expected.kind !== 'ready' || this.flight || typeof turnOffTwoStep !== 'boolean' || turnOffTwoStep === expected.turnOffTwoStep) return;
    const flight = {};this.flight = flight;
    if (!this.claim()) {this.flight = undefined;return;}
    // Withdraw plaintext and Copy synchronously, before predicates or IO can
    // re-enter. Only the request-local original proof remains for this flight.
    this.publish({kind: 'pending', turnOffTwoStep});
    try {
      this.assert();this.valid(expected.result);
      const result = await replaceMemberResetCode(this.api, this.target.accountId, this.target.revision,
        expected.result.code, turnOffTwoStep, this.abort.signal);
      this.assert();this.valid(result, expected.result);
      this.publish({kind: 'ready', turnOffTwoStep, result});
    } catch (error) {this.publish({kind: 'unresolved', turnOffTwoStep, error});}
    finally {if (this.flight === flight) {this.flight = undefined;this.release();}}
  };
  copy = async (expected: MemberResetState) => {
    if (this.state !== expected || expected.kind !== 'ready' || this.flight) return;
    try {
      this.assert();this.valid(expected.result);
      await this.options.copy(expected.result.code);
    } catch (error) {
      if (this.current() && this.state === expected) {
        if (Date.parse(expected.result.expiresAt) <= this.now()) this.publish({kind: 'unresolved', turnOffTwoStep: expected.turnOffTwoStep, error});
        else this.publish({...expected, error});
      }
    }
  };
  retire = () => {
    if (!this.live) return;
    this.live = false;this.abort.abort();this.caller.signal.removeEventListener('abort', this.stop);
    this.state = Object.freeze({kind: 'retired'});
    for (const listener of this.listeners) {try {listener();} catch { /* Retire all view listeners. */ }}
    this.listeners.clear();
  };
}

export type MemberResetPanelState = Readonly<{kind: 'empty'} | {kind: 'open'; opening: MemberResetCodeOpening} | {kind: 'retired'}>;
/** The exact committed People panel, separate from a reset flight. Every drawn
 * Make/Done/parent-close handler carries its original immutable screen state. */
export class MemberResetCodePanel {
  private live = true;
  private claiming = false;
  private ticket?: object;
  private readonly abort = new AbortController();
  private readonly listeners = new Set<() => void>();
  private state: MemberResetPanelState = Object.freeze({kind: 'empty'});
  constructor(private readonly source: MemberResetCodeSource | undefined, private readonly target: Target | undefined,
    private readonly isCurrent: () => boolean, private readonly canWrite: () => boolean) {}
  current = () => {try {return this.live && !this.abort.signal.aborted && this.isCurrent();} catch {return false;}};
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  present = () => !!this.source && !!this.target && this.current() && this.source.eligible(this.target);
  private emit() {for (const listener of this.listeners) {try {listener();} catch { /* One view cannot retain another. */ }}}
  private holds(expected: MemberResetPanelState) {return this.current() && this.state === expected;}
  canMake = (expected: MemberResetPanelState) => this.holds(expected) && expected.kind === 'empty' && !!this.target &&
    !!this.source && this.source.available(this.target) && this.canWrite();
  begin = (expected: MemberResetPanelState) => {
    if (this.claiming || this.state !== expected || expected.kind !== 'empty') return;
    this.claiming = true;
    try {
      if (!this.canMake(expected) || !this.source || !this.target) return;
      const ticket = {};this.ticket = ticket;
      const opening = this.source.open(this.target, {signal: this.abort.signal,
        isCurrent: () => this.current() && this.ticket === ticket && this.canWrite()});
      if (!opening || !this.holds(expected) || this.ticket !== ticket) {opening?.retire();if (this.ticket === ticket) this.ticket = undefined;return;}
      this.state = Object.freeze({kind: 'open', opening});this.emit();
    } finally {this.claiming = false;}
  };
  done = (expected: MemberResetPanelState) => {
    if (!this.holds(expected) || expected.kind !== 'open') return;
    this.ticket = undefined;expected.opening.retire();this.state = Object.freeze({kind: 'empty'});this.emit();
  };
  leave = (expected: MemberResetPanelState) => {if (!this.holds(expected)) return false;this.retire();return true;};
  retire = () => {
    if (!this.live) return;
    this.live = false;this.ticket = undefined;this.abort.abort();
    if (this.state.kind === 'open') this.state.opening.retire();
    this.state = Object.freeze({kind: 'retired'});this.emit();this.listeners.clear();
  };
}
