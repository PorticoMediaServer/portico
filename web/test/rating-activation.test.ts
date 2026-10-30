import test from 'node:test';
import assert from 'node:assert/strict';
import {Frame, elements} from './choice-handlers.ts';
const {WatchEndCardView} = await import('../src/screens/watch/EndCard.tsx');

test('rating arrows/Home/End preview focus with zero writes; activation commits once', async () => {
  const writes: number[] = [], frame = new Frame();
  const render = () => WatchEndCardView({card: {title: 'Film', related: [], rating: {value: 6, async set(value: number) {writes.push(value);}}, back() {}, openRelated() {}},
    canReplay: false, onReplay() {}, reportError(error) {throw error;}, artwork() {return undefined;}});
  let focused = 2;
  const mount = () => {
    const radios = elements(frame.render(render)).filter(row => row.type === 'button' && row.props.role === 'radio');
    radios.forEach((row, i) => row.props.ref({focus() {focused = i; row.props.onFocus();}}));
    assert.equal(radios.filter(row => row.props.tabIndex === 0).length, 1);
    return radios;
  };
  let radios = mount();
  for (const [key, expected] of [['ArrowRight', 3], ['ArrowDown', 4], ['ArrowRight', 0], ['ArrowLeft', 4], ['ArrowUp', 3], ['Home', 0], ['End', 4]] as const) {
    let prevented = false, stopped = false;
    radios[focused].props.onKeyDown({key, preventDefault() {prevented = true;}, stopPropagation() {stopped = true;}});
    assert.equal(focused, expected); assert.equal(prevented, true); assert.equal(stopped, true); assert.deepEqual(writes, []);
    radios = mount(); assert.equal(radios[focused].props.tabIndex, 0); assert.equal(radios[focused].props.disabled, false);
  }
  radios[focused].props.onClick();
  assert.deepEqual(writes, [10]);
  await Promise.resolve();
});
