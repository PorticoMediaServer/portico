import assert from 'node:assert/strict';
import {test} from 'node:test';
import {componentModule} from '../helpers/component-harness.mjs';
import {createRenderer, one, tree} from '../helpers/interaction-harness.mjs';

test('ReaderHighlightsExport defers blob URL revocation by 60s after download click', async t => {
  const h = createRenderer();
  t.after(() => h.dispose());

  const created = [], revoked = [];
  let clicked = 0;
  const origCreate = URL.createObjectURL, origRevoke = URL.revokeObjectURL;
  const origDocument = globalThis.document;
  URL.createObjectURL = () => {const url = `blob:export-${created.length + 1}`; created.push(url); return url;};
  URL.revokeObjectURL = url => {revoked.push(url);};
  globalThis.document = {
    createElement: () => ({href: '', download: '', rel: '', click() {clicked++;}, remove() {}}),
    body: {append() {}},
  };
  t.after(() => {URL.createObjectURL = origCreate; URL.revokeObjectURL = origRevoke; globalThis.document = origDocument;});

  const module = await componentModule(new URL('../../src/screens/read/ReaderHighlightsExport.tsx', import.meta.url), {
    react: h.react,
    '@core/reading-highlights-export.ts': {collectReadingHighlights: async () => ({markdown: '# Notes', fileName: 'notes.md'})},
    '../../kit/controls': {Button: 'Button'},
  });
  const signal = new AbortController().signal;
  const service = {
    port: {signal: new AbortController().signal},
    current: () => true,
    getSnapshot: () => ({loading: false, saving: false, revision: 1}),
  };
  const render = () => tree(h.render(() => module.ReaderHighlightsExport({
    service, signal, reportError: () => {},
  })));
  render();
  t.mock.timers.enable({apis: ['setTimeout']});
  const button = one(render(), 'Button');
  button.props.onClick();
  for (let i = 0; i < 6; i++) await Promise.resolve();
  assert.equal(clicked, 1);
  assert.deepEqual(created, ['blob:export-1']);
  assert.deepEqual(revoked, [], 'URL must not be revoked synchronously right after link.click()');
  t.mock.timers.tick(59_999);
  assert.deepEqual(revoked, []);
  t.mock.timers.tick(1);
  assert.deepEqual(revoked, ['blob:export-1']);
});
