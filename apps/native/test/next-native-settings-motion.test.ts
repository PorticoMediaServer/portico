import assert from 'node:assert/strict';
import test from 'node:test';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {nativeAppearanceMotion} from '../src-next/screens/settings/appearance-policy';
import {createTheme} from '../src-next/kit/theme';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = <T>() => {let resolve!: (value: T) => void, reject!: (error: unknown) => void; const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
function snapshot(motion?: 'follow' | 'reduce') {
  const effective = motion === undefined ? {'playback.skipBackMs': 10000} : {'appearance.motion': motion};
  const key = Object.keys(effective)[0]!;
  return parsePreferenceSnapshot({deviceClass: 'mobile', deviceBound: true, registryRevision: 'motion-fixture', registry: {revision: 'motion-fixture', fields: [
    {key, type: motion === undefined ? 'number' : 'string', default: effective[key as keyof typeof effective], group: 'appearance', labelKey: `preferences.${key}`, scopes: ['profile-device']},
  ]}, documents: ['profile-server', 'profile-device-class', 'profile-device'].map(scope => ({scope, revision: 1, activeRevision: 1, digest: 'd'.repeat(64), values: scope === 'profile-device' ? effective : {}})),
    effective, effectiveSource: {[key]: 'profile-device'}, clampedFields: []});
}

test('motion policy reads only the published canonical value, without inventing an older registry value', () => {
  assert.equal(nativeAppearanceMotion(snapshot('follow')), 'follow');
  assert.equal(nativeAppearanceMotion(snapshot('reduce')), 'reduce');
  assert.equal(nativeAppearanceMotion(snapshot()), undefined);
});

// The presenter runs on the installed Fabric renderer, so React owns the store
// subscription, the OS listener and their cleanup on unmount.
async function fixture(inheritedReduced = false, supported = true) {
  let motion: 'follow' | 'reduce' | undefined = 'follow', event: ((value: boolean) => void) | undefined, removals = 0;
  const pending = deferred<boolean>(), errors: unknown[] = [], listeners = new Set<() => void>();
  const inherited = createTheme({isTV: false, width: 834, textScale: 1.25, reduceMotion: inheritedReduced});
  const source = {get appearanceMotion() {return motion;}, subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, reportError: (error: unknown) => {errors.push(error);}};
  const native = {isReduceMotionEnabled() {assert.equal(this, native); return pending.promise;}, addEventListener(name: string, listener: (value: boolean) => void) {
    assert.equal(this, native); assert.equal(name, 'reduceMotionChanged'); event = listener; return {remove() {removals++;}};
  }};
  const {NativeSettingsAppearance} = await screenModule(new URL('../src-next/screens/settings/NativeSettingsAppearance.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {AccessibilityInfo: supported ? native : undefined},
    '../../kit/theme': {ThemeProvider: 'ThemeProvider', useTheme: () => inherited},
  });
  const child = React.createElement('existing-shell');
  const host = await fabricHost();
  let element: any;
  const Appearance = () => {element = NativeSettingsAppearance({source, children: child}); return element;};
  await host.render(React.createElement(Appearance));
  const view = {get tree() {return host.root[0];}, unmount: () => host.unmount(),
    async act(work: () => void) {work(); await host.settle();}};
  // The caller's child is the same committed host for the whole theme lifetime.
  const childHost = view.tree.children[0];
  return {get tree() {return view.tree;}, get element() {return element;}, get childHost() {return childHost;}, pending, errors, listeners, child, inherited, source,
    act: view.act,
    event: () => event!, removals: () => removals,
    policy: async (value: typeof motion) => {await view.act(() => {motion = value; for (const listener of listeners) listener();});},
    close: () => view.unmount(),
  };
}

test('confirmed motion and genuine OS events drive the same theme without changing its canvas, scale or child', async () => {
  const f = await fixture();
  try {
    assert.equal(f.tree.type, 'ThemeProvider');
    assert.equal(f.tree.props.width, f.inherited.width); assert.equal(f.tree.props.textScale, 1.25);
    assert.equal(f.element.props.children, f.child, 'the presenter offers the exact caller child');
    assert.equal(f.tree.children[0], f.childHost, 'the caller child host is not replaced');
    assert.equal(f.tree.props.reduceMotion, false);
    await f.policy('reduce'); assert.equal(f.tree.props.reduceMotion, true);
    assert.equal(f.tree.children[0], f.childHost, 'a policy change does not replace the child');
    await f.policy('follow'); assert.equal(f.tree.props.reduceMotion, false);
    await f.act(() => f.event()(true)); assert.equal(f.tree.props.reduceMotion, true);
    await f.act(() => f.pending.resolve(false)); assert.equal(f.tree.props.reduceMotion, true, 'a late initial query cannot undo a newer OS event');
    await f.act(() => f.event()(false)); assert.equal(f.tree.props.reduceMotion, false);
    await f.policy(undefined); assert.equal(f.tree.props.reduceMotion, false);
    assert.deepEqual(f.errors, []);
  } finally {await f.close();}
  assert.equal(f.removals(), 1); assert.equal(f.listeners.size, 0);
});

test('retirement removes both subscriptions and rejects late OS answers while inherited reduced motion stays authoritative', async () => {
  const f = await fixture(true), queued = f.event();
  assert.equal(f.tree.props.reduceMotion, true);
  await f.close();
  await f.act(() => {queued(false); f.pending.resolve(false);});
  assert.equal(f.removals(), 1); assert.equal(f.listeners.size, 0); assert.deepEqual(f.errors, []);
});

test('an unavailable OS API preserves the current theme and an actual read refusal uses existing feedback', async () => {
  const unavailable = await fixture(true, false);
  assert.equal(unavailable.tree.props.reduceMotion, true); await unavailable.close();
  const f = await fixture(), error = new Error('native accessibility read refused');
  try {await f.act(() => f.pending.reject(error)); assert.deepEqual(f.errors, [error]); await f.policy('reduce'); assert.equal(f.tree.props.reduceMotion, true);}
  finally {await f.close();}
});
