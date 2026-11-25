import assert from 'node:assert/strict';
import test, {mock} from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';
import {watchHooks as hooks} from './helpers/watch-hooks.mjs';

// #5329 review: a kept hand-over page must end on its own, with no playback update to redraw its host.
test('a kept page ends at its time on its own timer, and one already past its time draws nothing', async () => {
  mock.timers.enable({apis: ['setTimeout', 'Date'], now: 1_000});
  const h = hooks();
  try {
    const {KeptPage} = await screenModule(new URL('../src-next/pages/shared/player/KeptPage.tsx', import.meta.url), {react: h.react});
    const page = h.react.createElement('Page', {testID: 'kept'});
    let tree = h.render(() => KeptPage({until: 9_000, children: page}));
    assert.equal(tree, page);
    mock.timers.tick(7_999); tree = h.flush(); assert.equal(tree, page);
    mock.timers.tick(1); tree = h.flush(); assert.equal(tree, null, 'gone at its time with nothing else redrawing it');
    h.unmount();
    const late = hooks();
    try {assert.equal(late.render(() => KeptPage({until: 500, children: page})), null);} finally {late.unmount();}
  } finally {mock.timers.reset();}
});
