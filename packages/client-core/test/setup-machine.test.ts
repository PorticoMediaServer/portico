import test from 'node:test';
import assert from 'node:assert/strict';
import {initialSetupView, setupReadDelay, stepSetup, SETUP_READ_CEILING_MS, SETUP_READ_FLOOR_MS,
  type SetupEffect, type SetupError, type SetupInput, type SetupView} from '../src/setup-machine.ts';
import type {SetupReading} from '../src/setup-store.ts';

const NOW = 1_000_000;
const err = (retryable = true, retryAfterMs?: number): SetupError => ({code: 'x', retryable, ...(retryAfterMs ? {retryAfterMs} : {})});

// ---- sample readings and inputs -------------------------------------------------------------------------------------------
type Phase = 'started' | 'awaiting_approval' | 'expired' | 'approved' | 'finishing' | 'exhausted' | 'installed' | 'installed_lapsed';
const reading = (status: SetupReading['status'], setup?: SetupReading['setup'], retryAfterSeconds?: number): SetupReading => ({status, ...(setup ? {setup} : {}), ...(retryAfterSeconds ? {retryAfterSeconds} : {})});
const phase = (p: Phase, expiresAt?: string): SetupReading => reading(p === 'expired' ? 'ready' : 'in_progress',
  {phase: p, requestId: 'r1', revision: '1', cancelling: false, renewable: false, ...(expiresAt ? {expiresAt} : {})});
const own = (p: Phase, extra: Partial<Extract<SetupInput, {kind: 'state'}>> = {}): SetupInput => ({kind: 'state', own: true, state: phase(p), name: 'Den', ...extra});

const INPUTS: Record<string, SetupInput> = {
  start: {kind: 'start'},
  submitHosted: {kind: 'submitHosted', name: 'Den', navigate: true},
  submitLocal: {kind: 'submitLocal', name: 'Den'},
  chooseKind: {kind: 'chooseKind', mode: 'local'},
  back: {kind: 'back'},
  startAgain: {kind: 'startAgain'},
  retry: {kind: 'retry'},
  approveInApp: {kind: 'approveInApp'},
  addLibrary: {kind: 'addLibrary'},
  skip: {kind: 'skip'},
  dispose: {kind: 'dispose'},
  'state.complete.held': {kind: 'state', own: false, state: reading('complete'), ownerSessionHeld: true},
  'state.complete': {kind: 'state', own: false, state: reading('complete')},
  'state.unknown-status': {kind: 'state', own: false, state: reading('unknown')},
  'state.unknown-phase': {kind: 'state', own: true, state: reading('in_progress', 'unknown')},
  'state.open': {kind: 'state', own: false, state: reading('ready'), name: 'Den'},
  'state.expired': own('expired'),
  'state.started': own('started'),
  'state.awaiting': own('awaiting_approval'),
  'state.approved': own('approved'),
  'state.finishing': own('finishing'),
  'state.exhausted': own('exhausted'),
  'state.installed': own('installed'),
  'state.installed-noattempt': own('installed', {hasAttempt: false}),
  'state.installed_lapsed': own('installed_lapsed'),
  'state.other': {kind: 'state', own: false, state: reading('in_progress', undefined, 30)},
  'state.other-untimed': {kind: 'state', own: false, state: reading('in_progress')},
  readFailed: {kind: 'readFailed', error: err()},
  tokenRefused: {kind: 'tokenRefused'},
  notLocalSource: {kind: 'notLocalSource'},
  addressNotAllowed: {kind: 'addressNotAllowed'},
  alreadyComplete: {kind: 'alreadyComplete'},
  setupInProgress: {kind: 'setupInProgress', retryAfterSeconds: 90},
  startDone: {kind: 'startDone'},
  initializeDone: {kind: 'initializeDone'},
  approvalReady: {kind: 'approvalReady', approvalUrl: 'https://hosted.example/claim#x', expiresAt: '2026-10-31T12:00:00Z'},
  approvedInApp: {kind: 'approvedInApp'},
  abandonRefused: {kind: 'abandonRefused'},
  deliveryRetried: {kind: 'deliveryRetried'},
  exchangeDone: {kind: 'exchangeDone'},
  'exchangeRefused.lapsed': {kind: 'exchangeRefused', lapsed: true, error: err()},
  'exchangeRefused.other': {kind: 'exchangeRefused', lapsed: false, error: err()},
  ownerAdopted: {kind: 'ownerAdopted'},
  renewDone: {kind: 'renewDone'},
  libraryAdded: {kind: 'libraryAdded'},
  'failed.start': {kind: 'effectFailed', effect: 'start', error: err()},
  'failed.initialize': {kind: 'effectFailed', effect: 'initialize', error: err()},
  'failed.request-approval': {kind: 'effectFailed', effect: 'request-approval', error: err()},
  'failed.approve-in-app': {kind: 'effectFailed', effect: 'approve-in-app', error: err()},
  'failed.abandon': {kind: 'effectFailed', effect: 'abandon', error: err()},
  'failed.retry-delivery': {kind: 'effectFailed', effect: 'retry-delivery', error: err()},
  'failed.exchange': {kind: 'effectFailed', effect: 'exchange', error: err()},
  'failed.renew': {kind: 'effectFailed', effect: 'renew', error: err()},
  'failed.adopt-owner': {kind: 'effectFailed', effect: 'adopt-owner', error: err()},
};

// ---- sample views, one per distinct behaviour ------------------------------------------------------------------------------
const hostedStarting = (step: 'start' | 'initialize' | 'approval', navigate = true): SetupView => ({kind: 'starting', mode: 'hosted', name: 'Den', navigate, step});
const localStarting = (step: 'start' | 'initialize' | 'adopt'): SetupView => ({kind: 'starting', mode: 'local', name: 'Den', navigate: false, step});
const failedView = (hold: 'none' | 'unapproved' | 'committed', retryable: boolean, to: SetupView, effects: SetupEffect[]): SetupView =>
  ({kind: 'failed', error: err(retryable), retryable, name: 'Den', hold, retry: {to, effects}});
const exhaustedView: SetupView = {kind: 'exhausted', polls: 1};
const admittingView: SetupView = {kind: 'admitting'};

const VIEWS: Record<string, SetupView> = {
  connecting: initialSetupView,
  refused: {kind: 'refused', reason: 'already_set_up'},
  'busy.timed': {kind: 'busy_elsewhere', retryAt: NOW + 30_000, polls: 1},
  'busy.untimed': {kind: 'busy_elsewhere', polls: 1},
  form: {kind: 'form', mode: 'hosted', name: 'Den'},
  'starting.hosted.start': hostedStarting('start'),
  'starting.hosted.initialize': hostedStarting('initialize'),
  'starting.hosted.approval': hostedStarting('approval'),
  'starting.hosted.approval-native': hostedStarting('approval', false),
  'starting.local.start': localStarting('start'),
  'starting.local.initialize': localStarting('initialize'),
  'starting.local.adopt': localStarting('adopt'),
  'awaiting.started': {kind: 'awaiting_approval', name: 'Den', serverPhase: 'started', polls: 1},
  'awaiting.live': {kind: 'awaiting_approval', name: 'Den', serverPhase: 'awaiting_approval', approvalUrl: 'https://hosted.example/claim#x', polls: 1},
  finishing: {kind: 'finishing', slow: false, polls: 1},
  exhausted: exhaustedView,
  admitting: admittingView,
  renewing: {kind: 'renewing'},
  library: {kind: 'library'},
  done: {kind: 'done'},
  'ended.denied': {kind: 'ended', outcome: 'denied', name: 'Den'},
  'ended.superseded': {kind: 'ended', outcome: 'superseded', name: 'Den'},
  'failed.none': failedView('none', true, initialSetupView, [{kind: 'read-state'}]),
  'failed.unapproved': failedView('unapproved', true, hostedStarting('initialize'), [{kind: 'initialize-hosted', name: 'Den'}]),
  'failed.committed': failedView('committed', true, admittingView, [{kind: 'exchange'}]),
  'failed.final': failedView('unapproved', false, hostedStarting('approval'), [{kind: 'request-approval', navigate: true}]),
  unknown: {kind: 'unknown'},
};

// ---- the allowed pairs, written from B-DESIGN §6.2 -------------------------------------------------------------------------
// 'next-kind | effect kinds in order'. '=' is the same kind as the view. Anything not listed must change nothing and do nothing.
const RETIRES = 'retire-record retire-attempt';
// What a server reading means to a view that holds no attempt (the `reconcile` rows).
const RECONCILE: Record<string, string> = {
  'state.complete.held': 'library|',
  'state.complete': `refused|${RETIRES}`,
  'state.unknown-status': 'unknown|',
  'state.unknown-phase': 'unknown|',
  'state.open': `form|${RETIRES}`,
  'state.expired': `form|${RETIRES}`,
  'state.started': 'awaiting_approval|schedule-read',
  'state.awaiting': 'awaiting_approval|schedule-read',
  'state.approved': 'finishing|schedule-read',
  'state.finishing': 'finishing|schedule-read',
  'state.exhausted': 'exhausted|schedule-read',
  'state.installed': 'admitting|exchange',
  'state.installed-noattempt': 'busy_elsewhere|schedule-read',
  'state.installed_lapsed': 'renewing|renew',
  'state.other': 'busy_elsewhere|schedule-read',
  'state.other-untimed': 'busy_elsewhere|schedule-read',
};
// What a view holding an attempt makes of a reading that no longer fits it.
const HELD_GONE: Record<string, string> = {
  'state.open': `ended|${RETIRES}`, 'state.other': `ended|${RETIRES}`, 'state.other-untimed': `ended|${RETIRES}`,
  'state.complete.held': 'library|', 'state.complete': `refused|${RETIRES}`, 'state.unknown-status': 'unknown|', 'state.unknown-phase': 'unknown|',
};
const AHEAD: Record<string, string> = {
  'state.approved': 'finishing|schedule-read', 'state.finishing': 'finishing|schedule-read', 'state.exhausted': 'exhausted|schedule-read',
  'state.installed': 'admitting|exchange', 'state.installed-noattempt': 'busy_elsewhere|schedule-read', 'state.installed_lapsed': 'renewing|renew',
};
const LIVE_POLLS = {readFailed: '=|schedule-read', tokenRefused: `ended|${RETIRES}`, alreadyComplete: `refused|${RETIRES}`};
const DELIVERY: Record<string, string> = {
  ...HELD_GONE, ...AHEAD, 'state.expired': 'ended|', 'state.started': 'awaiting_approval|schedule-read', 'state.awaiting': 'awaiting_approval|schedule-read',
  'state.approved': 'finishing|schedule-read', 'state.finishing': 'finishing|schedule-read', 'state.exhausted': 'exhausted|schedule-read',
  ...LIVE_POLLS, back: '=|read-state',
};
const AWAITING: Record<string, string> = {
  ...HELD_GONE, ...AHEAD, 'state.expired': 'ended|', 'state.started': '=|schedule-read', 'state.awaiting': '=|schedule-read', ...LIVE_POLLS,
  back: `form|abandon`, retry: 'starting|request-approval', approveInApp: '=|approve-in-app', approvedInApp: 'finishing|schedule-read', 'failed.approve-in-app': 'failed|',
};

const ALLOWED: Record<string, Record<string, string>> = {
  connecting: {...RECONCILE, start: '=|read-state', readFailed: 'failed|', tokenRefused: `form|${RETIRES}`, notLocalSource: 'refused|', alreadyComplete: `refused|${RETIRES}`},
  refused: {},
  'busy.timed': {...RECONCILE, readFailed: '=|schedule-read', alreadyComplete: `refused|${RETIRES}`, back: 'form|'},
  'busy.untimed': {...RECONCILE, readFailed: '=|schedule-read', alreadyComplete: `refused|${RETIRES}`, back: 'form|'},
  form: {chooseKind: '=|', submitHosted: 'starting|start', submitLocal: 'starting|start', abandonRefused: 'connecting|read-state', 'failed.abandon': 'failed|'},
  'starting.hosted.start': {startDone: '=|initialize-hosted', notLocalSource: 'refused|', addressNotAllowed: 'refused|', alreadyComplete: `refused|${RETIRES}`, setupInProgress: 'busy_elsewhere|schedule-read', 'failed.start': 'failed|'},
  'starting.hosted.initialize': {initializeDone: '=|request-approval', 'failed.initialize': 'failed|', tokenRefused: `ended|${RETIRES}`, alreadyComplete: `refused|${RETIRES}`},
  'starting.hosted.approval': {approvalReady: 'awaiting_approval|navigate schedule-read', 'failed.request-approval': 'failed|', tokenRefused: `ended|${RETIRES}`, alreadyComplete: `refused|${RETIRES}`},
  'starting.hosted.approval-native': {approvalReady: 'awaiting_approval|schedule-read', 'failed.request-approval': 'failed|', tokenRefused: `ended|${RETIRES}`, alreadyComplete: `refused|${RETIRES}`},
  'starting.local.start': {startDone: '=|initialize-local', notLocalSource: 'refused|', alreadyComplete: `refused|${RETIRES}`, setupInProgress: 'busy_elsewhere|schedule-read', 'failed.start': 'failed|'},
  'starting.local.initialize': {initializeDone: '=|adopt-owner', 'failed.initialize': 'failed|', alreadyComplete: `refused|${RETIRES}`},
  'starting.local.adopt': {ownerAdopted: `library|${RETIRES}`, 'failed.adopt-owner': 'failed|', alreadyComplete: `refused|${RETIRES}`},
  'awaiting.started': AWAITING,
  'awaiting.live': {...AWAITING, 'state.started': 'ended|'},
  finishing: {...DELIVERY},
  exhausted: {...DELIVERY, retry: '=|retry-delivery', deliveryRetried: 'finishing|schedule-read', 'failed.retry-delivery': 'failed|'},
  admitting: {exchangeDone: '=|adopt-owner', 'exchangeRefused.lapsed': 'renewing|renew', 'exchangeRefused.other': 'failed|', ownerAdopted: `library|${RETIRES}`, tokenRefused: `ended|${RETIRES}`, 'failed.exchange': 'failed|', 'failed.adopt-owner': 'failed|'},
  renewing: {renewDone: 'admitting|exchange', tokenRefused: `ended|${RETIRES}`, 'failed.renew': 'failed|'},
  library: {addLibrary: '=|add-library', libraryAdded: 'done|', skip: 'done|'},
  done: {},
  'ended.denied': {back: 'form|abandon', startAgain: 'form|abandon'},
  'ended.superseded': {back: 'form|', startAgain: 'form|'},
  'failed.none': {retry: 'connecting|read-state', back: 'form|'},
  'failed.unapproved': {retry: 'starting|initialize-hosted', back: 'form|abandon'},
  'failed.committed': {retry: 'admitting|exchange', back: 'connecting|read-state'},
  'failed.final': {back: 'form|abandon'},
  unknown: {retry: 'connecting|read-state'},
};

const deepFreeze = <T>(value: T): T => {
  if (value && typeof value === 'object') { Object.values(value).forEach(deepFreeze); Object.freeze(value); }
  return value;
};

test('golden table: every view × input pair is exactly the allowed list, and everything else changes nothing', () => {
  let allowed = 0, checked = 0;
  for (const [viewName, view] of Object.entries(VIEWS)) {
    for (const [inputName, input] of Object.entries(INPUTS)) {
      checked++;
      const label = `${viewName} × ${inputName}`;
      const expected = inputName === 'dispose' ? '=|cancel-pending' : ALLOWED[viewName]?.[inputName];
      const before = structuredClone(view);
      const out = stepSetup(deepFreeze(structuredClone(view)), input, NOW);
      assert.deepEqual(view, before, `${label}: the view must not be mutated`);
      if (expected === undefined) {
        assert.deepEqual(out.view, view, `${label}: not allowed, so the view must be unchanged`);
        assert.deepEqual(out.effects, [], `${label}: not allowed, so no effects`);
        continue;
      }
      allowed++;
      const [next, effects] = expected.split('|');
      assert.equal(out.view.kind, next === '=' ? view.kind : next, `${label}: next view`);
      assert.deepEqual(out.effects.map(e => e.kind), effects.split(' ').filter(Boolean), `${label}: effects`);
    }
  }
  assert.equal(checked, Object.keys(VIEWS).length * Object.keys(INPUTS).length);
  assert.ok(allowed > 200, `the table should be wide (${allowed})`);
});

test('every table row names a real view and a real input', () => {
  for (const [view, row] of Object.entries(ALLOWED)) {
    assert.ok(view in VIEWS, view);
    for (const input of Object.keys(row)) assert.ok(input in INPUTS, `${view}: ${input}`);
  }
  for (const view of Object.keys(VIEWS)) assert.ok(view in ALLOWED, `${view} has a row`);
});

// ---- focused ------------------------------------------------------------------------------------------------------------------
const step = (view: SetupView, input: SetupInput) => stepSetup(view, input, NOW);
const kinds = (effects: readonly SetupEffect[]) => effects.map(e => e.kind);

test('back from an own unapproved attempt abandons it and offers both kinds, keeping the name', () => {
  for (const view of [VIEWS['awaiting.started'], VIEWS['awaiting.live'], VIEWS['ended.denied'], VIEWS['failed.unapproved']]) {
    const out = step(view, {kind: 'back'});
    assert.deepEqual(kinds(out.effects), ['abandon']);
    assert.deepEqual(out.view, {kind: 'form', mode: 'choose', name: 'Den'});
  }
});

test('back from a held-by-nobody view abandons nothing', () => {
  for (const view of [VIEWS['busy.timed'], VIEWS['busy.untimed'], VIEWS['ended.superseded'], VIEWS['failed.none']]) {
    const out = step(view, {kind: 'back'});
    assert.deepEqual(out.effects, []);
    assert.equal(out.view.kind, 'form');
    assert.equal((out.view as {mode: string}).mode, 'choose');
  }
});

test('back after approval never abandons: the server refuses, so the controller re-reads and shows the server phase', () => {
  for (const view of [VIEWS.finishing, VIEWS.exhausted]) {
    const out = step(view, {kind: 'back'});
    assert.deepEqual(kinds(out.effects), ['read-state']);
    assert.deepEqual(out.view, view);
  }
  const committed = step(VIEWS['failed.committed'], {kind: 'back'});
  assert.deepEqual(kinds(committed.effects), ['read-state']);
  assert.equal(committed.view.kind, 'connecting');
  // and what the server then says is shown as-is, never the form
  assert.equal(step(committed.view, own('finishing')).view.kind, 'finishing');
  // a refused abandon (an approval raced the Back) goes back to reading too
  assert.deepEqual(kinds(step(VIEWS.form, {kind: 'abandonRefused'}).effects), ['read-state']);
});

test('start again after an ended attempt is the same S8 abandon, except when the attempt was superseded', () => {
  assert.deepEqual(kinds(step(VIEWS['ended.denied'], {kind: 'startAgain'}).effects), ['abandon']);
  assert.deepEqual(kinds(step(VIEWS['ended.superseded'], {kind: 'startAgain'}).effects), []);
  for (const outcome of ['expired', 'approval_lapsed', 'cancelled'] as const) {
    assert.deepEqual(kinds(step({kind: 'ended', outcome, name: 'n'}, {kind: 'back'}).effects), ['abandon'], outcome);
  }
});

test('the read cadence: 2 s, backing off to 15 s, and a server floor is never shortened or capped', () => {
  assert.equal(SETUP_READ_FLOOR_MS, 2000);
  assert.equal(SETUP_READ_CEILING_MS, 15_000);
  assert.deepEqual([0, 1, 2, 3, 4, 50].map(n => setupReadDelay(n)), [2000, 4000, 8000, 15_000, 15_000, 15_000]);
  assert.deepEqual([0, 3, 50].map(n => setupReadDelay(n, 60_000)), [60_000, 60_000, 60_000]);
  assert.equal(setupReadDelay(3, 1000), 15_000);
  assert.equal(setupReadDelay(0, 1500), 2000);
  assert.equal(setupReadDelay(0, 2500), 2500);
  assert.equal(setupReadDelay(Number.NaN), 2000);
  assert.equal(setupReadDelay(-4, Number.NaN), 2000);

  // walking a view through repeated readings follows the cadence
  let view: SetupView = {kind: 'awaiting_approval', name: 'Den', serverPhase: 'awaiting_approval', polls: 0};
  const delays: number[] = [];
  for (let i = 0; i < 5; i++) {
    const out = step(view, own('awaiting_approval'));
    view = out.view;
    const wait = out.effects.find(e => e.kind === 'schedule-read');
    assert.ok(wait && wait.kind === 'schedule-read');
    delays.push(wait.afterMs);
  }
  assert.deepEqual(delays, [4000, 8000, 15_000, 15_000, 15_000]);

  // the server's retryAfterSeconds is a floor on a reading, and the view remembers when
  const busy = step(VIEWS.connecting, INPUTS['state.other']);
  assert.deepEqual(busy.view, {kind: 'busy_elsewhere', polls: 0, retryAt: NOW + 30_000});
  assert.deepEqual(busy.effects, [{kind: 'schedule-read', afterMs: 30_000}]);
  const again = step(busy.view, {kind: 'state', own: false, state: reading('in_progress', undefined, 1)});
  assert.deepEqual(again.effects, [{kind: 'schedule-read', afterMs: 4000}], 'a smaller floor never shortens our own backoff');
  const long = step(busy.view, {kind: 'state', own: false, state: reading('in_progress', undefined, 1500)});
  assert.deepEqual(long.effects, [{kind: 'schedule-read', afterMs: 1_500_000}], 'no ceiling on a server floor');
  // a failed read honours Retry-After too
  const failedRead = step(VIEWS['awaiting.live'], {kind: 'readFailed', error: err(true, 40_000)});
  assert.deepEqual(failedRead.effects, [{kind: 'schedule-read', afterMs: 40_000}]);
  // a start refused because another device holds setup carries the floor as well
  const refused = step(VIEWS['starting.hosted.start'], {kind: 'setupInProgress', retryAfterSeconds: 90});
  assert.deepEqual(refused.view, {kind: 'busy_elsewhere', polls: 0, retryAt: NOW + 90_000});
  assert.deepEqual(refused.effects, [{kind: 'schedule-read', afterMs: 90_000}]);
});

test('an unknown status or phase gives unknown, from every view that reads, and Retry reads again', () => {
  for (const name of ['connecting', 'busy.timed', 'awaiting.started', 'awaiting.live', 'finishing', 'exhausted']) {
    for (const input of [INPUTS['state.unknown-status'], INPUTS['state.unknown-phase']]) {
      const out = step(VIEWS[name], input);
      assert.deepEqual(out.view, {kind: 'unknown'}, name);
      assert.deepEqual(out.effects, [], name);
    }
  }
  assert.deepEqual(step({kind: 'unknown'}, {kind: 'retry'}), {view: {kind: 'connecting'}, effects: [{kind: 'read-state'}]});
});

test('no path offers the other kind: nothing switches a setup from one kind to the other', () => {
  const mode = (view: SetupView) => (view.kind === 'starting' || view.kind === 'form' ? view.mode : undefined);
  for (const [viewName, view] of Object.entries(VIEWS)) {
    for (const [inputName, input] of Object.entries(INPUTS)) {
      const label = `${viewName} × ${inputName}`;
      const out = step(view, input);
      // only the form ever starts a kind, and only by the person's own submit
      if (input.kind === 'submitHosted' || input.kind === 'submitLocal' || input.kind === 'chooseKind') {
        if (view.kind !== 'form') assert.deepEqual(out, {view, effects: []}, label);
      }
      // a hosted setup never turns into a local one, and the reverse
      if (view.kind === 'starting' && out.view.kind === 'starting') assert.equal(mode(out.view), mode(view), label);
      // local owner creation is only ever asked for by a local form's own start
      if (out.effects.some(e => e.kind === 'initialize-local')) assert.deepEqual([viewName, inputName], ['starting.local.start', 'startDone'], label);
      if (out.effects.some(e => e.kind === 'initialize-hosted')) assert.ok(viewName.startsWith('starting.hosted') || (view.kind === 'failed' && view.retry.to.kind === 'starting' && view.retry.to.mode === 'hosted'), label);
      // every way back into the form offers both kinds, never one of them
      if (out.view.kind === 'form' && view.kind !== 'form') assert.equal(out.view.mode, 'choose', label);
      // only Back ever abandons, and only to the form
      if (out.effects.some(e => e.kind === 'abandon')) assert.ok(['back', 'startAgain'].includes(input.kind) && out.view.kind === 'form', label);
    }
  }
  // the input vocabulary has no switch, use-local or fallback intent
  const vocabulary = Object.values(INPUTS).map(i => i.kind.toLowerCase());
  for (const word of vocabulary) assert.ok(!/switch|fallback|uselocal|usehosted/.test(word), word);
  // a failed hosted setup offers Try again and Back, and Back lands on the form with both kinds
  const hostedFailure = step(VIEWS['starting.hosted.approval'], INPUTS['failed.request-approval']).view;
  assert.equal(hostedFailure.kind, 'failed');
  assert.deepEqual(step(hostedFailure, {kind: 'back'}).view, {kind: 'form', mode: 'choose', name: 'Den'});
  assert.deepEqual(step(hostedFailure, {kind: 'submitLocal', name: 'Den'}), {view: hostedFailure, effects: []});
});

test('the form: choosing a kind is client-only, and a submit starts that kind', () => {
  const form = {kind: 'form', mode: 'choose', name: 'Den', earlierExpired: true} as const;
  assert.deepEqual(step(form, {kind: 'chooseKind', mode: 'local'}), {view: {...form, mode: 'local'}, effects: []});
  assert.deepEqual(step(form, {kind: 'submitHosted', name: 'Kitchen', navigate: false}),
    {view: {kind: 'starting', mode: 'hosted', name: 'Kitchen', navigate: false, step: 'start'}, effects: [{kind: 'start'}]});
  assert.deepEqual(step(form, {kind: 'submitLocal', name: 'Kitchen'}),
    {view: {kind: 'starting', mode: 'local', name: 'Kitchen', navigate: false, step: 'start'}, effects: [{kind: 'start'}]});
});

test('reconcile: a stale own record becomes the form, quietly, and only a never-approved expiry shows the earlier-expired line', () => {
  assert.deepEqual(step(VIEWS.connecting, INPUTS['state.open']).view, {kind: 'form', mode: 'choose', name: 'Den'});
  assert.deepEqual(step(VIEWS.connecting, INPUTS['state.expired']).view, {kind: 'form', mode: 'choose', name: 'Den', earlierExpired: true});
  assert.deepEqual(step(VIEWS.connecting, {kind: 'tokenRefused'}).view, {kind: 'form', mode: 'choose', name: ''});
  assert.deepEqual(kinds(step(VIEWS.connecting, INPUTS['state.open']).effects), ['retire-record', 'retire-attempt']);
});

test('reconcile: a completed server shows the library only when the owner session is already held', () => {
  assert.equal(step(VIEWS.connecting, INPUTS['state.complete.held']).view.kind, 'library');
  const refused = step(VIEWS.connecting, INPUTS['state.complete']);
  assert.deepEqual(refused.view, {kind: 'refused', reason: 'already_set_up'});
  assert.deepEqual(kinds(refused.effects), ['retire-record', 'retire-attempt']);
});

test('reconcile: the server fact "slow" and the web reload both land in the right place', () => {
  const slow = step(VIEWS.connecting, {kind: 'state', own: true, state: reading('in_progress', {phase: 'finishing', requestId: 'r1', revision: '1', cancelling: false, renewable: false, delivery: {slow: true}})});
  assert.deepEqual(slow.view, {kind: 'finishing', slow: true, polls: 0});
  const reload = step(VIEWS.connecting, own('awaiting_approval', {state: phase('awaiting_approval', '2026-10-31T12:30:00Z')}));
  assert.deepEqual(reload.view, {kind: 'awaiting_approval', name: 'Den', serverPhase: 'awaiting_approval', polls: 0, expiresAt: '2026-10-31T12:30:00Z'});
});

test('a denial is the server going back to started after an approval request; before one it is just waiting', () => {
  const waitingStarted = step(VIEWS['awaiting.started'], INPUTS['state.started']);
  assert.equal(waitingStarted.view.kind, 'awaiting_approval');
  assert.equal(step(VIEWS['awaiting.live'], INPUTS['state.started']).view.kind, 'ended');
  assert.deepEqual(step(VIEWS['awaiting.live'], INPUTS['state.started']).view, {kind: 'ended', outcome: 'denied', name: 'Den'});
  assert.deepEqual(step(VIEWS['awaiting.live'], INPUTS['state.expired']).view, {kind: 'ended', outcome: 'expired', name: 'Den'});
  assert.deepEqual(step(VIEWS.finishing, INPUTS['state.expired']).view, {kind: 'ended', outcome: 'approval_lapsed', name: 'Den'});
  assert.deepEqual(step(VIEWS['awaiting.live'], INPUTS['state.other']).view, {kind: 'ended', outcome: 'superseded', name: 'Den'});
});

test('hosted web happy path: submit, bootstrap, request approval, leave, return, exchange, adopt, library, done', () => {
  const effects: string[] = [];
  let view = initialSetupView as SetupView;
  const kindNow = () => view.kind;
  const feed = (input: SetupInput) => { const out = step(view, input); view = out.view; effects.push(...kinds(out.effects)); };
  feed({kind: 'start'});
  feed(INPUTS['state.open']);
  feed({kind: 'submitHosted', name: 'Den', navigate: true});
  feed({kind: 'startDone'});
  feed({kind: 'initializeDone'});
  feed(INPUTS.approvalReady);
  assert.equal(kindNow(), 'awaiting_approval');
  feed(own('approved'));
  assert.equal(kindNow(), 'finishing');
  feed(own('installed'));
  assert.equal(kindNow(), 'admitting');
  feed({kind: 'exchangeDone'});
  feed({kind: 'ownerAdopted'});
  assert.equal(kindNow(), 'library');
  feed({kind: 'skip'});
  assert.equal(kindNow(), 'done');
  assert.deepEqual(effects, ['read-state', 'retire-record', 'retire-attempt', 'start', 'initialize-hosted', 'request-approval', 'navigate', 'schedule-read',
    'schedule-read', 'exchange', 'adopt-owner', 'retire-record', 'retire-attempt']);
});

test('local happy path: one start, one owner, no approval leg', () => {
  const effects: string[] = [];
  let view = {kind: 'form', mode: 'local', name: 'Den'} as SetupView;
  const kindNow = () => view.kind;
  const feed = (input: SetupInput) => { const out = step(view, input); view = out.view; effects.push(...kinds(out.effects)); };
  for (const input of [INPUTS.submitLocal, INPUTS.startDone, INPUTS.initializeDone, INPUTS.ownerAdopted]) feed(input);
  assert.equal(kindNow(), 'library');
  assert.deepEqual(effects, ['start', 'initialize-local', 'adopt-owner', 'retire-record', 'retire-attempt']);
});

test('a failure keeps what to repeat: Try again repeats the same step, and an unconfirmed result keeps its place', () => {
  const failed = step(VIEWS['starting.hosted.approval'], {kind: 'effectFailed', effect: 'request-approval', error: err(true, 5000)});
  assert.equal(failed.view.kind, 'failed');
  const retried = step(failed.view, {kind: 'retry'});
  assert.deepEqual(retried.view, VIEWS['starting.hosted.approval']);
  assert.deepEqual(retried.effects, [{kind: 'request-approval', navigate: true}]);
  const final = step(VIEWS['starting.hosted.approval'], {kind: 'effectFailed', effect: 'request-approval', error: err(false)});
  assert.equal(step(final.view, {kind: 'retry'}).view, final.view);
});

test('the exchange: a lapsed completion renews, anything else fails, and renewal comes back to the exchange', () => {
  assert.deepEqual(step(admittingView, INPUTS['exchangeRefused.lapsed']), {view: {kind: 'renewing'}, effects: [{kind: 'renew'}]});
  assert.equal(step(admittingView, INPUTS['exchangeRefused.other']).view.kind, 'failed');
  assert.deepEqual(step({kind: 'renewing'}, {kind: 'renewDone'}), {view: {kind: 'admitting'}, effects: [{kind: 'exchange'}]});
});

test('dispose cancels pending work from every view and changes nothing else', () => {
  for (const [name, view] of Object.entries(VIEWS)) assert.deepEqual(step(view, {kind: 'dispose'}), {view, effects: [{kind: 'cancel-pending'}]}, name);
});

test('the reducer is deterministic and its answers are plain data', () => {
  for (const [viewName, view] of Object.entries(VIEWS)) {
    for (const [inputName, input] of Object.entries(INPUTS)) {
      const a = step(view, input), b = step(view, input);
      assert.deepEqual(a, b, `${viewName} × ${inputName}`);
      assert.deepEqual(JSON.parse(JSON.stringify(a)), a, `${viewName} × ${inputName}: serialisable`);
    }
  }
});
