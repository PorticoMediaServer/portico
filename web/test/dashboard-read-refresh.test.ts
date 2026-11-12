import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer} from './helpers/interaction-harness.mjs';

// A Dashboard read refreshed after an action (Fix permissions, Retry) asks the server: the held copy from before the
// action would still show the alert the action just cleared.
test('the first read of a Dashboard section may use the held copy, and a refresh after an action always asks the server', async () => {
  const g = globalThis as {document?: unknown}, had = g.document;
  g.document = {hidden: false, addEventListener() {}, removeEventListener() {}};
  const h = createRenderer(), reads: boolean[] = [];
  const module = await componentModule(new URL('../src/screens/dashboard/use-read.ts', import.meta.url), {react: h.react, './pages': {readDashboardAccounts: async () => []}});
  const bindings = {isCurrent: () => true, events: undefined, effects: {reportError(error: unknown) { throw error; }}};
  const load = async (_signal: AbortSignal, fresh: boolean) => {reads.push(fresh); return 'status';};
  let refresh = () => {};
  function Section() { const state = module.useDashboardRead(bindings, load, true); refresh = state.refresh; return null; }
  try {
    h.render(() => h.react.createElement(Section, {})); await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(reads, [false], 'opening the Dashboard may show the held copy');
    refresh(); h.render(() => h.react.createElement(Section, {})); await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(reads, [false, true], 'after an action the section is read from the server');
  } finally {h.dispose(); g.document = had;}
});

test('a section the server\'s changes keep current reads on its slow timer only while the change feed is not connected', async () => {
  const g = globalThis as {document?: unknown; setInterval: typeof setInterval; clearInterval: typeof clearInterval}, had = g.document, realSet = g.setInterval, realClear = g.clearInterval;
  g.document = {hidden: false, addEventListener() {}, removeEventListener() {}};
  const ticks: (() => void)[] = [];
  g.setInterval = ((fn: () => void) => {ticks.push(fn); return ticks.length;}) as unknown as typeof setInterval; g.clearInterval = (() => {}) as typeof clearInterval;
  const h = createRenderer(), reads: boolean[] = [];
  let connected = true;
  try {
    const module = await componentModule(new URL('../src/screens/dashboard/use-read.ts', import.meta.url), {react: h.react, './pages': {readDashboardAccounts: async () => []}});
    const bindings = {isCurrent: () => true, events: {on: () => () => {}, getStatus: () => ({connected})}, effects: {reportError(error: unknown) { throw error; }}};
    const load = async (_signal: AbortSignal, fresh: boolean) => {reads.push(fresh); return 'status';};
    function Section() { module.useDashboardRead(bindings, load, true, 30000, undefined, true); return null; }
    h.render(() => h.react.createElement(Section, {})); await new Promise(resolve => setImmediate(resolve));
    assert.equal(reads.length, 1);
    ticks.at(-1)!(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(reads.length, 1, 'connected: the timer reads nothing');
    connected = false; ticks.at(-1)!(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(reads.length, 2, 'feed down: the timer reads');
  } finally {h.dispose(); g.document = had; g.setInterval = realSet; g.clearInterval = realClear;}
});
