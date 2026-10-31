import {releaseAll, releaseQuietly, releaseQuietlyAsync} from './cleanup.ts';
import {initialSetupView, stepSetup, type SetupEffect, type SetupEffectKind, type SetupEffectName, type SetupError, type SetupFormMode, type SetupInput, type SetupView} from './setup-machine.ts';
import {reconcileSetup, type SetupLocal, type SetupReading, type SetupStore} from './setup-store.ts';
import {createUserAction, describeActionError, USER_ACTION_WATCHDOG_MS, type UserAction} from './user-action.ts';
import {nextDelay, serverFloorFrom, systemScheduler, wait, type Scheduler} from './timer.ts';
import {randomId} from './random-id.ts';

/** The first-run setup controller: the runner around the pure reducer in setup-machine.ts (B-DESIGN §6). It holds the view, turns each
 * user intent into one A `createUserAction`, performs every effect the reducer emits through `SetupPorts`, and feeds each outcome back in.
 * Framework-free: web and native subscribe through getSnapshot/subscribe. Not yet wired to either.
 *
 * Two lanes, each one user action with its own watchdog: the user's intents, and the system's reads (start, the poll timer, and whatever
 * a read leads to, such as the exchange). A result that arrives after its action was aborted, or after dispose, is dropped untouched. */

type Of<K extends SetupInput['kind']> = Extract<SetupInput, {kind: K}>;

/** What a port is told about the call. `intentId` is the idempotency key of start, request approval, abandon and retry delivery;
 * it is stable across Try again, so a repeat never does the thing twice. */
export type SetupPortContext = Readonly<{signal: AbortSignal; intentId: string}>;
/** Handed to the one effect that needs them and held in memory only until that effect succeeds or the person leaves. */
export type SetupCredentials = Readonly<{username: string; password: string}>;
/** The first library, as entered: a folder path, or folders picked from the owner's own listing. */
export type SetupLibrary = Readonly<{kind: string; name: string; path: string; folders?: readonly string[]}>;

/** What a read of the server says. The port pairs if needed and asks with the record's token (none when there is no record). */
export type SetupServerRead = Readonly<{kind: 'state'; state: SetupReading; ownerSessionHeld?: boolean}> | Of<'tokenRefused' | 'notLocalSource' | 'alreadyComplete'>;
/** A read and the records it was made with: the port reads the store after pairing, since a store can be per server. */
export type SetupRead = Readonly<{server: SetupServerRead; local: SetupLocal}>;

/** Each port returns the reducer's own result input, or throws; a throw becomes `effectFailed` / `readFailed` with a data-only error.
 * Ports that write records (start, initialize, request approval, renew) write through `store`, and the controller re-reads it after. */
export interface SetupPorts {
  readonly store: SetupStore;
  readState(context: SetupPortContext): Promise<SetupRead>;
  /** S1. */
  start(context: SetupPortContext): Promise<Of<'startDone' | 'notLocalSource' | 'addressNotAllowed' | 'setupInProgress' | 'alreadyComplete' | 'tokenRefused'>>;
  /** S4. */
  initializeHosted(name: string, context: SetupPortContext): Promise<Of<'initializeDone' | 'tokenRefused' | 'alreadyComplete'>>;
  /** S5. */
  initializeLocal(name: string, credentials: SetupCredentials, context: SetupPortContext): Promise<Of<'initializeDone' | 'tokenRefused' | 'alreadyComplete'>>;
  /** S6, then binds the handoff in the store. */
  requestApproval(navigate: boolean, context: SetupPortContext): Promise<Of<'approvalReady' | 'tokenRefused' | 'alreadyComplete'>>;
  approveInApp(context: SetupPortContext): Promise<Of<'approvedInApp'>>;
  /** S8. `abandoned`: the server is back to open, and the controller retires the record and the attempt. */
  abandon(context: SetupPortContext): Promise<Of<'abandonRefused'> | Readonly<{kind: 'abandoned'}>>;
  /** S15. */
  retryDelivery(context: SetupPortContext): Promise<Of<'deliveryRetried' | 'tokenRefused' | 'alreadyComplete'>>;
  exchange(context: SetupPortContext): Promise<Of<'exchangeDone' | 'exchangeRefused' | 'tokenRefused'>>;
  /** S7. */
  renew(context: SetupPortContext): Promise<Of<'renewDone' | 'tokenRefused'>>;
  adoptOwner(context: SetupPortContext): Promise<Of<'ownerAdopted' | 'alreadyComplete'>>;
  addLibrary(library: SetupLibrary, context: SetupPortContext): Promise<Of<'libraryAdded'>>;
  /** Leave for Hosted. Fire and forget: a throw is logged and ignored. */
  navigate(url: string): void;
}

export type SetupControllerOptions = Readonly<{
  scheduler?: Scheduler;
  newIntentId?: () => string;
  /** Wall-clock ms, for record expiry and `busy_elsewhere.retryAt`. Waiting always uses the scheduler's monotonic clock. */
  wallNow?: () => number;
  /** Jitter source for reads; jitter is only ever added to the reducer's delay. */
  random?: () => number;
  /** Default true. */
  foreground?: boolean;
  slowAfterMs?: number;
  watchdogMs?: number;
  /** A data-only note for the local diagnostic of a retired unreadable record (R-13). */
  diagnostic?: (code: 'setup_record_unreadable') => void;
}>;

export interface SetupController {
  /** Frozen; the same object until the view changes. */
  getSnapshot(): SetupView;
  /** A person's action is running (a submit, Back, Try again, adding the library); listeners hear when it changes. */
  busy(): boolean;
  subscribe(listener: () => void): () => void;
  /** Pair, read the local record and the server, and land where the reducer's reconcile rows say. */
  start(): void;
  submitHosted(name: string, options: Readonly<{navigate: boolean}>): void;
  /** The credentials go to the S5 port call and nowhere else. */
  submitLocal(name: string, username: string, password: string): void;
  chooseKind(mode: SetupFormMode): void;
  back(): void;
  startAgain(): void;
  /** Try again. After a failure or an unconfirmed action it keeps the same intentId. */
  retry(): void;
  approveInApp(): void;
  /** The library goes to the add-library port call and is kept only until it succeeds or the person leaves. */
  addLibrary(library: SetupLibrary): void;
  skip(): void;
  /** Reads run only while foregrounded; going back to the foreground resumes a read that was held. */
  setForeground(foreground: boolean): void;
  /** Cancels actions and timers. Never throws. */
  dispose(): void;
}

/** A thrown value as data: a short code (the error's own, when it is a plain token), whether Try again is worth it, and the server's floor. */
export function setupErrorOf(error: unknown): SetupError {
  const raw = (error as {code?: unknown} | null | undefined)?.code, floor = serverFloorFrom(error)?.retryAfterMs;
  const code = typeof raw === 'string' && /^[a-z][a-z0-9_.-]{0,63}$/.test(raw) ? raw : error instanceof TypeError ? 'network' : 'failed';
  return Object.freeze({code, retryable: describeActionError(error).retryable, ...(floor ? {retryAfterMs: floor} : {})});
}

const EFFECT_NAME: Partial<Record<SetupEffectKind, SetupEffectName>> = {
  start: 'start', 'initialize-hosted': 'initialize', 'initialize-local': 'initialize', 'request-approval': 'request-approval', 'approve-in-app': 'approve-in-app',
  abandon: 'abandon', 'retry-delivery': 'retry-delivery', exchange: 'exchange', renew: 'renew', 'adopt-owner': 'adopt-owner', 'add-library': 'add-library',
};
const failureOf = (effect: SetupEffect, error: SetupError): SetupInput =>
  effect.kind === 'read-state' ? {kind: 'readFailed', error} : {kind: 'effectFailed', effect: EFFECT_NAME[effect.kind]!, error};
const UNCONFIRMED: SetupError = Object.freeze({code: 'unconfirmed', retryable: true});

const deepFreeze = <T>(value: T): T => {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {Object.values(value).forEach(deepFreeze); Object.freeze(value);}
  return value;
};

/** The chain ended in a `failed` view. Thrown so the action reads `failed` and Try again can reuse its intentId. */
class SetupChainFailed extends Error {}

type Seed = SetupInput | 'read';
type Lane = {action: UserAction<void>; seed: Seed; inflight?: SetupEffect};

export function createSetupController(ports: SetupPorts, options: SetupControllerOptions = {}): SetupController {
  const {store} = ports;
  const scheduler = options.scheduler ?? systemScheduler, wallNow = options.wallNow ?? Date.now, random = options.random ?? Math.random;
  const life = new AbortController();
  let view: SetupView = initialSetupView, foreground = options.foreground !== false, disposed = false;
  let credentials: SetupCredentials | undefined, library: SetupLibrary | undefined;
  // The ids this controller last saw in the store: what a retire expects, so another tab's successor is never removed.
  let known: {requestId?: string; attemptId?: string} = {};
  let readWait: AbortController | undefined, readDueAt: number | undefined, readDeferred = false;
  const listeners = new Set<() => void>();

  const notify = () => {for (const listener of [...listeners]) releaseQuietly('setup listener', listener);};
  const setView = (next: SetupView) => {
    if (next === view || JSON.stringify(next) === JSON.stringify(view)) return;
    view = deepFreeze(next);
    notify();
  };
  /** One reducer step. Credentials live only while a start or its Try again can still need them. */
  const apply = (input: SetupInput): readonly SetupEffect[] => {
    const step = stepSetup(view, input, wallNow());
    setView(step.view);
    if (view.kind !== 'starting' && view.kind !== 'failed') credentials = undefined;
    if (view.kind !== 'library' && view.kind !== 'failed') library = undefined;
    return step.effects;
  };

  // ---- the read timer: one pending read, held while backgrounded, never early ----------------------------------------------
  const cancelRead = () => {
    readWait?.abort();
    readWait = readDueAt = undefined;
  };
  const armRead = () => {
    if (disposed || !foreground || readDueAt === undefined || readWait) return;
    const mine = readWait = new AbortController();
    wait(Math.max(0, readDueAt - scheduler.now()), {signal: mine.signal, scheduler}).then(() => {
      if (readWait !== mine) return;
      readWait = readDueAt = undefined;
      fireRead();
    }, () => {});
  };
  const scheduleRead = (afterMs: number) => {
    cancelRead();
    const base = Number.isFinite(afterMs) && afterMs >= 0 ? afterMs : 0;
    readDueAt = scheduler.now() + nextDelay({baseMs: base, maxMs: base, jitter: 'none'}, 1, {retryAfterMs: base}, random);
    armRead();
  };
  /** The effects a step can have outside a chain (a failure fed in by the watchdog, a direct intent, dispose). */
  const runSync = (effects: readonly SetupEffect[]) => {
    for (const effect of effects) {
      if (effect.kind === 'schedule-read') scheduleRead(effect.afterMs);
      else if (effect.kind === 'cancel-pending') cancelRead();
    }
  };

  // ---- the store: re-read after the ports write; retire by compare-and-swap ------------------------------------------------
  const remember = (local: SetupLocal) => {known = {requestId: local.record?.requestId, attemptId: local.attempt?.attemptId};};
  const refreshKnown = () => releaseQuietlyAsync('setup store read', async () => remember(await store.read()));
  const retire = async (slot: 'record' | 'attempt') => {
    const ok = slot === 'record' ? await store.change({requestId: known.requestId}, {record: null}) : await store.change({attemptId: known.attemptId}, {attempt: null});
    if (!ok) await refreshKnown();   // a successor is there: it stays, and is what we know now
    else known = slot === 'record' ? {...known, requestId: undefined} : {...known, attemptId: undefined};
  };
  const retireQuietly = (slot: 'record' | 'attempt') => releaseQuietlyAsync(`setup retire ${slot}`, () => retire(slot));

  // ---- the effects ---------------------------------------------------------------------------------------------------------
  async function readServer(context: SetupPortContext): Promise<SetupInput | undefined> {
    const {server: result, local} = await ports.readState(context);
    if (context.signal.aborted) return undefined;
    remember(local);
    if (local.unreadable) releaseQuietly('setup diagnostic', () => options.diagnostic?.('setup_record_unreadable'));
    if (result.kind !== 'state') {
      if (local.unreadable) {await retireQuietly('record'); await retireQuietly('attempt');}
      return result;
    }
    const verdict = reconcileSetup(local, result.state, wallNow());
    if (verdict === 'retire') {await retireQuietly('record'); await retireQuietly('attempt');}
    else if (verdict === 'retire-attempt') await retireQuietly('attempt');
    const {record} = local, setup = result.state.setup;
    return {kind: 'state', state: result.state, own: !!record && typeof setup === 'object' && setup.requestId === record.requestId,
      hasAttempt: !!local.attempt && verdict === 'keep', ...(result.ownerSessionHeld !== undefined ? {ownerSessionHeld: result.ownerSessionHeld} : {}), ...(record ? {name: record.name} : {})};
  }
  /** The port call may have written a record or an attempt, so the ids are read again before anything retires them. */
  const written = async <T>(call: Promise<T>): Promise<T> => {
    const result = await call;
    await refreshKnown();
    return result;
  };

  async function call(effect: SetupEffect, context: SetupPortContext): Promise<SetupInput | undefined> {
    switch (effect.kind) {
      case 'read-state': return readServer(context);
      case 'start': return written(ports.start(context));
      case 'initialize-hosted': return written(ports.initializeHosted(effect.name, context));
      case 'initialize-local': {
        if (!credentials) return failureOf(effect, {code: 'credentials_missing', retryable: false});
        const result = await written(ports.initializeLocal(effect.name, credentials, context));
        credentials = undefined;
        return result;
      }
      case 'request-approval': return written(ports.requestApproval(effect.navigate, context));
      case 'approve-in-app': return ports.approveInApp(context);
      case 'abandon': {
        const result = await ports.abandon(context);
        if (result.kind !== 'abandoned') return result;
        if (!context.signal.aborted) {await retireQuietly('record'); await retireQuietly('attempt');}
        return undefined;
      }
      case 'retry-delivery': return ports.retryDelivery(context);
      case 'exchange': return ports.exchange(context);
      case 'renew': return written(ports.renew(context));
      case 'adopt-owner': return ports.adoptOwner(context);
      case 'add-library': {
        if (!library) return failureOf(effect, {code: 'library_missing', retryable: false});
        const result = await ports.addLibrary(library, context);
        library = undefined;
        return result;
      }
      default: return undefined;
    }
  }
  async function perform(lane: Lane, effect: SetupEffect, context: SetupPortContext): Promise<SetupInput | undefined> {
    switch (effect.kind) {
      case 'schedule-read': scheduleRead(effect.afterMs); return undefined;
      case 'cancel-pending': cancelRead(); return undefined;
      case 'navigate': releaseQuietly('setup navigate', () => ports.navigate(effect.url)); return undefined;
      case 'retire-record': await retireQuietly('record'); return undefined;
      case 'retire-attempt': await retireQuietly('attempt'); return undefined;
    }
    lane.inflight = effect;
    try {
      return await call(effect, context);
    } catch (error) {
      return context.signal.aborted ? undefined : failureOf(effect, setupErrorOf(error));
    } finally {
      if (lane.inflight === effect) lane.inflight = undefined;
    }
  }

  /** Runs one seed to rest: step it, perform its effects, feed each outcome back, until nothing is in flight. */
  async function drive(lane: Lane, context: SetupPortContext): Promise<void> {
    const {seed} = lane;
    let inputs: SetupInput[] = seed === 'read' ? [] : [seed], effects: SetupEffect[] = seed === 'read' ? [{kind: 'read-state'}] : [];
    while (inputs.length || effects.length) {
      for (const input of inputs) {
        if (context.signal.aborted) return;
        effects.push(...apply(input));
      }
      inputs = [];
      for (const effect of effects) {
        if (context.signal.aborted) return;
        const next = await perform(lane, effect, context);
        if (context.signal.aborted) return;
        if (next) inputs.push(next);
      }
      effects = [];
    }
    if (view.kind === 'failed') throw new SetupChainFailed();
  }

  /** The watchdog gave up: the effect in flight may or may not have happened, so it becomes the retryable failure; Try again keeps the intentId. */
  const unconfirmed = (lane: Lane) => {
    const {inflight} = lane;
    lane.inflight = undefined;
    if (inflight && !disposed) runSync(apply(failureOf(inflight, UNCONFIRMED)));
  };
  const makeLane = (): Lane => {
    const lane: Lane = {seed: 'read', action: undefined as unknown as UserAction<void>};
    lane.action = createUserAction<void>({
      run: context => drive(lane, {signal: context.signal, intentId: context.intentId}),
      scheduler, binding: life, newIntentId: options.newIntentId ?? randomId,
      slowAfterMs: options.slowAfterMs, watchdogMs: options.watchdogMs ?? USER_ACTION_WATCHDOG_MS,
    });
    lane.action.subscribe(() => {
      const {phase} = lane.action.getSnapshot();
      if (lane === user) notify();
      if (phase === 'unconfirmed') unconfirmed(lane);
      if (lane === system && phase !== 'working' && readDeferred) {readDeferred = false; fireRead();}
    });
    return lane;
  };
  const user = makeLane(), system = makeLane();
  const working = (lane: Lane) => lane.action.getSnapshot().phase === 'working';

  function fireRead() {
    if (disposed || !foreground) return;
    if (working(system)) {readDeferred = true; return;}
    system.seed = 'read';
    system.action.start();
  }
  const intent = (input: SetupInput) => {
    if (disposed || working(user)) return;
    user.seed = input;
    user.action.start();
  };
  const direct = (input: SetupInput) => {
    if (!disposed) runSync(apply(input));
  };

  const controller: SetupController = {
    getSnapshot: () => view,
    busy: () => !disposed && working(user),
    subscribe(listener: () => void) {
      if (disposed) return () => {};
      listeners.add(listener);
      return () => {listeners.delete(listener);};
    },
    start() {
      if (disposed || working(system)) return;
      system.seed = {kind: 'start'};
      system.action.start();
    },
    submitHosted(name, {navigate}) {intent({kind: 'submitHosted', name, navigate});},
    submitLocal(name, username, password) {
      if (disposed || working(user)) return;
      credentials = Object.freeze({username, password});
      intent({kind: 'submitLocal', name});
    },
    chooseKind: mode => direct({kind: 'chooseKind', mode}),
    back() {
      // Back while Continue is in flight drops that call, when the reducer takes Back there; the server decides any race.
      if (!disposed && working(user) && view.kind === 'starting' && stepSetup(view, {kind: 'back'}, wallNow()).view !== view) user.action.cancel();
      intent({kind: 'back'});
    },
    startAgain: () => intent({kind: 'startAgain'}),
    retry() {
      if (disposed || working(user)) return;
      const {phase} = user.action.getSnapshot();
      if (view.kind === 'failed' && (phase === 'failed' || phase === 'unconfirmed')) {
        user.seed = {kind: 'retry'};
        user.action.retry();
      } else intent({kind: 'retry'});
    },
    approveInApp: () => intent({kind: 'approveInApp'}),
    addLibrary(input) {
      if (disposed || working(user)) return;
      library = Object.freeze({kind: input.kind, name: input.name, path: input.path, ...(input.folders ? {folders: Object.freeze([...input.folders])} : {})});
      intent({kind: 'addLibrary'});
    },
    skip: () => direct({kind: 'skip'}),
    setForeground(next) {
      if (disposed || foreground === next) return;
      foreground = next;
      if (!next) {readWait?.abort(); readWait = undefined; return;}   // the due time stays: resuming never reads early
      armRead();
      if (readDeferred) fireRead();
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      releaseAll('setup controller', [
        () => runSync(stepSetup(view, {kind: 'dispose'}, wallNow()).effects),
        () => cancelRead(),
        () => user.action.dispose(),
        () => system.action.dispose(),
        () => life.abort(),
        () => {credentials = library = undefined; listeners.clear();},
      ]);
    },
  };
  return Object.freeze(controller);
}
