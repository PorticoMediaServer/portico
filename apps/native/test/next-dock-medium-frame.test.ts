import test from 'node:test';
import assert from 'node:assert/strict';
import {dockBarFrame, mediumDockMetrics} from '../src-next/kit/dock/model.ts';

test('the medium dock sits at the foot of the content beside the 84 pt rail', () => {
  assert.deepEqual(mediumDockMetrics, {height: 66, gap: 8, button: 44, maxWidth: 540});
  assert.deepEqual(dockBarFrame('tablet', undefined, 34), {left: 100, right: 16, bottom: 18, maxWidth: 540});
  assert.deepEqual(dockBarFrame('tablet', 92, 34), {left: 100, right: 16, bottom: 18, maxWidth: 540}, 'the rail owns the foot, not the tab bar');
});

test('compact keeps the phone dock above the tab bar', () => {
  assert.deepEqual(dockBarFrame('phone', undefined, 34), {left: 8, right: 8, bottom: 92});
  assert.deepEqual(dockBarFrame('phone', 92, 0), {left: 8, right: 8, bottom: 92});
});

test('the web dock sits at the content foot beside its sidebar', () => {
  assert.deepEqual(dockBarFrame('web', undefined, 0), {left: 84, right: 0, bottom: 0});
  assert.deepEqual(dockBarFrame('web', 92, 34, 260), {left: 260, right: 0, bottom: 0});
});
