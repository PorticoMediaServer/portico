import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

test('an opened Troubleshooting viewer starts where the landing\'s first group does, and its way back is a button, not a bar over the heading', () => {
  const css = readFileSync(new URL('../src/screens/settings/server/troubleshooting/troubleshooting.module.css', import.meta.url), 'utf8');
  const kit = readFileSync(new URL('../src/kit/settings/settings.module.css', import.meta.url), 'utf8');
  const gap = kit.match(/\.group:first-child \{ margin-top: (\d+px); \}/)?.[1];
  assert.ok(gap);
  assert.match(css, new RegExp(`\\.page \\{ display: flex; flex-direction: column; gap: 22px; margin-top: ${gap}; \\}`));
  assert.match(css, /\.back \{ align-self: flex-start; \}/);
  const page = readFileSync(new URL('../src/screens/settings/server/troubleshooting/index.tsx', import.meta.url), 'utf8');
  assert.match(page, /<Button className=\{styles\.back\} variant="glass" size="small" icon="i-left" onClick=\{\(\) => setView\(undefined\)\}>/);
});
