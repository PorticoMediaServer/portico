import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createHookHost} from './hook-driver.ts';

/**
 * The driver's render pass is React's: an update that scheduled the render is spent on entry,
 * and only a render-phase state adjustment made while the body runs retries it. Effects queued
 * by a discarded pass leave with that pass and never commit.
 */

test('an ordinary update renders the body once and commits its effect once', async () => {
  let renders = 0, setups = 0;
  let update!: (value: number) => void;
  const host = createHookHost(() => {
    renders++;
    const [value, setValue] = React.useState(0);
    update = setValue;
    React.useEffect(() => {setups++;}, [value]);
  });
  try {
    host.mount();
    assert.equal(renders, 1, 'the mount renders once');
    assert.equal(setups, 1, 'the mount commits its effect once');

    await act(() => {update(1);});
    assert.equal(renders, 2, 'the ordinary update renders exactly once');
    assert.equal(setups, 2, 'the update commits its replacement exactly once');
  } finally {
    host.unmount();
  }
});

test('a render-phase adjustment retries the body exactly once', () => {
  let renders = 0;
  const host = createHookHost(() => {
    renders++;
    const [value, setValue] = React.useState(0);
    if (value === 0) setValue(1);
  });
  try {
    host.mount();
    assert.equal(renders, 2, 'the adjustment retries once and the settled pass ends the render');
  } finally {
    host.unmount();
  }
});

test('an effect queued by a discarded render pass never commits', () => {
  const committed: number[] = [];
  let renders = 0;
  const host = createHookHost(() => {
    const pass = ++renders;
    const [value, setValue] = React.useState(0);
    React.useEffect(() => {committed.push(pass);}, [pass]);
    if (value === 0) setValue(1);
  });
  try {
    host.mount();
    assert.deepEqual(committed, [2], 'only the settled pass runs its effect');
  } finally {
    host.unmount();
  }
});
