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
