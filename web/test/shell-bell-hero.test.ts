import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const shell = readFileSync(new URL('../src/shell/shell.module.css', import.meta.url), 'utf8');
const layout = readFileSync(new URL('../src/kit/layout/layout.module.css', import.meta.url), 'utf8');

test('the bell is a plain icon like Search and Saved: a button with no browser border or fill', () => {
  const icon = shell.slice(shell.indexOf('.icon {')).split('}')[0];
  assert.match(icon, /border: 0;/); assert.match(icon, /background: none;/); assert.match(icon, /padding: 0;/);
  assert.match(shell, /\.icon:hover, \.icon\[aria-current='page'\], \.icon\[aria-expanded='true'\] \{/);
});

test('below 1280 the hero thumbnails shrink so the strip clears My List (survey: overlap at 1024)', () => {
  const narrow = layout.slice(layout.lastIndexOf('@media (max-width: 1279px) {'));
  assert.match(narrow, /\.thumbnailImage img \{ width: 104px; \}/);
  assert.match(narrow, /height: 58px;/);
  // 5 × 104 + 4 × 12 = 568; at 1024 with a 32 px gutter the strip starts at x 424, past My List's right edge (~304).
  assert.ok(1024 - 32 - (5 * 104 + 4 * 12) > 304);
});
