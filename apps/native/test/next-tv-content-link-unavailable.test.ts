import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// The TV page for a refused content link (N5-1's "lands on Home without a message" on TV): a presented layer in the
// TV layer stack, focus on Home, Back the same as Home.
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];

test('a refused link on TV draws its page as a presented layer that takes focus on Home, and Home or Back closes it', async () => {
  const host = hooks(), presented: {key: string; claim: () => boolean}[] = [], backs: (() => boolean)[] = [], focused: string[] = [];
  const effects: (() => void | (() => void))[] = [];
  const react = {...host.react, useLayoutEffect: (effect: () => void | (() => void)) => {effects.push(effect);}};
  const {TVContentLinkUnavailable} = await screenModule(new URL('../src-next/screens/share/NativeContentLinks.tsx', import.meta.url), {
    react: {...react, default: react}, 'react-native': {View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'},
    '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({focusRegion: (id: string) => {focused.push(id); return true;}}),
      presentTVLayer: (key: string, claim: () => boolean) => {presented.push({key, claim}); return () => {presented.splice(presented.findIndex(value => value.claim === claim), 1);};}},
    '../../kit/navigation/back': {registerBack: (handler: () => boolean, options: {scope: string}) => {assert.equal(options.scope, 'overlay'); backs.push(handler); return () => {backs.splice(backs.indexOf(handler), 1);};}},
    '../../kit/overlays/TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'}, '../../kit/states': {StateLayout: 'StateLayout', StateView: 'StateView'},
    '../../kit/theme': {useTheme: () => ({isTV: true})}, '../../platform/content-links': {nativeContentLinks: {}}, './share-open': {openContentLink: async () => undefined},
  });
  let homes = 0;
  const tree = host.render(() => TVContentLinkUnavailable({title: 'No access', body: 'Ask the owner', onHome: () => {homes++;}}));
  const cleanups = effects.map(effect => effect());
  assert.deepEqual(presented.map(value => value.key), ['content-link-unavailable'], 'a presented layer over the shell');
  assert.equal(presented[0]!.claim(), true); assert.deepEqual(focused, ['content-link-unavailable'], 'its claim focuses its own region');
  const region = nodes(tree).find(node => node.type === 'FocusRegion');
  assert.equal(region.props.id, 'content-link-unavailable'); assert.equal(region.props.entry, 'home'); assert.equal(region.props.hold, true);
  assert.ok(nodes(tree).some(node => node.type === 'TVDesignCanvas'), 'drawn in the TV design canvas');
  const layout = nodes(tree).find(node => node.type === 'StateLayout');
  assert.equal(layout.props.title, 'No access'); assert.equal(layout.props.primaryAction.id, 'home');
  layout.props.primaryAction.onPress(); assert.equal(homes, 1, 'Home closes it');
  assert.equal(backs.length, 1); assert.equal(backs[0]!(), true); assert.equal(homes, 2, 'Back does the same as Home');
  for (const cleanup of cleanups) if (typeof cleanup === 'function') cleanup();
  assert.deepEqual(presented, [], 'closed: the layer goes, and the stack returns focus');
  assert.deepEqual(backs, []);
});
