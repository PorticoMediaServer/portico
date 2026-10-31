import type {SetupReading} from './setup-store.ts';

/** The first-run setup controller as a pure reducer (B-DESIGN §6.1 and §6.2). No I/O, no timers, no clock, no randomness:
 * `stepSetup(view, input, now)` returns the next view and the effects a runner must perform. Effects and failure reasons
 * are data, never copy. The runner feeds every effect's outcome back in as an input.
 *
 * Runner contract:
 * - `read-state` is "pair if needed, then GET /v1/setup/state with the record's token"; its answer is a `state` input
 *   (with `own`, from comparing the saved requestId), or `readFailed`, `tokenRefused`, `notLocalSource`, `alreadyComplete`.
 * - `schedule-read` waits `afterMs` (plus any jitter, added on top, never below) while foregrounded, then does a `read-state`.
 *   It replaces any read already scheduled. A step that emits none leaves the pending one alone; `cancel-pending` clears it.
 * - `abandon` is S8. When the server accepts it the runner retires the record and the attempt (compare-and-swap on the requestId).
 * - There is no input or effect that moves a setup from one kind to the other: only the ordinary Back, through `form`. */

export type SetupKind = 'hosted' | 'local';
/** `choose` offers both kinds; `hosted` and `local` are the two forms. Arriving in `form` always shows `choose`. */
export type SetupFormMode = 'choose' | SetupKind;
export type SetupRefusal = 'not_local_source' | 'address_not_allowed' | 'already_set_up';
export type SetupOutcome = 'denied' | 'expired' | 'superseded' | 'cancelled' | 'approval_lapsed';
export type SetupError = Readonly<{code: string; retryable: boolean; retryAfterMs?: number}>;
/** How far the server has gone with this person's attempt, which decides what Back may do:
 * `none` nothing to abandon; `unapproved` Back sends S8 abandon; `committed` the server won't take it, so Back shows its phase. */
export type SetupHold = 'none' | 'unapproved' | 'committed';

export type SetupView =
  | Readonly<{kind: 'connecting'}>
  | Readonly<{kind: 'refused'; reason: SetupRefusal}>
  | Readonly<{kind: 'busy_elsewhere'; retryAt?: number; polls: number}>
  | Readonly<{kind: 'form'; mode: SetupFormMode; name: string; earlierExpired?: true}>
  | Readonly<{kind: 'starting'; mode: SetupKind; name: string; navigate: boolean; step: 'start' | 'initialize' | 'approval' | 'adopt'}>
  | Readonly<{kind: 'awaiting_approval'; name: string; serverPhase: 'started' | 'awaiting_approval'; approvalUrl?: string; expiresAt?: string; polls: number;
      /** The Portico Account declined the last request: one line says so; Continue and Back are as ever. */ declined?: true}>
  | Readonly<{kind: 'finishing'; slow: boolean; polls: number}>
  | Readonly<{kind: 'exhausted'; polls: number}>
  | Readonly<{kind: 'admitting'}>
  | Readonly<{kind: 'renewing'}>
  | Readonly<{kind: 'library'}>
  | Readonly<{kind: 'done'}>
  | Readonly<{kind: 'ended'; outcome: SetupOutcome; name: string}>
  | Readonly<{kind: 'failed'; error: SetupError; retryable: boolean; name: string; hold: SetupHold; retry: Readonly<{to: SetupView; effects: readonly SetupEffect[]}>}>
  | Readonly<{kind: 'unknown'}>;
export type SetupViewKind = SetupView['kind'];

export type SetupEffect =
  | Readonly<{kind: 'read-state'}>
  | Readonly<{kind: 'schedule-read'; afterMs: number}>
  | Readonly<{kind: 'start'}>
  | Readonly<{kind: 'initialize-hosted'; name: string}>
  | Readonly<{kind: 'initialize-local'; name: string}>
  | Readonly<{kind: 'request-approval'; navigate: boolean}>
  | Readonly<{kind: 'approve-in-app'}>
  | Readonly<{kind: 'abandon'}>
  | Readonly<{kind: 'retry-delivery'}>
  | Readonly<{kind: 'exchange'}>
  | Readonly<{kind: 'renew'}>
  | Readonly<{kind: 'adopt-owner'}>
  | Readonly<{kind: 'add-library'}>
  | Readonly<{kind: 'retire-record'}>
  | Readonly<{kind: 'retire-attempt'}>
  | Readonly<{kind: 'navigate'; url: string}>
  | Readonly<{kind: 'cancel-pending'}>;
export type SetupEffectKind = SetupEffect['kind'];
/** Effects whose failure comes back as `effectFailed`. */
export type SetupEffectName = 'start' | 'initialize' | 'request-approval' | 'approve-in-app' | 'abandon' | 'retry-delivery' | 'exchange' | 'renew' | 'adopt-owner' | 'add-library';

export type SetupInput =
  // intents
  | Readonly<{kind: 'start'}>
  | Readonly<{kind: 'submitHosted'; name: string; navigate: boolean}>
  | Readonly<{kind: 'submitLocal'; name: string}>
  | Readonly<{kind: 'chooseKind'; mode: SetupFormMode}>
  | Readonly<{kind: 'back'}>
  | Readonly<{kind: 'startAgain'}>
  | Readonly<{kind: 'retry'}>
  | Readonly<{kind: 'approveInApp'}>
  | Readonly<{kind: 'addLibrary'}>
  | Readonly<{kind: 'skip'}>
  | Readonly<{kind: 'dispose'}>
  // server readings. `own`: the saved record's requestId is the one the server answered for.
  // `ownerSessionHeld`: an owner session for this server is already held. `hasAttempt`: this tab or app holds the live attempt (default true).
  // `name`: the saved record's name, so the form keeps it.
  | Readonly<{kind: 'state'; state: SetupReading; own: boolean; ownerSessionHeld?: boolean; hasAttempt?: boolean; name?: string}>
  | Readonly<{kind: 'readFailed'; error: SetupError}>
  | Readonly<{kind: 'tokenRefused'}>
  | Readonly<{kind: 'notLocalSource'}>
  | Readonly<{kind: 'addressNotAllowed'}>
  | Readonly<{kind: 'alreadyComplete'}>
  | Readonly<{kind: 'setupInProgress'; retryAfterSeconds?: number}>
  // effect results
  | Readonly<{kind: 'startDone'}>
  | Readonly<{kind: 'initializeDone'}>
  | Readonly<{kind: 'approvalReady'; approvalUrl: string; expiresAt?: string}>
  | Readonly<{kind: 'approvedInApp'}>
  | Readonly<{kind: 'abandonRefused'}>
  | Readonly<{kind: 'deliveryRetried'}>
  | Readonly<{kind: 'exchangeDone'}>
  | Readonly<{kind: 'exchangeRefused'; lapsed: boolean; error: SetupError}>
  | Readonly<{kind: 'ownerAdopted'}>
  | Readonly<{kind: 'renewDone'}>
  | Readonly<{kind: 'libraryAdded'}>
  | Readonly<{kind: 'effectFailed'; effect: SetupEffectName; error: SetupError}>;
export type SetupInputKind = SetupInput['kind'];

export type SetupStep = Readonly<{view: SetupView; effects: readonly SetupEffect[]}>;

/** Reads while waiting: 2 s, doubling to 15 s. A server floor (Retry-After, in ms) is the later of the two and is never shortened or capped. */
export const SETUP_READ_FLOOR_MS = 2000;
export const SETUP_READ_CEILING_MS = 15_000;
export function setupReadDelay(polls: number, serverFloorMs?: number): number {
  const step = Number.isFinite(polls) ? Math.min(8, Math.max(0, Math.floor(polls))) : 0;
  const ours = Math.min(SETUP_READ_CEILING_MS, SETUP_READ_FLOOR_MS * 2 ** step);
  const floor = typeof serverFloorMs === 'number' && Number.isFinite(serverFloorMs) && serverFloorMs > 0 ? Math.ceil(serverFloorMs) : 0;
  return Math.max(ours, floor);
}

export const initialSetupView: SetupView = Object.freeze({kind: 'connecting'});

const RETIRE: readonly SetupEffect[] = [{kind: 'retire-record'}, {kind: 'retire-attempt'}];
const go = (view: SetupView, effects: readonly SetupEffect[] = []): SetupStep => ({view, effects});
const stay = (view: SetupView): SetupStep => ({view, effects: []});
const readAfter = (polls: number, floorMs?: number): SetupEffect => ({kind: 'schedule-read', afterMs: setupReadDelay(polls, floorMs)});
const form = (name: string, earlierExpired?: true): SetupView => ({kind: 'form', mode: 'choose', name, ...(earlierExpired ? {earlierExpired} : {})});
const ended = (outcome: SetupOutcome, name: string): SetupView => ({kind: 'ended', outcome, name});
const superseded = (name: string): SetupStep => go(ended('superseded', name), RETIRE);
const alreadySetUp = (): SetupStep => go({kind: 'refused', reason: 'already_set_up'}, RETIRE);

type StateInput = Extract<SetupInput, {kind: 'state'}>;
type OwnPhase = Exclude<SetupReading['setup'], 'unknown' | undefined>;
const ownOf = (i: StateInput): OwnPhase | undefined => (i.own && typeof i.state.setup === 'object' ? i.state.setup : undefined);
const floorOf = (r: SetupReading) => (typeof r.retryAfterSeconds === 'number' && r.retryAfterSeconds > 0 ? r.retryAfterSeconds * 1000 : undefined);
const waiting = (name: string, own: OwnPhase, polls: number): SetupView => ({kind: 'awaiting_approval', name, serverPhase: own.phase === 'awaiting_approval' ? 'awaiting_approval' : 'started', polls,
  ...(own.expiresAt ? {expiresAt: own.expiresAt} : {}), ...(own.phase === 'started' && own.outcome === 'denied' ? {declined: true as const} : {})});

/** The `reconcile` rows of §6.2: what a server reading means when nothing more specific applies. */
function reconcile(i: StateInput, now: number, from?: SetupView): SetupStep {
  const r = i.state, name = i.name ?? '';
  if (r.status === 'unknown' || r.setup === 'unknown') return go({kind: 'unknown'});
  if (r.status === 'complete') return i.ownerSessionHeld ? go({kind: 'library'}) : alreadySetUp();
  const own = ownOf(i);
  if (!own) {
    if (r.status !== 'in_progress') return go(form(name), RETIRE);
    const floor = floorOf(r), polls = from?.kind === 'busy_elsewhere' ? from.polls + 1 : 0;
    return go({kind: 'busy_elsewhere', polls, ...(floor ? {retryAt: now + floor} : {})}, [readAfter(polls, floor)]);
  }
  switch (own.phase) {
    case 'expired': return go(form(name, true), RETIRE);
    case 'started': case 'awaiting_approval': return go(waiting(name, own, 0), [readAfter(0)]);
    case 'approved': case 'finishing': return go({kind: 'finishing', slow: own.delivery?.slow === true, polls: 0}, [readAfter(0)]);
    case 'exhausted': return go({kind: 'exhausted', polls: 0}, [readAfter(0)]);
    case 'installed': return i.hasAttempt === false ? go({kind: 'busy_elsewhere', polls: 0}, [readAfter(0)]) : go({kind: 'admitting'}, [{kind: 'exchange'}]);
    case 'installed_lapsed': return go({kind: 'renewing'}, [{kind: 'renew'}]);
  }
}

const failedStep = (error: SetupError, name: string, hold: SetupHold, to: SetupView, effects: readonly SetupEffect[]): SetupStep =>
  go({kind: 'failed', error, retryable: error.retryable, name, hold, retry: {to, effects}});

/** Back and Start again. An own unapproved attempt is abandoned (S8) and the form shows both kinds; an approved one is never abandoned. */
function leave(hold: SetupHold, name: string): SetupStep {
  if (hold === 'committed') return go({kind: 'connecting'}, [{kind: 'read-state'}]);
  return go(form(name), [{kind: 'cancel-pending'}, ...(hold === 'unapproved' ? [{kind: 'abandon'} as const] : [])]);
}

function connecting(view: SetupView, i: SetupInput, now: number): SetupStep {
  switch (i.kind) {
    case 'start': return go(view, [{kind: 'read-state'}]);
    case 'state': return reconcile(i, now);
    case 'readFailed': return failedStep(i.error, '', 'none', view, [{kind: 'read-state'}]);
    case 'tokenRefused': return go(form(''), RETIRE);
    case 'notLocalSource': return go({kind: 'refused', reason: 'not_local_source'});
    case 'alreadyComplete': return alreadySetUp();
    default: return stay(view);
  }
}

function form_(view: Extract<SetupView, {kind: 'form'}>, i: SetupInput): SetupStep {
  switch (i.kind) {
    case 'chooseKind': return go({...view, mode: i.mode});
    case 'submitHosted': return go({kind: 'starting', mode: 'hosted', name: i.name, navigate: i.navigate, step: 'start'}, [{kind: 'start'}]);
    case 'submitLocal': return go({kind: 'starting', mode: 'local', name: i.name, navigate: false, step: 'start'}, [{kind: 'start'}]);
    case 'abandonRefused': return go({kind: 'connecting'}, [{kind: 'read-state'}]);
    case 'effectFailed': return i.effect === 'abandon' ? failedStep(i.error, view.name, 'unapproved', view, [{kind: 'abandon'}]) : stay(view);
    default: return stay(view);
  }
}

function starting(view: Extract<SetupView, {kind: 'starting'}>, i: SetupInput, now: number): SetupStep {
  const {mode, name, step} = view;
  const hosted = mode === 'hosted';
  switch (i.kind) {
    case 'alreadyComplete': return alreadySetUp();
    case 'tokenRefused': return step === 'initialize' && hosted || step === 'approval' ? superseded(name) : stay(view);
    case 'notLocalSource': return step === 'start' ? go({kind: 'refused', reason: 'not_local_source'}) : stay(view);
    case 'addressNotAllowed': return step === 'start' && hosted ? go({kind: 'refused', reason: 'address_not_allowed'}) : stay(view);
    case 'setupInProgress': {
      if (step !== 'start') return stay(view);
      const floor = typeof i.retryAfterSeconds === 'number' && i.retryAfterSeconds > 0 ? i.retryAfterSeconds * 1000 : undefined;
      return go({kind: 'busy_elsewhere', polls: 0, ...(floor ? {retryAt: now + floor} : {})}, [readAfter(0, floor)]);
    }
    case 'startDone':
      if (step !== 'start') return stay(view);
      return go({...view, step: 'initialize'}, [hosted ? {kind: 'initialize-hosted', name} : {kind: 'initialize-local', name}]);
    case 'initializeDone':
      if (step !== 'initialize') return stay(view);
      return hosted ? go({...view, step: 'approval'}, [{kind: 'request-approval', navigate: view.navigate}]) : go({...view, step: 'adopt'}, [{kind: 'adopt-owner'}]);
    case 'approvalReady': {
      if (step !== 'approval') return stay(view);
      const next: SetupView = {kind: 'awaiting_approval', name, serverPhase: 'awaiting_approval', polls: 0, approvalUrl: i.approvalUrl, ...(i.expiresAt ? {expiresAt: i.expiresAt} : {})};
      return go(next, [...(view.navigate ? [{kind: 'navigate', url: i.approvalUrl} as const] : []), readAfter(0)]);
    }
    case 'ownerAdopted': return step === 'adopt' ? go({kind: 'library'}, RETIRE) : stay(view);
    // Back while Continue is in flight: the runner drops the call, and anything the server may already hold is abandoned (it decides
    // the race). A local owner being created can't be taken back, so that runs on.
    case 'back': return step === 'start' ? leave('none', name) : hosted ? leave('unapproved', name) : stay(view);
    case 'effectFailed': {
      if (step === 'start' && i.effect === 'start') return failedStep(i.error, name, 'none', view, [{kind: 'start'}]);
      if (step === 'initialize' && i.effect === 'initialize') return failedStep(i.error, name, hosted ? 'unapproved' : 'none', view, [hosted ? {kind: 'initialize-hosted', name} : {kind: 'initialize-local', name}]);
      if (step === 'approval' && i.effect === 'request-approval') return failedStep(i.error, name, 'unapproved', view, [{kind: 'request-approval', navigate: view.navigate}]);
      if (step === 'adopt' && i.effect === 'adopt-owner') return failedStep(i.error, name, 'committed', view, [{kind: 'adopt-owner'}]);
      return stay(view);
    }
    default: return stay(view);
  }
}

function awaitingApproval(view: Extract<SetupView, {kind: 'awaiting_approval'}>, i: SetupInput, now: number): SetupStep {
  switch (i.kind) {
    case 'state': {
      const r = i.state;
      if (r.status === 'unknown' || r.setup === 'unknown' || r.status === 'complete') return reconcile(i, now);
      const own = ownOf(i);
      if (!own) return superseded(view.name);
      if (own.phase === 'expired') return go(ended('expired', view.name));
      // The server says the Portico Account declined: the same page, with one line saying so.
      if (own.phase === 'started' && own.outcome === 'denied') return go(waiting(view.name, own, view.polls + 1), [readAfter(view.polls + 1, floorOf(r))]);
      if (own.phase === 'started' && view.serverPhase === 'awaiting_approval') return go(ended('denied', view.name));
      if (own.phase === 'started' || own.phase === 'awaiting_approval') {
        const polls = view.polls + 1;
        const {declined: _earlier, ...rest} = view;
        return go({...rest, serverPhase: own.phase, polls, ...(own.expiresAt ? {expiresAt: own.expiresAt} : {})}, [readAfter(polls, floorOf(r))]);
      }
      return reconcile(i, now);
    }
    case 'readFailed': return go({...view, polls: view.polls + 1}, [readAfter(view.polls + 1, i.error.retryAfterMs)]);
    case 'tokenRefused': return superseded(view.name);
    case 'alreadyComplete': return alreadySetUp();
    case 'back': return leave('unapproved', view.name);
    case 'retry': return go({kind: 'starting', mode: 'hosted', name: view.name, navigate: true, step: 'approval'}, [{kind: 'request-approval', navigate: true}]);
    case 'approveInApp': return go(view, [{kind: 'approve-in-app'}]);
    case 'approvedInApp': return go({kind: 'finishing', slow: false, polls: 0}, [readAfter(0)]);
    case 'effectFailed': return i.effect === 'approve-in-app' ? failedStep(i.error, view.name, 'unapproved', view, [{kind: 'approve-in-app'}]) : stay(view);
    default: return stay(view);
  }
}

/** `finishing` and `exhausted`: the server has approved, so the attempt is committed. */
function delivering(view: Extract<SetupView, {kind: 'finishing' | 'exhausted'}>, i: SetupInput, now: number): SetupStep {
  switch (i.kind) {
    case 'state': {
      const r = i.state;
      if (r.status === 'unknown' || r.setup === 'unknown' || r.status === 'complete') return reconcile(i, now);
      const own = ownOf(i);
      if (!own) return superseded(i.name ?? '');
      switch (own.phase) {
        case 'expired': return go(ended('approval_lapsed', i.name ?? ''));
        case 'approved': case 'finishing': {
          const slow = own.delivery?.slow === true, polls = view.kind === 'finishing' ? view.polls + 1 : 0;
          return go({kind: 'finishing', slow, polls}, [readAfter(polls, floorOf(r))]);
        }
        case 'exhausted': {
          const polls = view.kind === 'exhausted' ? view.polls + 1 : 0;
          return go({kind: 'exhausted', polls}, [readAfter(polls, floorOf(r))]);
        }
        default: return reconcile(i, now);
      }
    }
    case 'readFailed': return go({...view, polls: view.polls + 1}, [readAfter(view.polls + 1, i.error.retryAfterMs)]);
    case 'tokenRefused': return superseded('');
    case 'alreadyComplete': return alreadySetUp();
    case 'back': return go(view, [{kind: 'read-state'}]);
    case 'retry': return view.kind === 'exhausted' ? go(view, [{kind: 'retry-delivery'}]) : stay(view);
    case 'deliveryRetried': return view.kind === 'exhausted' ? go({kind: 'finishing', slow: true, polls: 0}, [readAfter(0)]) : stay(view);
    case 'effectFailed': return view.kind === 'exhausted' && i.effect === 'retry-delivery' ? failedStep(i.error, '', 'committed', view, [{kind: 'retry-delivery'}]) : stay(view);
    default: return stay(view);
  }
}

function busy(view: Extract<SetupView, {kind: 'busy_elsewhere'}>, i: SetupInput, now: number): SetupStep {
  switch (i.kind) {
    case 'state': return reconcile(i, now, view);
    case 'readFailed': return go({...view, polls: view.polls + 1}, [readAfter(view.polls + 1, i.error.retryAfterMs)]);
    case 'alreadyComplete': return alreadySetUp();
    case 'back': return leave('none', '');
    default: return stay(view);
  }
}

function admitting(view: Extract<SetupView, {kind: 'admitting'}>, i: SetupInput): SetupStep {
  switch (i.kind) {
    case 'exchangeDone': return go(view, [{kind: 'adopt-owner'}]);
    case 'exchangeRefused': return i.lapsed ? go({kind: 'renewing'}, [{kind: 'renew'}]) : failedStep(i.error, '', 'committed', view, [{kind: 'exchange'}]);
    case 'ownerAdopted': return go({kind: 'library'}, RETIRE);
    case 'tokenRefused': return superseded('');
    case 'effectFailed':
      if (i.effect === 'exchange') return failedStep(i.error, '', 'committed', view, [{kind: 'exchange'}]);
      if (i.effect === 'adopt-owner') return failedStep(i.error, '', 'committed', view, [{kind: 'adopt-owner'}]);
      return stay(view);
    default: return stay(view);
  }
}

function renewing(view: Extract<SetupView, {kind: 'renewing'}>, i: SetupInput): SetupStep {
  switch (i.kind) {
    case 'renewDone': return go({kind: 'admitting'}, [{kind: 'exchange'}]);
    case 'tokenRefused': return superseded('');
    case 'effectFailed': return i.effect === 'renew' ? failedStep(i.error, '', 'committed', view, [{kind: 'renew'}]) : stay(view);
    default: return stay(view);
  }
}

function failed(view: Extract<SetupView, {kind: 'failed'}>, i: SetupInput): SetupStep {
  switch (i.kind) {
    case 'retry': return view.retryable ? go(view.retry.to, view.retry.effects) : stay(view);
    case 'back': return leave(view.hold, view.name);
    default: return stay(view);
  }
}

/** One step of the setup controller. Pure: the same view, input and `now` always give the same answer.
 * Every (view, input) pair not defined by §6.2 leaves the view as it is, with no effects. */
export function stepSetup(view: SetupView, input: SetupInput, now: number): SetupStep {
  if (input.kind === 'dispose') return go(view, [{kind: 'cancel-pending'}]);
  switch (view.kind) {
    case 'connecting': return connecting(view, input, now);
    case 'form': return form_(view, input);
    case 'starting': return starting(view, input, now);
    case 'awaiting_approval': return awaitingApproval(view, input, now);
    case 'finishing': case 'exhausted': return delivering(view, input, now);
    case 'busy_elsewhere': return busy(view, input, now);
    case 'admitting': return admitting(view, input);
    case 'renewing': return renewing(view, input);
    case 'failed': return failed(view, input);
    case 'ended':
      return input.kind === 'back' || input.kind === 'startAgain' ? leave(view.outcome === 'superseded' ? 'none' : 'unapproved', view.name) : stay(view);
    case 'unknown': return input.kind === 'retry' ? go({kind: 'connecting'}, [{kind: 'read-state'}]) : stay(view);
    case 'library':
      if (input.kind === 'addLibrary') return go(view, [{kind: 'add-library'}]);
      if (input.kind === 'effectFailed') return input.effect === 'add-library' ? failedStep(input.error, '', 'committed', view, [{kind: 'add-library'}]) : stay(view);
      if (input.kind === 'libraryAdded' || input.kind === 'skip') return go({kind: 'done'});
      return stay(view);
    case 'refused': case 'done': return stay(view);
  }
}
