import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

test('A5: the filter groups sit on the panel\'s own 32px edge, as its title and footer do (desktop-browse #filters)', () => {
  const css = readFileSync(new URL('../src/kit/browse/browse.module.css', import.meta.url), 'utf8');
  assert.match(css, /^\.filterBuilder \{ padding: 0 0 24px; \}/m);
  assert.doesNotMatch(css, /^\.filterBuilder \{ padding: 0 32px 24px; \}/m);
  // Phone and TV keep their own insets.
  assert.match(css, /\.filterBuilder\[data-platform=phone\] \{ padding: 0 16px 16px; \}/);
  assert.match(css, /\.filterBuilder\[data-platform=tv\] \{ padding: 0; \}/);
});
