import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {assertTVFocusGuideRule, tvosNativeProps} from './helpers/tvos-focus-guide.mjs';

// CoveredLayer provides whether its subtree is covered (W-9); the shallow hook host has no createContext.
const withContext = (react: any) => {const out = {...react, createContext: (value: unknown) => ({value, Provider: 'CoveredProvider'})}; return {...out, default: out};};

async function layer(tv: boolean) {
  const h = hooks();
  const {CoveredLayer} = await screenModule(new URL('../src-next/kit/focus/CoveredLayer.tsx', import.meta.url), {
    react: withContext(h.react), 'react-native': {TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => ({isTV: tv})}});
  return (covered: boolean) => h.render(() => CoveredLayer({covered, children: 'page', testID: 'base'})) as any;
}

test('a covered TV layer blocks D-pad focus into everything under it, and an uncovered one does not', async () => {
  const render = await layer(true);
  const covered = render(true), open = render(false);
  assert.equal(covered.type, 'TVFocusGuideView');
  assert.equal(covered.props.focusable, false, 'Android FOCUS_BLOCK_DESCENDANTS / tvOS unfocusable');
  assert.equal(covered.props.importantForAccessibility, 'no-hide-descendants');
  assert.equal(open.type, covered.type, 'the same element covered or not: the page underneath is never remounted');
  assert.equal(open.props.focusable, true);
  assert.equal(open.props.importantForAccessibility, 'auto');
  assert.equal(open.props.destinations, undefined, 'no guide destinations: the container never takes focus itself');
  assert.equal(open.props.autoFocus, true, 'every TV layer remembers its last focused element');
});

test('W-9/W-12: every TV layer remembers its last focused element, so an empty layer never takes focus itself', async () => {
  const h = hooks();
  const {CoveredLayer} = await screenModule(new URL('../src-next/kit/focus/CoveredLayer.tsx', import.meta.url), {
    react: withContext(h.react), 'react-native': {TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => ({isTV: true})}});
  const routes = h.render(() => CoveredLayer({covered: false, children: null, testID: 'routes'})) as any;
  assert.equal(routes.props.autoFocus, true);
  assert.equal(routes.props.focusable, true);
  const host = {current: null};
  const base = h.render(() => CoveredLayer({covered: false, hostRef: host, children: 'page', testID: 'native-shell-base'})) as any;
  assert.equal(base.props.ref ?? base.ref, host, 'the shell can claim focus into its base page (W-12 fallback)');
});

test('TV focus review §4.4.1: on tvOS an uncovered layer is selectable again, so a route page still works after Back', async () => {
  assertTVFocusGuideRule(assert);
  const render = await layer(true);
  // Collections, See all and the reader pass no option: every CoveredLayer now behaves this way.
  const covered = render(true), open = render(false);
  assert.deepEqual(tvosNativeProps(covered.props), {isTVSelectable: false, autoFocus: true}, 'covered: its subtree is hidden from the focus engine');
  assert.deepEqual(tvosNativeProps(open.props), {isTVSelectable: true, autoFocus: true}, 'uncovered: its subtree takes focus again');
});

test('touch has no D-pad: the layer only hides from screen readers and touches', async () => {
  const render = await layer(false);
  const covered = render(true);
  assert.equal(covered.type, 'View');
  assert.equal(covered.props.pointerEvents, 'none');
  assert.equal(covered.props.accessibilityElementsHidden, true);
});

test('W-9 (2eec0520): a covered layer tells every control in it that it is covered, so none answers OK or draws a ring', async () => {
  const h = hooks();
  const module = await screenModule(new URL('../src-next/kit/focus/CoveredLayer.tsx', import.meta.url), {
    react: withContext(h.react), 'react-native': {TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => ({isTV: true})}});
  const provided = (element: any) => [element.props.children].flat()[0].props.value;
  assert.equal(provided(h.render(() => module.CoveredLayer({covered: true, children: 'page'}))), true, 'covered: its controls are inert');
  assert.equal(provided(h.render(() => module.CoveredLayer({covered: false, children: 'page'}))), false, 'uncovered: its controls work');
});

// 4 Nov (refused-link page as a native Modal): under a page Modal the shell's layers are covered by context only. The
// platform keeps D-pad focus out of them; blocking natively would move tvOS focus (D7). #610/#616's inert-when-uncovered
// wrappers and the Android focus stop are gone with the presented layer they served.
test('CoveredSignal covers by context only: its controls go inert, with no view and no focus guide', async () => {
  const h = hooks();
  const module = await screenModule(new URL('../src-next/kit/focus/CoveredLayer.tsx', import.meta.url), {
    react: withContext(h.react), 'react-native': {TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => ({isTV: true})}});
  for (const covered of [true, false]) {
    const element = h.render(() => module.CoveredSignal({covered, children: 'player'})) as any;
    assert.notEqual(element.type, 'View'); assert.notEqual(element.type, 'TVFocusGuideView');
    assert.equal(element.props.value, covered, covered ? 'held: its controls are inert' : 'not held: they work');
    assert.deepEqual([element.props.children].flat(), ['player'], 'the same children either way: nothing remounts');
  }
});
