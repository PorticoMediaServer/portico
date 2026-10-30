import {defaultI18n} from '../../../packages/i18n/src';
import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter.ts';
import {createDeviceEqualizer} from '../platform/equalizer-store.ts';
import * as equalizerControls from '../src-next/screens/watch/equalizer-controls.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const theme = {isTV: true, platform: 'tv', textScale: 1, colors: {text: '#edf5fa', textSecondary: '#abc', textTertiary: '#789', ink: '#071018', accentStrong: '#5cb3e2', danger: '#f00', line: '#234', lineStrong: '#345', raised: '#123', raisedHigh: '#234'}};
const type = (value: any, scale = 1) => ({fontSize: value.size * scale});

test('TV code entry exposes counts, hides the digits and announces changes and rejection', async () => {
  const host = hooks(), spoken: string[] = [];
  let pending: (() => void)[] = [], previous: readonly unknown[] | undefined;
  const react = {...host.react, useEffect(callback: () => void, deps: readonly unknown[]) {
    if (!previous || deps.some((value, index) => previous![index] !== value)) pending.push(callback);
    previous = deps;
  }};
  const {TVCodeBoxes} = await screenModule(new URL('../src-next/tv/auth/TVNumericPad.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View', AccessibilityInfo: {announceForAccessibility: (value: string) => spoken.push(value)}},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {}, '../../kit/focus': {}, '../../kit/focus/initial-entry': {}, '../../kit/Icon': {}, '../../kit/theme': {nativeType: type, useTheme: () => theme},
  });
  const render = (code: string, error = false, masked = true) => {
    const tree = host.render(() => TVCodeBoxes({code, length: masked ? 4 : 6, masked, busy: false, error}));
    const effects = pending; pending = []; effects.forEach(run => run()); return tree;
  };
  render(''); const tree = render('92');
  assert.equal(tree.props.accessible, true);
  assert.deepEqual(tree.props.accessibilityValue, {min: 0, max: 4, now: 2, text: '2 of 4 digits entered'});
  assert.equal(spoken.length, 1); assert.match(spoken[0], /2 of 4 digits entered/); assert.ok(!spoken[0].includes('92'));
  assert.ok(tree.props.children[0].every((digit: any) => digit.props.accessibilityElementsHidden && digit.props.importantForAccessibility === 'no-hide-descendants'));
  render('9'); assert.match(spoken.at(-1)!, /1 of 4 digits entered/);
  render('9', true); assert.ok(spoken.at(-1)!.endsWith(defaultI18n.t('signIn.error.pinInvalid')));
  const count = spoken.length; render('9', true); assert.equal(spoken.length, count, 'unchanged rerenders do not repeat the announcement');
  const verification = render('731', false, false);
  assert.equal(verification.props.accessibilityValue.text, '3 of 6 digits entered');
  render('731', true, false); assert.ok(spoken.at(-1)!.endsWith(defaultI18n.t('signIn.error.codeInvalid')));
  assert.ok(spoken.every(value => !value.includes('731')));
});

test('real tail-retry hook accepts native key-up, deduplicates pairs and checks original focused host', async () => {
  const registry = createFocusRegistry(); let listener: ((event: any) => void) | undefined, cleanup: (() => void) | undefined, retried = 0;
  registry.configure({id: 'row', order: ['first', 'tail']});
  registry.attach({regionId: 'row', childId: 'first'}, {requestTVFocus() {}});
  const tail = {requestTVFocus() {}}; registry.attach({regionId: 'row', childId: 'tail'}, tail); registry.didFocus({regionId: 'row', childId: 'tail'});
  const {useRowPageBoundary} = await screenModule(new URL('../src-next/kit/focus/page-boundary.ts', import.meta.url), {
    react: {default: {}, useLayoutEffect: (effect: () => (() => void)) => {cleanup = effect();}},
    'react-native': {findNodeHandle: (host: unknown) => host === tail ? 7 : 8, TVEventHandler: {addListener: (callback: typeof listener) => {listener = callback; return {remove() {listener = undefined;}};}}},
    '../../platform/tv-key-adapter': {createTVKeyAdapter}, '../../platform/tv-key-source': {tvKeySource: 'native'},
  });
  useRowPageBoundary(true, registry, 'row', 'tail', {hasMore: true, failed: true, loading: false, waitingId: 'cursor', requestNext: () => {retried++;}});
  listener!({eventType: 'right', eventKeyAction: 1, tag: 7}); assert.equal(retried, 1);
  listener!({eventType: 'right', eventKeyAction: 0, tag: 7}); listener!({eventType: 'right', eventKeyAction: 1, tag: 7}); assert.equal(retried, 2);
  listener!({eventType: 'right', eventKeyAction: 1, tag: 9}); assert.equal(retried, 2);
  registry.didFocus({regionId: 'row', childId: 'first'}); listener!({eventType: 'right', eventKeyAction: 1}); assert.equal(retried, 2);
  cleanup!(); assert.equal(listener, undefined);
});

test('equalizers have separate held roots, explicit entry and gated release-only band edits', async () => {
  let native = {on: true, preamp: 0, gains: Array(10).fill(0)}, writes = 0;
  const store = createDeviceEqualizer({read: async () => native, write: async value => {writes++; return native = {...value, gains: [...value.gains]};}, reset: async () => native});
  await store.refresh();
  async function fixture(instance: string) {
    const host = hooks(), keys = createTVKeyAdapter(); let handler: (event: any) => void = () => {};
    const entries: unknown[] = [];
    const {NativeWatchEqualizer} = await screenModule(new URL('../src-next/screens/watch/Equalizer.tsx', import.meta.url), {
      react: {...host.react, useId: () => instance}, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', Platform: {OS: 'android'}},
      'react-native-gesture-handler': {}, 'react-native-svg': {}, 'react-native-reanimated': {},
      '../../platform/playback/equalizer': {nativeEqualizer: store}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'},
      '../../kit/focus': {FocusProvider: 'FocusProvider', FocusRegion: 'FocusRegion'}, '../../kit/Icon': {}, '../../kit/theme': {useTheme: () => theme, nativeType: type}, '../../kit/useMotion': {},
      '../../platform/tv-keys': {useTVKeyEvent: (callback: (event: any) => void) => {handler = raw => {const event = keys.read(raw); if (event) callback(event);};}},
      '../../kit/focus/useDestinationEntryFocus': {useDestinationEntryFocus: (...args: unknown[]) => entries.push(args)}, './equalizer-controls': equalizerControls, './equalizer-gesture-paint': {},
    });
    const copy = {title: 'Equalizer', bands: 'Bands', preamp: 'Preamp', reset: 'Reset', thisDevice: 'This device', musicAndAudiobooks: 'Music & audiobooks', frequency: String, gain: String};
    return {render: (active = true) => host.render(() => NativeWatchEqualizer({store, copy, active, reportError(error: unknown) {throw error;}, onBack() {}})), key: (raw: unknown) => handler(raw), entries};
  }
  const settings = await fixture('settings'), playing = await fixture('playing');
  const first = settings.render(), second = playing.render();
  const regions = (tree: unknown) => nodes(tree).filter(node => node.type === 'FocusRegion');
  assert.notEqual(regions(first)[0].props.id, regions(second)[0].props.id);
  assert.equal(regions(first)[0].props.hold, true);
  const entry = nodes(second).find(node => typeof node.type === 'function' && node.type.name === 'EqualizerEntry');
  entry.type(entry.props); assert.deepEqual(playing.entries[0], [{visible: true, intent: 'enter', revision: 0}, {regionId: 'equalizer.playing.actions', childId: 'equalizer-enabled'}]);
  const band = nodes(second).find(node => node.props.target === 2); band.props.onFocus(2);
  playing.key({eventType: 'up', eventKeyAction: 1});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(store.getSnapshot().value!.gains[2], .5);
  playing.render(false); playing.key({eventType: 'up', eventKeyAction: 1});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(writes, 1, 'a retained covered editor cannot change the device');
  assert.ok(regions(playing.render(false)).every(region => region.props.enabled === false));
});
