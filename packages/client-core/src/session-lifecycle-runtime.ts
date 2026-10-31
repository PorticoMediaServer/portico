/**
 * Runs the session lifecycle (session-lifecycle.ts) against a platform. Views read getSnapshot()
 * and start the actions; nothing else on a device writes which server, profile or sign-in is in
 * use. Ports do the I/O; this file decides when, and drops any result whose sign-in has since
 * changed (the generation moved).
 */
import {ApiError} from './index.ts';
import {releaseQuietly} from './cleanup.ts';
import {systemScheduler, serverFloorFrom, type Scheduler} from './timer.ts';
import {createUserAction, type UserAction} from './user-action.ts';
import type {AuthoritySource, AuthorityTuple} from './session-authority.ts';
import {initialLifecycle, nextLifecycle, type ConnectFailure, type ConnectedFacts, type HeldSignIn, type LifecycleEffect,
  type LifecycleEvent, type LifecycleSnapshot, type SelectedFacts} from './session-lifecycle.ts';


/** One line per move: codes only, never ids, names, addresses or tokens. */
export type TransitionRecord = Readonly<{t: number; from: string; event: string; to: string; row: number | 'ignored'; generation: number}>;

export type RecordsRead = Readonly<{outcome: 'ok' | 'lost' | 'reset'; servers: number; held?: HeldSignIn}>;
export type HeldRead = Readonly<{servers: number; held?: HeldSignIn}>;
export type SelectInput = Readonly<{serverId: string; profileId: string; pin?: string; remember: boolean}>;

export type LifecyclePorts = Readonly<{
  /** Launch: prepare protected storage, then read the selected server's sign-in and its marker. */
  readRecords(): Promise<RecordsRead>;
  /** The person confirmed resetting this device's records (records lost). */
  resetRecords(): Promise<void>;
  /** Re-read what the record holds now (after a switch, or when another owner changed it). */
  readHeld(): Promise<HeldRead>;
  /** Connect to the held server, prove its identity, and read the account's profiles. */
  connect(serverId: string, signal: AbortSignal): Promise<ConnectedFacts>;
  /** Choose a profile. The adapter persists the successor sign-in (and its marker when
   *  remembered) before resolving, then ends the replaced sign-in in the background. */
  select(input: SelectInput, signal: AbortSignal): Promise<SelectedFacts>;
  storeSelect(serverId: string, signal: AbortSignal): Promise<void>;
  signOutServer(serverId: string, familyId: string, removeDownloads: boolean, signal: AbortSignal): Promise<HeldRead>;
  signOutAccount(signal: AbortSignal): Promise<HeldRead>;
  /** After an identity failure: forget the server without contacting the address. */
  forgetServer(serverId: string, signal: AbortSignal): Promise<HeldRead>;
  clearMarker(serverId: string): Promise<void>;
  /** Retire the mounted shell's private state before the sign-in it was built on changes. */
  fenceShell(): void;
  drainRevocations(serverId: string): void;
  /** Called when another owner changed the protected record. */
  observeRecords(listener: () => void): () => void;
  /** Maps a connect failure. Unknown failures read as offline (Can't reach). */
  classifyConnect(error: unknown): ConnectFailure | undefined;
  log?(record: TransitionRecord): void;
  scheduler?: Scheduler;
}>;

/** The boot screen's longest wait (boot.html): after this, Can't reach. */
export const CONNECT_DEADLINE_MS = 15_000;
const HISTORY = 200;
const pinRejection = (error: unknown) => error instanceof ApiError && (error.code === 'profile_pin_invalid' || error.code === 'profile_pin_locked');

export class SessionLifecycle implements AuthoritySource {
  readonly actions: Readonly<{
    pickProfile: UserAction<void, [profileId: string]>;
    submitPin: UserAction<void, [pin: string, remember: boolean]>;
    unlockManagement: UserAction<void, [pin: string]>;
    switchServer: UserAction<void, [server: HeldSignIn['server']]>;
    signOutServer: UserAction<void, [removeDownloads: boolean]>;
    signOutPorticoAccount: UserAction<void, []>;
    forgetServer: UserAction<void, []>;
    resetRecords: UserAction<void, []>;
    adoptSignedIn: UserAction<void, [held: HeldSignIn, continuation: 'viewer' | 'profile-selection']>;
  }>;
  private snapshot: LifecycleSnapshot = initialLifecycle;
  private readonly listeners = new Set<() => void>();
  private readonly ring: TransitionRecord[] = [];
  private readonly scheduler: Scheduler;
  private readonly started: number;
  private connecting?: AbortController;
  private stopObserving?: () => void;
  private disposed = false;

  private readonly ports: LifecyclePorts;

  constructor(ports: LifecyclePorts) {
    this.ports = ports;
    this.scheduler = ports.scheduler ?? systemScheduler;
    this.started = this.scheduler.now();
    const action = <A extends readonly unknown[]>(run: (signal: AbortSignal, ...args: A) => Promise<void>) =>
      createUserAction<void, A>({run: (context, ...args) => run(context.signal, ...args), scheduler: this.scheduler});
    this.actions = Object.freeze({
      pickProfile: action<[string]>(async (signal, profileId) => this.command({type: 'profileTapped', profileId}, signal)),
      submitPin: action<[string, boolean]>(async (signal, pin, remember) => this.command({type: 'pinSubmitted', pin, remember}, signal)),
      unlockManagement: action<[string]>(async (signal, pin) => this.command({type: 'unlockSubmitted', pin}, signal)),
      switchServer: action<[HeldSignIn['server']]>(async (signal, server) => this.command({type: 'switchServer', server}, signal)),
      signOutServer: action<[boolean]>(async (signal, removeDownloads) => this.command({type: 'signOutServer', removeDownloads}, signal)),
      signOutPorticoAccount: action<[]>(async signal => this.command({type: 'signOutAccount'}, signal)),
      forgetServer: action<[]>(async signal => this.command({type: 'forget'}, signal)),
      resetRecords: action<[]>(async signal => this.command({type: 'recordsResetConfirmed'}, signal)),
      adoptSignedIn: action<[HeldSignIn, 'viewer' | 'profile-selection']>(async (_signal, held, continuation) => {
        await this.dispatch({type: 'signedIn', held, continuation});
      }),
    });
  }

  getSnapshot = (): LifecycleSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => {this.listeners.delete(listener);};
  };
  history(): readonly TransitionRecord[] {return Object.freeze([...this.ring]);}

  /** Who is signed in where for the mounted shell; undefined while no profile is in use. While the
   *  person is choosing a profile to switch to, the shell still runs on the current sign-in. */
  authority(): AuthorityTuple | undefined {
    const s = this.snapshot.state;
    if (s.kind !== 'ready' && !((s.kind === 'choosingProfile' || s.kind === 'enteringPin') && s.switching)) return undefined;
    return Object.freeze({serverId: s.server.serverId, accountId: s.viewer.accountId, profileId: s.viewer.profileId, familyId: s.viewer.familyId});
  }

  /** Cold start. */
  start(): void {
    if (this.disposed) return;
    this.stopObserving ??= this.ports.observeRecords(() => {void this.recordsChanged();});
    void this.dispatch({type: 'launch'});
  }
  /** Foreground or network back: retry a server that wasn't answering. */
  resume(): void {void this.dispatch({type: 'retry'});}
  otherProfiles(): void {void this.dispatch({type: 'otherProfiles'});}
  switchProfile(): void {void this.dispatch({type: 'switchProfile'});}
  cancelSwitch(): void {void this.dispatch({type: 'cancelSwitch'});}
  /** A request on the mounted shell answered profile_pin_reentry, a terminal refresh refusal, or an identity failure. */
  report(event: Extract<LifecycleEvent, {type: 'gated' | 'signInEnded' | 'identityFailed' | 'networkLost'}>): void {void this.dispatch(event);}

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.connecting?.abort();
    releaseQuietly('lifecycle observer', () => this.stopObserving?.());
    for (const value of Object.values(this.actions)) value.dispose();
    this.listeners.clear();
  }

  /** A user command: refused unless the current state takes it, and it ends only when its effects have. */
  private async command(event: LifecycleEvent, signal: AbortSignal): Promise<void> {
    if (!(await this.dispatch(event, signal))) throw Object.assign(new Error('session_lifecycle_not_now'), {code: 'cancelled'});
  }

  /** Applies one event and runs its effects. Resolves false when the event did not apply. */
  private async dispatch(event: LifecycleEvent, signal?: AbortSignal): Promise<boolean> {
    if (this.disposed) return false;
    const from = this.snapshot, move = nextLifecycle(from, event);
    this.record(from, event, move?.snapshot ?? from, move?.row ?? 'ignored');
    if (!move) return false;
    this.snapshot = move.snapshot;
    for (const listener of [...this.listeners]) releaseQuietly('lifecycle listener', listener);
    for (const effect of move.effects) await this.run(effect, move.snapshot.generation, signal);
    return true;
  }

  private record(from: LifecycleSnapshot, event: LifecycleEvent, to: LifecycleSnapshot, row: number | 'ignored') {
    const value: TransitionRecord = Object.freeze({t: Math.round(this.scheduler.now() - this.started), from: from.state.kind, event: event.type,
      to: to.state.kind, row, generation: to.generation});
    this.ring.push(value);
    if (this.ring.length > HISTORY) this.ring.shift();
    if (this.ports.log) releaseQuietly('lifecycle log', () => this.ports.log!(value));
  }

  /** Follow-up events only publish for the sign-in that asked for them. */
  private current(generation: number) {return !this.disposed && this.snapshot.generation === generation;}

  private async run(effect: LifecycleEffect, generation: number, signal = new AbortController().signal): Promise<void> {
    const p = this.ports;
    switch (effect.kind) {
    case 'fenceShell': releaseQuietly('lifecycle fence', () => p.fenceShell()); return;
    case 'drainRevocations': releaseQuietly('lifecycle drain', () => p.drainRevocations(effect.serverId)); return;
    case 'clearMarker': await p.clearMarker(effect.serverId).catch(error => releaseQuietly('lifecycle marker', () => {throw error;})); return;
    case 'readRecords': {
      let read: RecordsRead;
      try {read = await p.readRecords();} catch {read = {outcome: 'lost', servers: 0};}
      if (this.current(generation)) await this.dispatch({type: 'recordsRead', ...read});
      return;
    }
    case 'resetRecords': {
      try {await p.resetRecords();}
      catch (error) {
        if (this.current(generation) && this.snapshot.pending) await this.dispatch({type: 'actionFailed'});
        throw error;
      }
      if (this.current(generation)) await this.dispatch({type: 'signedOut', remaining: 0});
      return;
    }
    case 'connect': return this.connect(effect.serverId, generation);
    case 'select': {
      const pending = this.snapshot.pending;
      try {
        const facts = await p.select(effect, signal);
        if (!this.current(generation)) return;
        await this.dispatch(pending === 'unlock' ? {type: 'managementUnlocked', facts} : {type: 'selected', facts});
      } catch (error) {
        if (this.current(generation)) {
          if (pinRejection(error)) {
            const floor = serverFloorFrom(error)?.retryAfterMs;
            await this.dispatch({type: 'pinRejected', ...(floor ? {waitUntil: this.scheduler.now() + floor} : {})});
          }
          if (this.snapshot.pending) await this.dispatch({type: 'selectFailed'});
        }
        throw error;
      }
      return;
    }
    case 'storeSelect': return this.held(generation, () => p.storeSelect(effect.serverId, signal).then(() => p.readHeld()), 'envelopeChanged');
    case 'signOutServer': return this.held(generation, () => p.signOutServer(effect.serverId, effect.familyId, effect.removeDownloads, signal), 'signedOut');
    case 'signOutAccount': return this.held(generation, () => p.signOutAccount(signal), 'signedOut');
    case 'forgetServer': return this.held(generation, () => p.forgetServer(effect.serverId, signal), 'forgotten');
    }
  }

  private async held(generation: number, work: () => Promise<HeldRead>, then: 'envelopeChanged' | 'signedOut' | 'forgotten') {
    try {
      const read = await work();
      if (!this.current(generation)) return;
      await this.dispatch(then === 'envelopeChanged' ? {type: 'envelopeChanged', servers: read.servers, ...(read.held ? {held: read.held} : {})}
        : {type: then, remaining: read.servers});
    } catch (error) {
      if (this.current(generation) && this.snapshot.pending) await this.dispatch({type: 'actionFailed'});
      throw error;
    }
  }

  /** Bounded by the boot screen's 15 s: a server that hasn't answered by then is Can't reach. */
  private async connect(serverId: string, generation: number) {
    this.connecting?.abort();
    const controller = this.connecting = new AbortController();
    // The deadline wins even when a port ignores its signal: the late answer is then dropped.
    let expire!: () => void;
    const deadline = new Promise<never>((_, reject) => {expire = () => {controller.abort(); reject(Object.assign(new Error('connect_deadline'), {code: 'timeout'}));};});
    const timer = this.scheduler.setTimeout(() => expire(), CONNECT_DEADLINE_MS);
    try {
      const attempt = this.ports.connect(serverId, controller.signal);
      attempt.catch(() => {});
      const facts = await Promise.race([attempt, deadline]);
      if (this.current(generation) && !controller.signal.aborted) await this.dispatch({type: 'connected', facts});
    } catch (error) {
      if (!this.current(generation)) return;
      let failure: ConnectFailure | undefined;
      try {failure = controller.signal.aborted ? undefined : this.ports.classifyConnect(error);} catch {failure = undefined;}
      await this.dispatch({type: 'connectFailed', failure: failure ?? {reason: 'offline'}});
    } finally {
      this.scheduler.clearTimeout(timer);
      if (this.connecting === controller) this.connecting = undefined;
    }
  }

  /** Another owner changed the record. Ignored while one of our own actions is writing it. */
  private async recordsChanged() {
    if (this.disposed || this.snapshot.pending) return;
    const generation = this.snapshot.generation;
    let read: HeldRead;
    try {read = await this.ports.readHeld();} catch {return;}
    if (this.current(generation) && !this.snapshot.pending)
      await this.dispatch({type: 'envelopeChanged', servers: read.servers, ...(read.held ? {held: read.held} : {})});
  }
}
