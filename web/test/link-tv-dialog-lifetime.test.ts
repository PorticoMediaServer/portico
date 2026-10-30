/**
 * FX-160 / BLK-WEB-005: Link a TV keeps one lifecycle owner for each committed open.
 *
 * The actual LinkTvDialog is run through the interaction harness with its link service stubbed:
 * closing, an external close, an api replacement and unmount retire the opening (aborting its
 * signal), late preview/decision results never publish into a successor, duplicate clicks issue
 * one request, and a current review approves once with the reviewed requestId/userCode.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, one, tick, tree} from './helpers/interaction-harness.mjs';

type Deferred<T> = {promise: Promise<T>; resolve(value: T): void; reject(error?: unknown): void};
const deferred = <T,>(): Deferred<T> => {
  let resolve!: (value: T) => void, reject!: (error?: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
};

async function mount(initialCode?: string) {
  const h = createRenderer(), opened: boolean[] = [], ports: any[] = [];
  const effects = new Set<{run: () => any; cleanup?: () => void}>();
  const layout = h.react.useLayoutEffect;
  h.react.useLayoutEffect = (run: () => any, deps: unknown[]) => layout(() => {
    const held = {run, cleanup: run()};effects.add(held);
    return () => {effects.delete(held);held.cleanup?.();};
  }, deps);
  const replayLayout = () => {
    for (const held of effects) held.cleanup?.();
    for (const held of effects) held.cleanup = held.run();
  };
  const watchLinkTv = (api: unknown) => {
    const port: any = {api, previews: [] as any[], decisions: [] as any[]};
    port.preview = (code: string, life?: {signal: AbortSignal}) => {
      const pending = deferred<any>();
      port.previews.push({code, life, ...pending});
      return pending.promise;
    };
    port.decide = (requestId: string, code: string, decision: string, life?: {signal: AbortSignal}) => {
      const pending = deferred<void>();
      port.decisions.push({requestId, code, decision, life, ...pending});
      return pending.promise;
    };
    ports.push(port);
    return port;
  };
  const app = await componentModule(new URL('../src/screens/settings/LinkTvDialog.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../../kit/overlays': {Dialog: 'Dialog'},
    './LinkTvForm': {LinkTvForm: 'LinkTvForm'},
    '../watch/receivers': {watchLinkTv},
  });
  const {returnFocusRef} = {returnFocusRef: {current: null}};
  const api = {request: async () => undefined};
  // A layout-committed admission re-renders before paint: show() models that synchronous second
  // pass, so the returned tree carries the callbacks bound to the committed opening.
  const show: any = (open: boolean, target = api, seed: string | undefined = initialCode) => {
    const paint = () => tree(h.render(() => app.LinkTvDialog({open, onOpenChange: (value: boolean) => opened.push(value), api: target, returnFocusRef, initialCode: seed})));
    paint();
    return paint();
  };
  return {h, api, opened, ports, show, replayLayout};
}

const form = (rendered: any[]) => one(rendered, 'LinkTvForm').props;

test('close during preview retires it: reopen shows a clean code field and no old decision', async () => {
  const {opened, ports, show} = await mount();
  let v = show(true);
  form(v).onCodeChange('abcd');
  v = show(true);
  assert.equal(form(v).code, 'ABCD', 'owner normalises the raw input');
  form(v).onReview();
  v = show(true);
  assert.equal(form(v).phase, 'busy');
  const first = ports[0];
  assert.equal(first.previews.length, 1);
  one(v, 'Dialog').props.onOpenChange(false);
  assert.equal(first.previews[0].life.signal.aborted, true, 'closing aborts the opening signal');
  v = show(false);
  first.previews[0].resolve({requestId: 'old_request', userCode: 'ABCD', deviceName: 'Old TV'});
  await tick();
  v = show(true);
  assert.equal(ports.length, 2, 'a new open owns a new lifecycle');
  assert.equal(form(v).code, '');
  assert.equal(form(v).preview, undefined);
  assert.equal(form(v).phase, 'code');
  assert.deepEqual(first.decisions, [], 'the retired preview never reaches a decision');
  assert.deepEqual(opened, [false], 'the dialog reported its close once');
});

test('an approval that resolves after close/reopen never marks the successor done', async () => {
  const {opened, ports, show} = await mount();
  let v = show(true);
  form(v).onCodeChange('WXYZ');
  v = show(true);
  form(v).onReview();
  const first = ports[0];
  first.previews[0].resolve({requestId: 'r_old', userCode: 'WXYZ', deviceName: 'Old TV'});
  await tick();
  v = show(true);
  assert.equal(form(v).phase, 'review');
  form(v).onDecide('approve');
  v = show(true);
  assert.equal(form(v).phase, 'busy');
  assert.equal(first.decisions.length, 1);
  one(v, 'Dialog').props.onOpenChange(false);
  v = show(false);
  v = show(true);
  assert.equal(ports.length, 2);
  first.decisions[0].resolve();
  await tick();
  v = show(true);
  assert.equal(form(v).phase, 'code', 'the successor stays on its own clean code step');
  assert.deepEqual(opened, [false], 'the late decision does not close the successor');
});

test('external open=false aborts the live opening', async () => {
  const {ports, show} = await mount();
  let v = show(true);
  form(v).onCodeChange('ABCD');
  v = show(true);
  form(v).onReview();
  assert.equal(ports[0].previews[0].life.signal.aborted, false);
  show(false);
  assert.equal(ports[0].previews[0].life.signal.aborted, true);
});

test('replacing the api retires the original opening before starting the next', async () => {
  const {ports, show} = await mount();
  const secondApi = {request: async () => undefined};
  let v = show(true);
  form(v).onCodeChange('ABCD');
  v = show(true);
  form(v).onReview();
  v = show(true, secondApi);
  v = show(true, secondApi);
  assert.equal(ports.length, 2);
  assert.equal(ports[0].previews[0].life.signal.aborted, true);
  assert.notEqual(ports[1].api, ports[0].api, 'the successor keeps its own api context');
  assert.equal(form(v).phase, 'code');
  assert.equal(form(v).code, '');
});

test('duplicate reviews issue one request', async () => {
  const {ports, show} = await mount();
  let v = show(true);
  form(v).onCodeChange('ABCD');
  v = show(true);
  form(v).onReview();
  form(v).onReview();
  assert.equal(ports[0].previews.length, 1);
});

test('callbacks rendered for a closed opening cannot act on its successor', async () => {
  const {opened, ports, show} = await mount();
  let v = show(true);
  form(v).onCodeChange('ABCD');
  v = show(true);
  const stale = form(v), staleDialog = one(v, 'Dialog').props;
  one(v, 'Dialog').props.onOpenChange(false);
  v = show(false);
  v = show(true);
  v = show(true);
  assert.equal(ports.length, 2);
  const successorCode = form(v).code, successorPhase = form(v).phase;
  stale.onCodeChange('ZZZZ');
  stale.onReview();
  stale.onDecide('approve');
  staleDialog.onOpenChange(false);
  v = show(true);
  assert.equal(ports[1].previews.length, 0, 'a stale review never starts a successor request');
  assert.equal(ports[1].decisions.length, 0);
  assert.equal(form(v).code, successorCode);
  assert.equal(form(v).phase, successorPhase);
  assert.deepEqual(opened, [false], 'the stale dialog close does not close the successor');
});

test('callbacks rendered before an api replacement cannot touch the new opening', async () => {
  const {opened, ports, show} = await mount();
  const secondApi = {request: async () => undefined};
  let v = show(true);
  form(v).onCodeChange('ABCD');
  v = show(true);
  form(v).onReview();
  const stale = form(v), staleDialog = one(v, 'Dialog').props;
  v = show(true, secondApi);
  v = show(true, secondApi);
  assert.equal(ports.length, 2);
  assert.equal(ports[0].previews[0].life.signal.aborted, true, 'the replaced opening is retired in layout');
  stale.onCodeChange('WXYZ');
  stale.onReview();
  stale.onDecide('approve');
  staleDialog.onOpenChange(false);
  v = show(true, secondApi);
  assert.equal(ports[1].previews.length, 0, 'the old opening cannot reach the successor port');
  assert.equal(form(v).code, '');
  assert.deepEqual(opened, [], 'the stale close is ignored');
});

test('a current review approves once with its captured requestId and reviewed userCode', async () => {
  const {ports, show} = await mount();
  let v = show(true);
  form(v).onCodeChange('abcd');
  v = show(true);
  form(v).onReview();
  const port = ports[0];
  port.previews[0].resolve({requestId: 'r_live', userCode: 'ABCD', deviceName: 'Living room TV', platform: 'tvos'});
  await tick();
  v = show(true);
  assert.equal(form(v).preview.requestId, 'r_live');
  form(v).onCodeChange('ZZZZ');
  form(v).onDecide('approve');
  assert.equal(port.decisions.length, 1);
  assert.equal(port.decisions[0].requestId, 'r_live');
  assert.equal(port.decisions[0].code, 'ABCD', 'decide uses the reviewed preview userCode, not entry state');
  port.decisions[0].resolve();
  await tick();
  v = show(true);
  assert.equal(form(v).phase, 'done');
});

test('an entry code seeds only the first committed opening, display-only', async () => {
  const {api, opened, ports, show} = await mount('ABCD-EFGH');
  let v = show(true);
  assert.equal(form(v).code, 'ABCD-EFGH', 'the captured entry code is shown in the field');
  assert.equal(form(v).phase, 'code');
  assert.equal(form(v).preview, undefined);
  assert.equal(ports[0].previews.length, 0, 'prefill starts no review');
  assert.equal(ports[0].decisions.length, 0, 'prefill records no approval');
  v = show(true, api, 'ZZZZ-ZZZZ');
  assert.equal(form(v).code, 'ABCD-EFGH', 'a later prop change never adopts the open dialog');
  one(v, 'Dialog').props.onOpenChange(false);
  v = show(false, api, 'ZZZZ-ZZZZ');
  v = show(true, api, 'ZZZZ-ZZZZ');
  assert.equal(form(v).code, '', 'the seed is consumed once; reopens start empty like Settings');
  assert.equal(ports[0].previews.length, 0);
  assert.deepEqual(opened, [false]);
});

test('layout replay creates a fresh live port without losing the original display draft', async () => {
  const {api, ports, show, replayLayout} = await mount('ABCD-EFGH');
  let v = show(true);const stale = form(v);
  replayLayout();v = show(true);
  assert.equal(ports.length, 2);assert.equal(form(v).code, 'ABCD-EFGH');
  stale.onReview();assert.equal(ports[0].previews.length, 0);assert.equal(ports[1].previews.length, 0);
  form(v).onCodeChange('JKLM-NPQR');v = show(true);
  replayLayout();v = show(true, api, 'ZZZZ-ZZZZ');
  assert.equal(form(v).code, 'JKLM-NPQR');assert.equal(ports.length, 3);
  form(v).onReview();assert.equal(ports[2].previews.length, 1);assert.equal(ports[2].previews[0].life.signal.aborted, false);
  show(false);assert.equal(ports[2].previews[0].life.signal.aborted, true);
});
