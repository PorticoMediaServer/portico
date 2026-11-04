import assert from 'node:assert/strict';
import test from 'node:test';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {railHoldHooks} from '../src-next/kit/navigation/rail-hold-hooks';

// tablet-play6: a page's open side panel folds the expanded sidebar to the rail; it opens again when the panel closes.
const {RailHoldProvider, useRailHold, useRailHeld} = railHoldHooks(React as any);
const jsx = (type: any, props: any, ...children: any[]) => React.createElement(type, props, ...children);

test('the rail is held while any page holds it, and released when the last one lets go or unmounts', async () => {
  const host = await fabricHost(), seen: boolean[] = [];
  function Reader() {seen.push(useRailHeld()); return null;}
  function Page({active}: {active: boolean}) {useRailHold(active); return null;}
  const tree = (a: boolean, b: boolean | undefined) => jsx(RailHoldProvider, null, jsx(Reader, null), jsx(Page, {active: a}), b === undefined ? null : jsx(Page, {active: b}));
  try {
    await host.render(tree(false, false)); await host.settle();
    assert.equal(seen.at(-1), false, 'nothing holds it');
    await host.render(tree(true, true)); await host.settle();
    assert.equal(seen.at(-1), true);
    await host.render(tree(false, true)); await host.settle();
    assert.equal(seen.at(-1), true, 'one panel still open');
    await host.render(tree(false, undefined)); await host.settle();
    assert.equal(seen.at(-1), false, 'the last holder unmounted');
  } finally {await host.unmount();}
});

test('outside a shell the hold is a no-op and nothing is held', async () => {
  const host = await fabricHost(), seen: boolean[] = [];
  function Page() {useRailHold(true); seen.push(useRailHeld()); return null;}
  try {await host.render(jsx(Page, null)); await host.settle(); assert.equal(seen.at(-1), false);} finally {await host.unmount();}
});
