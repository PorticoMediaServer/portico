import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

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
  assert.equal(open.props.autoFocus, undefined);
});

test('W-9/W-12: an autoFocus layer remembers its last focused element, so an empty layer never takes focus itself', async () => {
  const h = hooks();
  const {CoveredLayer} = await screenModule(new URL('../src-next/kit/focus/CoveredLayer.tsx', import.meta.url), {
    react: withContext(h.react), 'react-native': {TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => ({isTV: true})}});
  const routes = h.render(() => CoveredLayer({covered: false, autoFocus: true, children: null, testID: 'routes'})) as any;
  assert.equal(routes.props.autoFocus, true);
  assert.equal(routes.props.focusable, true);
  const host = {current: null};
  const base = h.render(() => CoveredLayer({covered: false, autoFocus: true, hostRef: host, children: 'page', testID: 'native-shell-base'})) as any;
  assert.equal(base.props.ref ?? base.ref, host, 'the shell can claim focus into its base page (W-12 fallback)');
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
