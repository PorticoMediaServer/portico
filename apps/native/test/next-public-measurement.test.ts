import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {nativeMeasurementLaunch} from '../src-next/dev/measurement-launch';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
async function fixture() {
  const state = hooks(), layout: (() => void)[] = [], passive: (() => void)[] = [];
  const slots = new Set<{deps?: readonly unknown[]; cleanup?: () => void}>();
  const effect = (queue: (() => void)[]) => (run: () => void | (() => void), deps: readonly unknown[] = []) => {
    const slot = state.react.useRef({} as {deps?: readonly unknown[]; cleanup?: () => void}).current;
    slots.add(slot);
    if (!slot.deps || deps.some((value, index) => value !== slot.deps![index])) queue.push(() => {
      slot.cleanup?.(); slot.deps = deps; slot.cleanup = run() || undefined;
    });
  };
  const react = {...state.react, useEffect: effect(passive), useLayoutEffect: effect(layout)};
  let resolve!: (url: string | null) => void, reject!: (reason: unknown) => void, listener: ((event: {url: string}) => void) | undefined;
  const initial = new Promise<string | null>((yes, no) => {resolve = yes; reject = no;});
  const AccountEntry = () => {throw new Error('this routing fixture does not mount the independently checked auth leaf');};
  const {NativePublicMeasurementGate} = await screenModule(new URL('../src-next/dev/public-measurement.tsx', import.meta.url), {
    react, 'react-native': {Linking: {getInitialURL: () => initial, addEventListener: (_event: string, receive: typeof listener) => {
      listener = receive; return {remove: () => {if (listener === receive) listener = undefined;}};
    }}}, '../screens/accounts/AccountEntry': {AccountEntry}, './measurement-launch': {nativeMeasurementLaunch},
  });
  let normal = 0, disposed = 0;
  const predicates: (() => boolean)[] = [];
  const install = (current: () => boolean) => {predicates.push(current); return () => {disposed++;};};
  const render = () => state.render(() => NativePublicMeasurementGate({renderNormal: () => {
    normal++; assert.ok(predicates.every(current => !current()), 'old capture is closed before normal/auth rendering'); return 'normal-shell';
  }, install}));
  const flush = () => {layout.splice(0).forEach(run => run()); passive.splice(0).forEach(run => run());};
  return {render, flush, initial: resolve, reject, AccountEntry, predicates,
    url: (url: string) => listener?.({url}), normal: () => normal, disposed: () => disposed,
    unmount: () => {for (const slot of slots) {slot.cleanup?.(); slot.cleanup = undefined;}}, listener: () => listener,
  };
}

test('public measurement waits for initial URL and never invokes the normal/auth shell', async () => {
  const f = await fixture();
  try {
    assert.equal(f.render(), null); f.flush(); assert.equal(f.normal(), 0); assert.equal(f.predicates.length, 0);
    f.initial('portico://native-layout?specimen=signed-out-welcome'); await tick();
    const element = f.render(); f.flush();
    assert.equal(element.type, f.AccountEntry); assert.equal(element.props.initialStage, 'welcome'); assert.equal(element.props.initialAddress, '');
    assert.equal(element.props.onBack, undefined); assert.equal(f.normal(), 0); assert.equal(f.predicates[0](), true);
    assert.throws(element.props.onComplete, {code: 'cancelled'});
    f.url('portico://native-layout?specimen=signed-out-welcome'); const next = f.render();
    assert.notEqual(next.props.key, element.props.key); assert.equal(f.predicates[0](), false); f.flush();
    assert.equal(f.disposed(), 1); assert.equal(f.predicates[1](), true);
    f.url('portico://app'); assert.equal(f.render(), 'normal-shell');
    assert.equal(f.normal(), 1); assert.equal(f.predicates[1](), false); f.flush(); assert.equal(f.disposed(), 2);
  } finally {f.unmount();}
  assert.equal(f.listener(), undefined); assert.ok(f.predicates.every(current => !current()));
});

test('rejected or malformed measurement resolution stays closed and a late initial URL cannot override a newer event', async () => {
  const rejected = await fixture();
  try {
    rejected.render(); rejected.flush(); rejected.reject(new Error('Linking unavailable')); await tick();
    assert.equal(rejected.render(), null); rejected.flush(); assert.equal(rejected.normal(), 0); assert.equal(rejected.predicates.length, 0);
  } finally {rejected.unmount();}
  const f = await fixture();
  try {
    f.render(); f.flush();
    f.url('portico://native-layout?specimen=signed-out-welcome&server=private');
    f.initial(null); await tick(); assert.equal(f.render(), null); f.flush();
    assert.equal(f.normal(), 0); assert.equal(f.predicates.length, 0);
    f.url('portico://native-layout?specimen=signed-out-welcome'); f.render(); f.flush();
    const held = f.predicates[0]; assert.equal(held(), true); f.unmount(); assert.equal(held(), false);
  } finally {f.unmount();}
});

test('leaving genuine welcome retires capture once and internal Back cannot reopen it', async () => {
  const f = await fixture();
  try {
    f.render(); f.flush(); f.initial('portico://native-layout?specimen=signed-out-welcome'); await tick();
    const entry = f.render(); f.flush();
    const held = f.predicates[0];
    entry.props.onStageChange('welcome'); assert.equal(held(), true);
    entry.props.onStageChange('address');
    assert.equal(held(), false); assert.equal(f.disposed(), 1); assert.equal(f.normal(), 0);
    entry.props.onStageChange('welcome'); f.render(); f.flush();
    assert.equal(held(), false); assert.equal(f.predicates.length, 1);
    f.url('portico://native-layout?specimen=signed-out-welcome');
    const fresh = f.render(); f.flush();
    assert.notEqual(fresh.props.key, entry.props.key);
    assert.equal(f.predicates[1](), true);
    entry.props.onStageChange('credentials');
    assert.equal(f.predicates[1](), true, 'an old AccountEntry cannot retire the new launch');
  } finally {f.unmount();}
  assert.equal(f.disposed(), 2);
});
