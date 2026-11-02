import test from 'node:test';
import assert from 'node:assert/strict';
import {readdirSync, readFileSync, statSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

// TV focus review step 0: no new hasTVPreferredFocus in screens/ (a region `entry` instead).
// tvOS re-applies an always-true preferred view on later layout changes and takes focus back
// (§4.4.2). Step 4 migrates the rest and lowers this to 0; lower it whenever a use is removed.
const baseline = 42;
const screens = fileURLToPath(new URL('../src-next/screens', import.meta.url));
const files = (directory: string): string[] => readdirSync(directory).flatMap(name => {
  const at = path.join(directory, name);
  return statSync(at).isDirectory() ? files(at) : /\.tsx?$/.test(name) ? [at] : [];
});

test('screens/ never gains a hasTVPreferredFocus', () => {
  const uses = files(screens).flatMap(file => readFileSync(file, 'utf8').split('\n').flatMap((line, index) =>
    /\bhasTVPreferredFocus\b/.test(line) ? [`${path.relative(screens, file)}:${index + 1}`] : []));
  assert.ok(uses.length <= baseline, `${uses.length} uses, baseline ${baseline}: give the FocusRegion an entry instead.\n${uses.join('\n')}`);
  if (uses.length < baseline) assert.fail(`${uses.length} uses now: lower the baseline in this test to ${uses.length}.`);
});
