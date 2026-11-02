import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// W-3 / J-3: the standard backdrop is body::before at z-index -1, which paints under the body's own
// background. A body background (the boot style once set `html, body { background }`) turns every
// page without artwork into flat ink, so only html carries the first-paint colour.
const index = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
const backdrop = readFileSync(new URL('../src/kit/backdrop.css', import.meta.url), 'utf8');

test('the first-paint style never gives the body a background', () => {
  const style = index.match(/<style[^>]*>([\s\S]*?)<\/style>/)?.[1] ?? '';
  for (const [, selectors, body] of style.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const targetsBody = selectors!.split(',').some(selector => /(^|\s)body\s*$/.test(selector.trim()));
    if (targetsBody) assert.doesNotMatch(body!, /background/, `"${selectors!.trim()}" paints over the backdrop`);
  }
  assert.match(style, /html\s*\{[^}]*background:\s*#071018/, 'html keeps the ink for the first paint');
});

test('the shared backdrop keeps the body transparent over its glows', () => {
  assert.match(backdrop, /html body\s*\{\s*background:\s*transparent;?\s*\}/);
  assert.match(backdrop, /body::before[^{]*\{[^}]*z-index:\s*-1/);
});
