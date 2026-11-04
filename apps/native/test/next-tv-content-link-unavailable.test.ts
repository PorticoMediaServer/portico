import test from 'node:test';
import assert from 'node:assert/strict';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';
import {tvRefusedPage} from './helpers/tv-refused-page.mjs';

// The TV page for a refused content link (N5-1's "lands on Home without a message" on TV): a full-screen opaque native
// Modal (DECISIONS, 4 Nov), focus on Home, Back the same as Home, and focus back where it was once it has gone. Runs
// the real page, overlay lifecycle and native dismissal on the fake engine's window model (D12-D15).
async function scene(platform: 'android' | 'tvos', options?: {wait: number}) {
  const kit = await tvFocusKit({platform}), {engine, e, focus, Stop} = kit, n = engine.native;
  const page = await tvRefusedPage(kit, options), registry = focus.createFocusRegistry();
  const Shell = ({refused}: {refused: boolean}) => e(focus.FocusProvider, {registry}, e(n.View, {style: {flex: 1}},
    e(focus.FocusRegion, {id: 'home.row', orientation: 'horizontal', style: {flexDirection: 'row', gap: 20}}, e(Stop, {id: 'film'}), e(Stop, {id: 'show'})),
    refused ? e(page.Refused, {}) : null));
  const held = () => page.pageModal.tvPageModal.getSnapshot();
  return {engine, e, Shell, held};
}

test('a refused link on TV is a full-screen opaque Modal that takes focus on Home; Home or Back closes it and focus returns', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    for (const key of ['select', platform === 'android' ? 'back' : 'menu'] as const) {
      const {engine, e, Shell, held} = await scene(platform), label = `${platform}, ${key}`;
      await engine.render(e(Shell, {refused: false}));
      await engine.focus('show');
      await engine.render(e(Shell, {refused: true}));
      assert.equal(engine.focused, 'refused-home', `${label}: focus on Home`);
      assert.equal(engine.windows().length, 1, `${label}: its own window`);
      assert.equal(held(), true, `${label}: the shell's layers are covered while it is up`);
      await engine.press('right');
      assert.equal(engine.focused, 'refused-home', `${label}: nothing under it takes focus`);
      await engine.press(key);
      if (platform === 'tvos') {
        assert.equal(engine.focused, 'refused-home', `${label}: focus waits for the dismissal`);
        await engine.finishDismissals();
      }
      assert.equal(engine.windows().length, 0, label);
      assert.equal(held(), false, label);
      assert.equal(engine.focused, 'show', `${label}: focus back where it was`);
    }
  }
});

test('tvOS Menu: UIKit dismisses the page itself and reports onDismiss before onRequestClose; the page goes at once, not on the fallback', async () => {
  const {engine, e, Shell, held} = await scene('tvos', {wait: 60_000});
  await engine.render(e(Shell, {refused: false}));
  await engine.focus('show');
  await engine.render(e(Shell, {refused: true}));
  assert.equal(engine.focused, 'refused-home');
  await engine.press('menu');
  assert.equal(engine.focused, 'refused-home', 'focus waits for the dismissal');
  await engine.finishDismissals();
  assert.equal(held(), false, 'the page has gone: its already-reported dismissal counts');
  assert.equal(engine.windows().length, 0);
  assert.equal(engine.focused, 'show', 'focus back where it was');
  await engine.unmount();
});

test('the refused page is an opaque, unanimated, full-screen Modal', async () => {
  const {engine, e, Shell} = await scene('android');
  await engine.render(e(Shell, {refused: true}));
  const [modal] = engine.windowProps();
  assert.equal(modal.transparent, false, 'opaque: nothing under it shows');
  assert.equal(modal.animationType, 'none');
  assert.equal(typeof modal.onRequestClose, 'function');
  assert.equal(typeof modal.onDismiss, 'function');
});
