import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

test('an opened Troubleshooting viewer starts where the landing\'s first group does, and its way back is a button, not a bar over the heading', () => {
  const css = readFileSync(new URL('../src/screens/settings/server/troubleshooting/troubleshooting.module.css', import.meta.url), 'utf8');
  const kit = readFileSync(new URL('../src/kit/settings/settings.module.css', import.meta.url), 'utf8');
  // The landing's groups start at the frame's body with no margin of their own; the viewer starts there too.
  assert.match(kit, /\.group \{ margin: 0; min-width: 0; \}/);
  assert.match(css, /\.page \{ display: flex; flex-direction: column; gap: 22px; \}/);
  assert.match(css, /\.back \{ align-self: flex-start; \}/);
  const page = readFileSync(new URL('../src/screens/settings/server/troubleshooting/index.tsx', import.meta.url), 'utf8');
  assert.match(page, /<Button className=\{styles\.back\} variant="glass" size="small" icon="i-left" onClick=\{\(\) => setView\(undefined\)\}>/);
});
