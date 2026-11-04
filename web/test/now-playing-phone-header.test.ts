import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// Now playing's `phone` layout is every width below 1024 (WatchPlayback), so the phone header's
// rules must not live only in the ≤599 px query: at 600–1023 the empty spacer would take grid
// column 3 and push More to a second row (#647 first pass). Source proof of where the rules sit;
// the browser check at 720 px is in the PR.
const css = readFileSync(new URL('../src/screens/watch/music/now-playing.module.css', import.meta.url), 'utf8');
function topLevelRules(source: string): string[] {
  const rules: string[] = []; let depth = 0, start = 0, inMedia = false;
  for (let i = 0; i < source.length; i++) {
    const c = source[i];
    if (c === '{') { if (depth === 0) inMedia = source.slice(start, i).trim().startsWith('@'); depth++; }
    else if (c === '}') { depth--; if (depth === 0) { if (!inMedia) rules.push(source.slice(start, i + 1).trim()); start = i + 1; } }
  }
  return rules;
}

test('the phone header grid and its hidden spacer apply at every phone width, not only at 599 px and below', () => {
  const rules = topLevelRules(css.replace(/\/\*[\s\S]*?\*\//g, ''));
  const rule = (selector: string) => rules.find(text => text.startsWith(selector + ' {'));
  assert.match(rule('.frame[data-phone] .top') ?? '', /grid-template-columns: 44px minmax\(0,1fr\) 44px/);
  assert.match(rule('.frame[data-phone] .spacer') ?? '', /display: none/, 'the spacer leaves the grid at 600–1023 px too');
  assert.match(rule('.frame[data-phone] .top > .glass') ?? '', /grid-column: 3/, 'More keeps the third column');
});
