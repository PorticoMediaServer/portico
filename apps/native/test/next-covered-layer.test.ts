import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

async function layer(tv: boolean) {
  const h = hooks();
  const {CoveredLayer} = await screenModule(new URL('../src-next/kit/focus/CoveredLayer.tsx', import.meta.url), {
    react: h.react, 'react-native': {TVFocusGuideView: 'TVFocusGuideView', View: 'View'}, '../theme': {useTheme: () => ({isTV: tv})}});
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

test('touch has no D-pad: the layer only hides from screen readers and touches', async () => {
  const render = await layer(false);
  const covered = render(true);
  assert.equal(covered.type, 'View');
  assert.equal(covered.props.pointerEvents, 'none');
  assert.equal(covered.props.accessibilityElementsHidden, true);
});
