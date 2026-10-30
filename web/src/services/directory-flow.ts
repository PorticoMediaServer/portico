import {RouteError} from '@core/route-identity.ts';
import type {WorkspaceServers} from './workspace-api';
import {accountDirectoryFailure, directoryFailurePhase} from './directory-errors';

export type DirectoryItem = WorkspaceServers['items'][number];
export type DirectorySnapshot<A = unknown> = Readonly<{items: readonly DirectoryItem[]; phase: 'idle'|'reading'|'opening'|'opened'; target?: DirectoryItem; showTarget?: boolean; status?: ReturnType<typeof directoryFailurePhase>; checkedAt: number; pendingId?: string; error?: unknown; errorSource?: 'account' | 'server'; waitingId?: string; seconds: number; admitted?: A}>;
/** Where an error came from. Only a failure of Portico Accounts' own requests is an account
 * error; a server that does not answer, or cannot prove itself, is that server's (H12). */
export type DirectoryErrorSource = NonNullable<DirectorySnapshot['errorSource']>;
/** An admission is the server's own sign-in state, owned by the caller once it is published. */
type Admission = Readonly<{dispose: () => void}>;
type Ports<A extends Admission> = Readonly<{current: () => boolean; read: (signal: AbortSignal) => Promise<WorkspaceServers>; admit: (server: DirectoryItem, signal: AbortSignal) => Promise<A>; empty: () => void; now?: () => number}>;

/** The account's server chooser at web.getportico.tv (connect 1-31). Opening a server signs in
 * to it here, on this page's own address: the browser is never sent to the server. Waiting and
 * retrying belong only to the server the person asked to open; no other server is ever tried
 * in its place. */
export class ServicesDirectoryFlow<A extends Admission = Admission> {
  private state: DirectorySnapshot<A>;
  private listeners = new Set<() => void>();
  private active?: {controller: AbortController; automatic: boolean};
  private disposed = false;
  private delay = 20;
  private initialAttempted = false;
  private readonly startedAt: number;
  /** `autoOpen`: this opening may open the account's only server by itself (a first visit with
   * nothing selected). A person who came here to choose is never taken anywhere unasked. */
  constructor(initial: WorkspaceServers, private readonly ports: Ports<A>, private readonly autoOpen = false) {
    this.startedAt = this.now();
    this.state = Object.freeze({items: initial.items, phase: 'idle', checkedAt: this.now(), seconds: 20});
  }
  private now() {return this.ports.now?.() ?? Date.now();}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current() {return !this.disposed && this.ports.current();}
  private publish(state: DirectorySnapshot<A>) {if (this.current()) {this.state = Object.freeze(state); this.listeners.forEach(listener => listener());}}
  automaticInitial() {
    if (!this.current() || this.initialAttempted) return;
    this.initialAttempted = true;
    if (this.autoOpen && this.state.items.length === 1 && this.state.items[0].status === 'active') {this.delay = 10; void this.open(this.state.items[0], this.state, true);}
  }
  retryDelay() {return this.delay;}
  inConnectingGrace(now = this.now()) {return now - this.startedAt < 40_000;}
  reportError(error: unknown, expected: DirectorySnapshot<A>) {if(this.current()&&this.state===expected)this.publish({...this.state,error});}
  countdown(seconds: number) {if (this.current() && !this.active && this.state.seconds !== seconds) this.publish({...this.state, seconds});}
  /** Retry the server the person is waiting for, and only that one. */
  automatic() {
    const target = this.state.target;
    if (!this.current() || this.active || this.state.phase !== 'idle' || !target || this.state.waitingId !== target.id) return;
    void this.open(target, this.state, true);
  }
  cancelAutomatic() {
    const active = this.active;
    if (!active?.automatic) return;
    this.active = undefined; active.controller.abort();
    this.publish({...this.state, phase: 'idle', pendingId: undefined});
  }
  cancelOpening(expected: DirectorySnapshot<A>) {
    if (!this.current() || this.state !== expected) return;
    const active = this.active; this.active = undefined; active?.controller.abort();
    expected.admitted?.dispose();
    this.publish({...this.state, phase: 'idle', pendingId: undefined, waitingId: undefined, target: undefined, status: undefined, showTarget: undefined, error: undefined, errorSource: undefined, admitted: undefined});
  }
  async refresh(expected: DirectorySnapshot<A>) {
    if (!this.current() || this.state !== expected || this.active || expected.phase === 'opened') return;
    const active = {controller: new AbortController(), automatic: false}; this.active = active;
    const current = () => this.current() && this.active === active && !active.controller.signal.aborted;
    this.publish({...expected, phase: 'reading', error: undefined, errorSource: undefined});
    try {
      const result = await this.ports.read(active.controller.signal); if (!current()) return;
      this.delay = 20;
      this.publish({items: result.items, phase: 'idle', checkedAt: this.now(), seconds: 20});
      if (current() && !result.items.length) this.ports.empty();
    } catch (error) {if (current()) this.publish({...this.state, phase: 'idle', error: accountDirectoryFailure(error), errorSource: 'account'});}
    finally {if (this.active === active) this.active = undefined;}
  }
  /** After a row was removed or left: drop it here at once, then reread the account. */
  forget(id: string, expected: DirectorySnapshot<A>) {
    if (!this.current() || this.state !== expected || this.active) return;
    const items = expected.items.filter(item => item.id !== id);
    this.publish({items, phase: 'idle', checkedAt: expected.checkedAt, seconds: 20});
    void this.refresh(this.state);
  }
  async open(server: DirectoryItem, expected: DirectorySnapshot<A>, automatic = false) {
    if (!this.current() || this.state !== expected || this.active || expected.phase === 'opened' || !expected.items.includes(server) && expected.target !== server) return;
    const captured = Object.freeze({...server}), active = {controller: new AbortController(), automatic}; this.active = active;
    const current = () => this.current() && this.active === active && !active.controller.signal.aborted;
    this.publish({...expected, phase: 'opening', pendingId: captured.id, target: captured, status: undefined, showTarget: !automatic || expected.showTarget || expected.items.length === 1 && captured.status === 'active', error: undefined, errorSource: undefined});
    let readingAccount = true;
    try {
      const result = await this.ports.read(active.controller.signal); if (!current()) return;
      readingAccount = false;
      const matches = result.items.filter(item => item.id === captured.id), fresh = matches[0];
      if (!result.items.length) {this.publish({...this.state, items: result.items, phase: 'idle', pendingId: undefined, target: undefined, waitingId: undefined, checkedAt: this.now()}); if (current()) this.ports.empty(); return;}
      if (matches.length !== 1 || fresh.publicKey !== captured.publicKey || fresh.policyRevision < captured.policyRevision) throw new RouteError('identity_mismatch', 'The selected server identity or membership changed.');
      this.publish({...this.state, items: result.items, checkedAt: this.now()});
      const admitted = await this.ports.admit(Object.freeze({...fresh}), active.controller.signal);
      if (!current()) {admitted.dispose(); return;}
      this.delay = 20;
      this.publish({...this.state, phase: 'opened', pendingId: undefined, waitingId: undefined, status: undefined, error: undefined, errorSource: undefined, admitted});
    } catch (error) {
      if (current()) {
        const failure = readingAccount ? accountDirectoryFailure(error) : error;
        const retry = Number((failure as {retryAfterSeconds?: unknown})?.retryAfterSeconds);
        const floor = Number.isFinite(retry) && retry > 0 ? Math.min(86400, Math.ceil(retry)) : 0;
        this.delay = Math.max(floor, Math.min(300, automatic ? this.delay * 2 : 20));
        const status = directoryFailurePhase(failure, automatic);
        // Only a wait is retried; a refusal, a changed identity or an update stays put until asked.
        const waiting = status === 'waiting' || status === 'account-waiting' || status === 'unreachable';
        this.publish({...this.state, phase: 'idle', pendingId: undefined, waitingId: waiting ? captured.id : undefined, error: failure, errorSource: readingAccount ? 'account' : 'server', status, seconds: this.delay});
      }
    } finally {if (this.active === active) this.active = undefined;}
  }
  dispose() {
    if (this.disposed) return; this.disposed = true;
    const active = this.active; this.active = undefined; active?.controller.abort();
    this.listeners.clear();
  }
}
